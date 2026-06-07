#include "pine/shared_mutex.hpp"

#include <doctest/doctest.h>

#include <atomic>
#include <chrono>
#include <mutex>
#include <shared_mutex>
#include <thread>
#include <vector>

using namespace pine;
using namespace std::chrono_literals;

// Single-thread sanity: lock_shared and unlock_shared compose;
// lock and unlock compose; mixing without ownership transitions works.
TEST_CASE("SharedMutex: single-threaded acquire/release symmetry") {
  SharedMutex m;

  // shared lock cycle
  m.lock_shared();
  m.unlock_shared();
  m.lock_shared();
  m.lock_shared();
  m.unlock_shared();
  m.unlock_shared();

  // exclusive lock cycle
  m.lock();
  m.unlock();
  m.lock();
  m.unlock();

  // alternate
  m.lock_shared();
  m.unlock_shared();
  m.lock();
  m.unlock();
  m.lock_shared();
  m.unlock_shared();
}

TEST_CASE("SharedMutex: try_lock returns false while a reader holds it") {
  SharedMutex m;
  m.lock_shared();
  CHECK_FALSE(m.try_lock());
  m.unlock_shared();
  CHECK(m.try_lock());
  m.unlock();
}

TEST_CASE("SharedMutex: try_lock_shared returns false while writer holds it") {
  SharedMutex m;
  m.lock();
  CHECK_FALSE(m.try_lock_shared());
  m.unlock();
  CHECK(m.try_lock_shared());
  m.unlock_shared();
}

TEST_CASE("SharedMutex: try_lock returns true on uncontended mutex") {
  SharedMutex m;
  CHECK(m.try_lock());
  m.unlock();
}

TEST_CASE("SharedMutex: try_lock_shared returns true on uncontended mutex") {
  SharedMutex m;
  CHECK(m.try_lock_shared());
  m.unlock_shared();
}

// Many concurrent readers must all hold the lock at the same time —
// nothing in the protocol should serialise them.
TEST_CASE("SharedMutex: N concurrent readers see no exclusion") {
  SharedMutex m;
  constexpr int kThreads = 16;
  std::atomic<int> in_critical{0};
  std::atomic<int> max_concurrent{0};
  std::atomic<bool> start{false};

  std::vector<std::thread> ts;
  ts.reserve(kThreads);
  for (int i = 0; i < kThreads; ++i) {
    ts.emplace_back([&]() {
      while (!start.load(std::memory_order_acquire)) {
        std::this_thread::yield();
      }
      m.lock_shared();
      int c = in_critical.fetch_add(1, std::memory_order_acq_rel) + 1;
      // Track peak concurrency across all readers.
      int prev = max_concurrent.load(std::memory_order_relaxed);
      while (c > prev && !max_concurrent.compare_exchange_weak(prev, c)) {
        // retry
      }
      // Hold long enough that other readers definitely overlap.
      std::this_thread::sleep_for(2ms);
      in_critical.fetch_sub(1, std::memory_order_acq_rel);
      m.unlock_shared();
    });
  }
  start.store(true, std::memory_order_release);
  for (auto& t : ts) {
    t.join();
  }

  // We expect at least 2 readers concurrent; in practice this hits
  // kThreads on any non-trivial scheduler.
  CHECK(max_concurrent.load() >= 2);
}

// A writer must exclude all readers, and readers must exclude the writer.
TEST_CASE("SharedMutex: writer excludes readers and vice versa") {
  SharedMutex m;
  std::atomic<int> readers_in{0};
  std::atomic<int> writers_in{0};
  std::atomic<bool> violation{false};
  std::atomic<bool> stop{false};
  constexpr int kReaders = 8;

  std::vector<std::thread> ts;
  for (int i = 0; i < kReaders; ++i) {
    ts.emplace_back([&]() {
      while (!stop.load(std::memory_order_acquire)) {
        m.lock_shared();
        readers_in.fetch_add(1, std::memory_order_acq_rel);
        if (writers_in.load(std::memory_order_acquire) != 0) {
          violation.store(true, std::memory_order_release);
        }
        readers_in.fetch_sub(1, std::memory_order_acq_rel);
        m.unlock_shared();
      }
    });
  }

  std::thread writer([&]() {
    auto end_at = std::chrono::steady_clock::now() + 200ms;
    while (std::chrono::steady_clock::now() < end_at) {
      m.lock();
      writers_in.fetch_add(1, std::memory_order_acq_rel);
      if (readers_in.load(std::memory_order_acquire) != 0) {
        violation.store(true, std::memory_order_release);
      }
      // Hold briefly to widen the violation window if any.
      std::this_thread::sleep_for(50us);
      writers_in.fetch_sub(1, std::memory_order_acq_rel);
      m.unlock();
    }
    stop.store(true, std::memory_order_release);
  });

  writer.join();
  for (auto& t : ts) {
    t.join();
  }

  CHECK_FALSE(violation.load());
}

