#pragma once

#include <atomic>
#include <cassert>
#include <cstdint>
#include <mutex>
#include <semaphore>

namespace pine {

// SharedMutex is a reader-writer mutex that ports Go's sync.RWMutex
// algorithm to C++ (go/src/sync/rwmutex.go, Go 1.26). It exists because
// the uncontended fast path of glibc's pthread_rwlock (the engine behind
// std::shared_mutex on Linux) costs ~25 instructions per acquisition —
// full function prologue/epilogue (PLT-called, never inlined), a TLS
// load for self-deadlock detection, a reader/writer-preference policy
// branch, and only then the LOCK XADD that does the real work. Go's
// RLock fast path inlines to LOCK XADD + sign test: ~3 instructions.
//
// Why the first pine::SharedMutex (CAS-based) failed: it used
// load + compare_exchange loops for both lock_shared and unlock_shared.
// That costs two atomic cacheline accesses per op instead of one, and
// under concurrent readers the CAS fails and retries (every successful
// +1 invalidates every other reader's expected value) — measured 2029ns
// per pair at 16 readers vs 1294ns for pthread_rwlock. Its writer-pending
// path also made readers spin on std::this_thread::yield(), burning CPU
// that the 2-core cgroup'd server needed for actual work.
//
// The Go algorithm fixes all three problems at once:
//
//   reader_count_  int32   — readers currently holding the lock. A writer
//                            "announces" itself by subtracting kMaxReaders,
//                            driving the count deeply negative. Readers
//                            fetch_add(1) unconditionally — never retry,
//                            never roll back — and the *sign* of the result
//                            tells them whether a writer is in the way.
//   reader_wait_   int32   — how many pre-announcement readers the writer
//                            must wait out. The last of them posts
//                            writer_sem_.
//   writer_mu_             — serialises writers against each other.
//   reader_sem_ / writer_sem_ — counting semaphores for blocking (futex
//                            underneath); nobody ever spins.
//
// Fast paths (the only paths production traffic hits — pine frames are
// request-local, so writer contention is rare):
//   lock_shared:   one LOCK XADD + a not-taken branch
//   unlock_shared: one LOCK XADD + a not-taken branch
//
// Semantics match std::shared_mutex closely enough for std::shared_lock
// and std::unique_lock: lock/unlock, lock_shared/unlock_shared,
// try_lock, try_lock_shared. Not recursive. Writer-preferring: once a
// writer announces, new readers queue behind it (no writer starvation).
//
// Unlock-of-unlocked is UB (asserted in debug builds), same stance as
// std::shared_mutex — this is an engine-internal lock, not a public API.
class SharedMutex {
 public:
  SharedMutex() noexcept = default;
  SharedMutex(const SharedMutex&) = delete;
  SharedMutex& operator=(const SharedMutex&) = delete;

  // ---- shared (reader) locking ----

  void lock_shared() noexcept {
    if (reader_count_.fetch_add(1, std::memory_order_acquire) < 0) {
      // A writer is pending or active. Our +1 is already counted — the
      // writer's unlock() will see us in the queued total and post
      // reader_sem_ exactly once for us. Block; no spinning.
      reader_sem_.acquire();
    }
  }

  bool try_lock_shared() noexcept {
    // Cannot blind fetch_add here: bumping reader_count_ while negative
    // would enrol us in the writer's queue accounting and we would have
    // to block to keep it balanced. CAS only when no writer is around.
    int32_t s = reader_count_.load(std::memory_order_relaxed);
    while (s >= 0) {
      if (reader_count_.compare_exchange_weak(s, s + 1, std::memory_order_acquire,
                                              std::memory_order_relaxed)) {
        return true;
      }
    }
    return false;
  }

  void unlock_shared() noexcept {
    int32_t r = reader_count_.fetch_sub(1, std::memory_order_release);
    if (r < 0) {
      // Mirrors Go's rUnlockSlow fatal checks: r == 0 means unlock of an
      // unlocked mutex; r == -kMaxReaders means unlock_shared while only
      // a writer holds it.
      assert(r != 0 && r != -kMaxReaders && "unlock_shared of unlocked SharedMutex");
      // A writer is waiting for the pre-announcement readers to drain.
      // The last one out posts the writer's semaphore.
      if (reader_wait_.fetch_sub(1, std::memory_order_acq_rel) == 1) {
        writer_sem_.release();
      }
    }
  }

  // ---- exclusive (writer) locking ----

  void lock() noexcept {
    // Resolve competition with other writers first.
    writer_mu_.lock();
    // Announce to readers that a writer is pending: drive reader_count_
    // negative. fetch_sub returns the previous value = the number of
    // readers holding the lock at announcement time.
    int32_t r = reader_count_.fetch_sub(kMaxReaders, std::memory_order_acq_rel);
    assert(r < kMaxReaders && "lock() while already write-locked");
    // Wait for those readers to drain. They may have *already* drained
    // between our fetch_sub and here — each of them decremented
    // reader_wait_ below zero, and our fetch_add(r) brings it back to
    // exactly zero in that case, meaning: nothing to wait for.
    if (r != 0 && reader_wait_.fetch_add(r, std::memory_order_acq_rel) + r != 0) {
      writer_sem_.acquire();
    }
  }

  bool try_lock() noexcept {
    if (!writer_mu_.try_lock()) {
      return false;
    }
    int32_t expected = 0;
    if (!reader_count_.compare_exchange_strong(expected, -kMaxReaders, std::memory_order_acq_rel,
                                               std::memory_order_relaxed)) {
      writer_mu_.unlock();
      return false;
    }
    return true;
  }

  void unlock() noexcept {
    // Un-announce: bring reader_count_ back to non-negative. The new
    // value (old + kMaxReaders) is the number of readers that arrived
    // while we held the lock — they are all blocked on reader_sem_.
    int32_t r = reader_count_.fetch_add(kMaxReaders, std::memory_order_release) + kMaxReaders;
    assert(r < kMaxReaders && "unlock of unlocked SharedMutex");
    if (r > 0) {
      reader_sem_.release(r);
    }
    // Let the next writer in.
    writer_mu_.unlock();
  }

  // Test/debug only.
  int32_t debug_reader_count() const noexcept {
    return reader_count_.load(std::memory_order_acquire);
  }

 private:
  static constexpr int32_t kMaxReaders = 1 << 30;

  std::atomic<int32_t> reader_count_{0};
  std::atomic<int32_t> reader_wait_{0};
  std::mutex writer_mu_;
  std::counting_semaphore<> reader_sem_{0};
  std::counting_semaphore<1> writer_sem_{0};
};

}  // namespace pine
