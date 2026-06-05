#include "pine/arena.hpp"

#include <doctest/doctest.h>

#include <atomic>
#include <cstddef>
#include <cstdint>
#include <thread>
#include <vector>

using namespace pine;

namespace {

// All bytes inside [p, p+n) of a freshly-bumped region must be writable
// without aliasing any other live allocation. We use this to verify
// non-overlap by stamping unique tags into each block and reading them
// back.
bool all_unique(const std::vector<std::pair<unsigned char*, std::size_t>>& blocks) {
  // O(n^2) overlap check; n is small in tests.
  for (std::size_t i = 0; i < blocks.size(); ++i) {
    auto [pi, ni] = blocks[i];
    for (std::size_t j = i + 1; j < blocks.size(); ++j) {
      auto [pj, nj] = blocks[j];
      // Disjoint iff one ends before the other begins.
      if (!(pi + ni <= pj || pj + nj <= pi)) {
        return false;
      }
    }
  }
  return true;
}

}  // namespace

TEST_CASE("RequestArena routes ArenaAllocator through the per-thread bump") {
  // Outside a RequestArena, allocator falls back to new_delete_resource.
  auto* before = current_resource();
  CHECK(current_central_arena() == nullptr);
  {
    RequestArena arena;
    CHECK(current_central_arena() != nullptr);
    CHECK(current_resource() != before);

    // ArenaAllocator is stateless; two instances compare equal.
    ArenaAllocator<int> a1;
    ArenaAllocator<int> a2;
    CHECK(a1 == a2);

    // Allocate from the bump and ensure the pointer is non-null and aligned.
    int* p = a1.allocate(16);
    REQUIRE(p != nullptr);
    CHECK(reinterpret_cast<std::uintptr_t>(p) % alignof(int) == 0);
    a1.deallocate(p, 16);  // no-op for arena
  }
  // Restored after RequestArena destructs.
  CHECK(current_resource() == before);
  CHECK(current_central_arena() == nullptr);
}

TEST_CASE("ThreadLocalBump hands out non-overlapping regions across many sizes") {
  RequestArena arena;
  ArenaAllocator<unsigned char> alloc;

  std::vector<std::pair<unsigned char*, std::size_t>> blocks;
  // Mix small, medium, and chunk-spanning allocations to force at least
  // one central-arena refill of the local buffer.
  const std::size_t sizes[] = {1, 7, 64, 4096, 50 * 1024, 8, 32 * 1024};
  for (std::size_t s : sizes) {
    auto* p = alloc.allocate(s);
    REQUIRE(p != nullptr);
    // Stamp the block with a tag tied to its index so any later overlap
    // would corrupt one of the witnesses.
    for (std::size_t i = 0; i < s; ++i) {
      p[i] = static_cast<unsigned char>(blocks.size() & 0xff);
    }
    blocks.emplace_back(p, s);
  }
  CHECK(all_unique(blocks));
  // Witnesses survive: stamps are still readable as their original index.
  for (std::size_t b = 0; b < blocks.size(); ++b) {
    auto [p, n] = blocks[b];
    for (std::size_t i = 0; i < n; ++i) {
      REQUIRE(p[i] == static_cast<unsigned char>(b & 0xff));
    }
  }
}

TEST_CASE("ScopedInstall isolates worker-thread allocations from caller bump") {
  RequestArena arena;
  auto* caller_resource = current_resource();
  auto* central = current_central_arena();
  REQUIRE(central != nullptr);

  std::atomic<bool> worker_ok{false};
  std::thread t([&]() {
    // Worker has no inherited tl_resource; install a local bump backed by
    // the same central arena.
    ScopedInstall guard(central);
    CHECK(current_resource() != caller_resource);
    ArenaAllocator<int> a;
    int* p = a.allocate(128);
    REQUIRE(p != nullptr);
    for (int i = 0; i < 128; ++i) {
      p[i] = i;
    }
    for (int i = 0; i < 128; ++i) {
      REQUIRE(p[i] == i);
    }
    worker_ok.store(true, std::memory_order_release);
    // ScopedInstall destructor restores the prior thread-local resource.
  });
  t.join();
  CHECK(worker_ok.load(std::memory_order_acquire));
  // Caller's tl_resource is unaffected by what the worker did.
  CHECK(current_resource() == caller_resource);
}

