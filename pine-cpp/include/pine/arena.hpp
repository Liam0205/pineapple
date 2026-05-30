#pragma once

#include <atomic>
#include <cstddef>
#include <cstdlib>
#include <memory_resource>
#include <mutex>
#include <new>
#include <type_traits>

namespace pine {

namespace detail {

// Central arena: thread-safe monotonic allocator that hands out large chunks.
// Individual threads grab chunks from here and sub-allocate locally (lock-free).
class CentralArena {
 public:
  explicit CentralArena(std::size_t initial_size) : mono_(initial_size, std::pmr::new_delete_resource()) {
  }

  void* allocate(std::size_t bytes, std::size_t alignment) {
    std::lock_guard<std::mutex> lk(mu_);
    return mono_.allocate(bytes, alignment);
  }

 private:
  std::pmr::monotonic_buffer_resource mono_;
  std::mutex mu_;
};

// Per-thread bump allocator that grabs chunks from a CentralArena.
// Allocation is lock-free (just a pointer bump). When the local buffer
// is exhausted, a new chunk is fetched from the central arena (locked).
// Deallocation is a no-op (arena pattern).
class ThreadLocalBump final : public std::pmr::memory_resource {
 public:
  explicit ThreadLocalBump(CentralArena* central) : central_(central) {
  }

 protected:
  void* do_allocate(std::size_t bytes, std::size_t alignment) override {
    // Align cursor up
    std::size_t space = end_ - cursor_;
    void* ptr = cursor_;
    if (std::align(alignment, bytes, ptr, space)) {
      cursor_ = static_cast<char*>(ptr) + bytes;
      return ptr;
    }
    // Need a new chunk. Request at least kChunkSize or the allocation size.
    std::size_t chunk_size = bytes + alignment > kChunkSize ? bytes + alignment : kChunkSize;
    cursor_ = static_cast<char*>(central_->allocate(chunk_size, alignof(std::max_align_t)));
    end_ = cursor_ + chunk_size;
    // Retry alignment in new chunk
    space = end_ - cursor_;
    ptr = cursor_;
    if (!std::align(alignment, bytes, ptr, space)) {
      // Should never happen — chunk is large enough
      std::abort();
    }
    cursor_ = static_cast<char*>(ptr) + bytes;
    return ptr;
  }

  void do_deallocate(void*, std::size_t, std::size_t) noexcept override {
  }

  bool do_is_equal(const std::pmr::memory_resource& other) const noexcept override {
    return this == &other;
  }

 private:
  static constexpr std::size_t kChunkSize = 32 * 1024;  // 32 KB per chunk
  CentralArena* central_;
  char* cursor_ = nullptr;
  char* end_ = nullptr;
};

// Thread-local resource pointer. Defaults to new_delete_resource.
inline thread_local std::pmr::memory_resource* tl_resource = std::pmr::new_delete_resource();
// Thread-local pointer to the current request's CentralArena (null if no arena active).
inline thread_local CentralArena* tl_central = nullptr;

}  // namespace detail

inline std::pmr::memory_resource* current_resource() noexcept {
  return detail::tl_resource;
}

inline detail::CentralArena* current_central_arena() noexcept {
  return detail::tl_central;
}

// ArenaAllocator is a stateless allocator that routes all allocations through
// the thread-local memory_resource. Because it's stateless (no stored pointer),
// all instances compare equal — this means STL containers can move-assign
// between each other without element-wise copy (just pointer swap).
template <typename T>
class ArenaAllocator {
 public:
  using value_type = T;
  using propagate_on_container_move_assignment = std::true_type;
  using is_always_equal = std::true_type;

  ArenaAllocator() noexcept = default;
  template <typename U>
  ArenaAllocator(const ArenaAllocator<U>&) noexcept {
  }

  T* allocate(std::size_t n) {
    return static_cast<T*>(detail::tl_resource->allocate(n * sizeof(T), alignof(T)));
  }

  void deallocate(T* p, std::size_t n) noexcept {
    detail::tl_resource->deallocate(p, n * sizeof(T), alignof(T));
  }

  template <typename U>
  bool operator==(const ArenaAllocator<U>&) const noexcept {
    return true;
  }
};

// ScopedInstall is an RAII guard for worker threads to temporarily install
// a per-thread bump allocator backed by the request's central arena.
// Each worker gets its own ThreadLocalBump (lock-free allocation).
class ScopedInstall {
 public:
  explicit ScopedInstall(detail::CentralArena* central) noexcept
      : bump_(central), prev_(detail::tl_resource) {
    detail::tl_resource = &bump_;
  }
  ~ScopedInstall() {
    detail::tl_resource = prev_;
  }
  ScopedInstall(const ScopedInstall&) = delete;
  ScopedInstall& operator=(const ScopedInstall&) = delete;

 private:
  detail::ThreadLocalBump bump_;
  std::pmr::memory_resource* prev_;
};

// RequestArena is an RAII guard that installs a per-request arena allocator.
// The calling thread gets a ThreadLocalBump backed by the central arena.
// Worker threads use ScopedInstall to get their own ThreadLocalBump.
//
// All allocation is lock-free (pointer bump) except when a thread exhausts
// its local chunk and needs a new one from the central arena (rare, amortized).
//
// Usage:
//   {
//     RequestArena arena;
//     auto result = engine.execute(request, resources);
//     std::string json = serialize(result);
//   }  // arena destroyed — all intermediate allocations freed at once
class RequestArena {
 public:
  explicit RequestArena(std::size_t initial_size = 256 * 1024)
      : central_(initial_size),
        bump_(&central_),
        prev_(detail::tl_resource),
        prev_central_(detail::tl_central) {
    detail::tl_resource = &bump_;
    detail::tl_central = &central_;
  }

  ~RequestArena() {
    detail::tl_resource = prev_;
    detail::tl_central = prev_central_;
  }

  RequestArena(const RequestArena&) = delete;
  RequestArena& operator=(const RequestArena&) = delete;

  detail::CentralArena* central() noexcept {
    return &central_;
  }

 private:
  detail::CentralArena central_;
  detail::ThreadLocalBump bump_;
  std::pmr::memory_resource* prev_;
  detail::CentralArena* prev_central_;
};

}  // namespace pine