// Ensure the writer-pending flag actually blocks new readers — this is
// the writer-starvation protection. Without it, a steady reader stream
// could lock out writers indefinitely.
TEST_CASE("SharedMutex: pending writer blocks new readers") {
  SharedMutex m;
  m.lock_shared();  // first reader holds the lock

  std::atomic<bool> writer_blocked_during_first_reader{true};
  std::atomic<bool> writer_acquired{false};

  std::thread w([&]() {
    m.lock();
    writer_acquired.store(true, std::memory_order_release);
    m.unlock();
  });

  // Give the writer a moment to set writer_pending and start waiting.
  std::this_thread::sleep_for(10ms);

  // Try a second reader: should NOT acquire while writer is pending.
  // try_lock_shared is the right primitive here; the rule is that
  // an attempt to take a fresh shared lock while a writer is pending
  // must back off rather than slip in ahead.
  CHECK_FALSE(m.try_lock_shared());

  // The writer hasn't acquired yet either (we still hold the first
  // shared lock), so the writer is parked between writer_pending=1
  // and writer_holding=1.
  CHECK_FALSE(writer_acquired.load());
  if (writer_acquired.load()) {
    writer_blocked_during_first_reader.store(false, std::memory_order_release);
  }

  // Release first reader → writer can now acquire.
  m.unlock_shared();
  w.join();
  CHECK(writer_acquired.load());
  CHECK(writer_blocked_during_first_reader.load());
}

// Check std::shared_lock and std::unique_lock work as RAII holders —
// since pine's frame code uses these RAII wrappers, this is the actual
// integration surface.
TEST_CASE("SharedMutex: composes with std::shared_lock and std::unique_lock") {
  SharedMutex m;
  {
    std::shared_lock<SharedMutex> lk(m);
    CHECK(lk.owns_lock());
  }
  {
    std::unique_lock<SharedMutex> lk(m);
    CHECK(lk.owns_lock());
  }
  // Nested through scopes.
  {
    std::shared_lock<SharedMutex> lk1(m);
    std::shared_lock<SharedMutex> lk2(m);
    CHECK(lk1.owns_lock());
    CHECK(lk2.owns_lock());
  }
}

// Stress: many readers + occasional writers, monitor reader_count
// integrity. If we under-count or double-count anywhere, the writer
// path's stage-2 spin will hang or violate exclusion.
TEST_CASE("SharedMutex: stress mixed readers and writers") {
  SharedMutex m;
  std::atomic<int64_t> shared_counter{0};
  std::atomic<bool> stop{false};
  constexpr int kReaders = 16;
  constexpr int kWriters = 4;

  std::vector<std::thread> ts;
  for (int i = 0; i < kReaders; ++i) {
    ts.emplace_back([&]() {
      while (!stop.load(std::memory_order_acquire)) {
        std::shared_lock<SharedMutex> lk(m);
        // Just observe the counter; do not write under shared lock.
        volatile int64_t v = shared_counter.load(std::memory_order_relaxed);
        (void)v;
      }
    });
  }
  for (int i = 0; i < kWriters; ++i) {
    ts.emplace_back([&]() {
      auto end_at = std::chrono::steady_clock::now() + 200ms;
      while (std::chrono::steady_clock::now() < end_at) {
        std::unique_lock<SharedMutex> lk(m);
        shared_counter.fetch_add(1, std::memory_order_relaxed);
      }
    });
  }
  // Run for the writer duration, then stop the readers.
  std::this_thread::sleep_for(220ms);
  stop.store(true, std::memory_order_release);
  for (auto& t : ts) {
    t.join();
  }

  // We don't care about the exact value, only that no thread hung
  // (the test reaching this point means everyone exited cleanly).
  CHECK(shared_counter.load() > 0);
}