TEST_CASE("Many concurrent workers allocate from the same CentralArena without corruption") {
  RequestArena arena;
  auto* central = current_central_arena();
  REQUIRE(central != nullptr);

  constexpr int kThreads = 8;
  constexpr int kPerThread = 256;
  std::atomic<int> failures{0};

  std::vector<std::thread> ts;
  ts.reserve(kThreads);
  for (int t = 0; t < kThreads; ++t) {
    ts.emplace_back([&, t]() {
      ScopedInstall guard(central);
      ArenaAllocator<std::uint64_t> a;
      // Each worker stamps its own tag into its block; if two workers
      // ever returned overlapping memory, the tag-readback would mismatch.
      const std::uint64_t tag = (static_cast<std::uint64_t>(t) << 56) | 0xdeadbeefULL;
      for (int i = 0; i < kPerThread; ++i) {
        const std::size_t n = 1 + (i % 64);
        auto* p = a.allocate(n);
        for (std::size_t k = 0; k < n; ++k) {
          p[k] = tag ^ static_cast<std::uint64_t>(i * 31 + k);
        }
        for (std::size_t k = 0; k < n; ++k) {
          if (p[k] != (tag ^ static_cast<std::uint64_t>(i * 31 + k))) {
            failures.fetch_add(1, std::memory_order_relaxed);
            break;
          }
        }
      }
    });
  }
  for (auto& th : ts) {
    th.join();
  }
  CHECK(failures.load() == 0);
}

TEST_CASE("Nested RequestArena restores the outer bump on inner destruction") {
  // The outer arena's pointer must come back unchanged after the inner
  // scope exits, even though the inner arena swapped tl_resource and
  // tl_central in its constructor.
  CHECK(current_central_arena() == nullptr);
  {
    RequestArena outer;
    auto* outer_resource = current_resource();
    auto* outer_central = current_central_arena();
    REQUIRE(outer_central != nullptr);

    {
      RequestArena inner;
      CHECK(current_resource() != outer_resource);
      CHECK(current_central_arena() != outer_central);
    }

    CHECK(current_resource() == outer_resource);
    CHECK(current_central_arena() == outer_central);
  }
  CHECK(current_central_arena() == nullptr);
}

TEST_CASE("Allocations after a chunk boundary remain readable until arena destruction") {
  // The bump's local chunk is 32 KB. Allocate just enough small blocks
  // to span at least two chunks, write a witness into each, and then
  // confirm every witness still reads correctly. This catches a bug
  // where a refill stomps on the previous chunk's live region.
  RequestArena arena;
  ArenaAllocator<std::uint32_t> a;

  std::vector<std::pair<std::uint32_t*, std::size_t>> blocks;
  std::size_t total_bytes = 0;
  std::uint32_t k = 0;
  while (total_bytes < 80 * 1024) {  // > 2 chunks
    const std::size_t n = 64 + (k % 64);
    auto* p = a.allocate(n);
    REQUIRE(p != nullptr);
    for (std::size_t i = 0; i < n; ++i) {
      p[i] = k * 1000003u + static_cast<std::uint32_t>(i);
    }
    blocks.emplace_back(p, n);
    total_bytes += n * sizeof(std::uint32_t);
    ++k;
  }
  // All witnesses still match — no chunk refill clobbered prior data.
  for (std::uint32_t b = 0; b < blocks.size(); ++b) {
    auto [p, n] = blocks[b];
    for (std::size_t i = 0; i < n; ++i) {
      REQUIRE(p[i] == b * 1000003u + static_cast<std::uint32_t>(i));
    }
  }
}
