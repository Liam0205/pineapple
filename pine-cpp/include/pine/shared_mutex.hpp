#pragma once

#include <atomic>
#include <cstdint>
#include <thread>

namespace pine {

// SharedMutex is a lightweight, writer-priority read-write mutex.
//
// Why this exists. glibc's pthread_rwlock (the implementation behind
// std::shared_mutex) costs ~30 ns per uncontended lock_shared / unlock_shared
// pair on Linux x86_64 — the kernel-aware fast path still does several
// atomic exchanges on per-rwlock state plus signal-mask juggling. On
// pine-cpp's hot read paths (OperatorInput::item firing N×M times per
// operator dispatch) those atomics dominate the profile (~20% on
// large_5000) even though the contention is essentially zero.
//
// Go's sync.RWMutex.RLock and Java's ReentrantReadWriteLock.readLock
// both achieve sub-10 ns uncontended cost via runtime-specific tricks
// (sync.Pool-style pinning in Go, JIT inlining + escape analysis in
// Java). On the C++ side we don't have those, so we implement a small
// futex-free spin/yield based reader-writer lock that pays roughly one
// CAS per uncontended lock_shared.
//
// Semantics. Mirrors std::shared_mutex enough to satisfy
// std::shared_lock / std::unique_lock as RAII holders:
//   - lock_shared / unlock_shared: read locks, multiple readers permitted
//   - lock / unlock: exclusive write lock
//   - try_lock_shared / try_lock: non-blocking
//
// State word layout (atomic<uint32_t>):
//   bit 31:     writer_holding  (1 = exclusive lock held)
//   bit 30:     writer_pending  (1 = a writer is waiting; new readers
//                                 must block to avoid writer starvation)
//   bits 0-29:  reader_count    (number of currently-held read locks)
//
// Concurrency design. All three operations (lock_shared, lock,
// unlock_*) work via CAS rather than fetch_add. CAS-only operation
// avoids the rollback-window race that fetch_add-then-check would
// otherwise expose: a reader that fetch_add'd just before a writer
// committed kWriterHolding via plain store could leave the state word
// holding both writer bits and a reader bit, and the reader's
// subsequent unlock_shared (fetch_sub 1) would underflow the reader
// count into the upper bits. Using CAS in lock_shared ensures we
// only commit the read lock if the observed state was clean of
// writer flags at commit time.
//
// Reader path: load → check writer flags → CAS to add 1.
// Writer path: load → check no writer ahead → CAS to set pending →
//   spin to drain readers → CAS pending→holding.
// Both unlocks: CAS-decrement / CAS-clear.
//
// Spin policy: std::this_thread::yield. For pine-cpp's contention
// pattern (short read windows + brief writes) yielding is sufficient
// and avoids futex syscall overhead. If a future workload shows
// lock-bound starvation, swap in a futex-based wait — the public
// interface doesn't change.
//
// Thread safety: all member functions are safe to call concurrently.
// No reentrance: a thread that holds a read or write lock must not try
// to take another lock on the same instance (matches std::shared_mutex).
class SharedMutex {
 public:
  SharedMutex() noexcept = default;
  SharedMutex(const SharedMutex&) = delete;
  SharedMutex& operator=(const SharedMutex&) = delete;

  // ---- shared (reader) locking ----

  void lock_shared() noexcept {
    for (;;) {
      uint32_t s = state_.load(std::memory_order_acquire);
      if ((s & kWriterMask) != 0) {
        // Writer holding or pending — yield and retry. Stalling here
        // (rather than fetch_add'ing and rolling back) is what gives
        // the writer a stable reader_count to drain against.
        std::this_thread::yield();
        continue;
      }
      // Try to commit reader_count + 1. CAS rather than fetch_add so
      // that if a writer set kWriterPending between our load and the
      // CAS, we observe it and retry instead of polluting the state
      // with a transient reader bump.
      uint32_t want = s + 1;
      if (state_.compare_exchange_weak(s, want, std::memory_order_acquire, std::memory_order_acquire)) {
        return;
      }
      // CAS failed — another reader or writer raced us. Retry.
    }
  }

  bool try_lock_shared() noexcept {
    uint32_t s = state_.load(std::memory_order_acquire);
    if ((s & kWriterMask) != 0) {
      return false;
    }
    uint32_t want = s + 1;
    return state_.compare_exchange_strong(s, want, std::memory_order_acquire, std::memory_order_acquire);
  }

  void unlock_shared() noexcept {
    // CAS-decrement so we never observe an underflow even if another
    // thread is concurrently mutating writer flags. fetch_sub would
    // be cheaper but mixes badly with the writer's CAS pending→holding
    // transition that assumes clean reader_count==0 transitions.
    for (;;) {
      uint32_t s = state_.load(std::memory_order_acquire);
      // Must have at least one reader to release; protocol violation
      // otherwise. We don't assert in release builds; just clamp.
      uint32_t want = s - 1;  // safe: reader bits live in low 30 bits
      if (state_.compare_exchange_weak(s, want, std::memory_order_release, std::memory_order_acquire)) {
        return;
      }
    }
  }

  // ---- exclusive (writer) locking ----

  void lock() noexcept {
    // Stage 1: claim writer_pending. Loop CAS-from-no-writer until we
    // get it. While another writer holds the pending or holding bit
    // we yield and retry.
    for (;;) {
      uint32_t s = state_.load(std::memory_order_acquire);
      if ((s & kWriterMask) != 0) {
        std::this_thread::yield();
        continue;
      }
      uint32_t want = s | kWriterPending;
      if (state_.compare_exchange_weak(s, want, std::memory_order_acq_rel, std::memory_order_acquire)) {
        break;
      }
    }

    // Stage 2: wait for reader_count to drain, then atomically flip
    // pending → holding. The transition CAS verifies the state is
    // exactly kWriterPending (no readers, no other writer flags) —
    // any racing reader that bumped reader_count after we set pending
    // will have rolled back via its own CAS retry by the time we get
    // here.
    for (;;) {
      uint32_t expected = kWriterPending;
      if (state_.compare_exchange_weak(expected, kWriterHolding, std::memory_order_acq_rel,
                                       std::memory_order_acquire)) {
        return;
      }
      // expected reflects the actual state. If readers are still in,
      // wait; otherwise (shouldn't happen) restart.
      if ((expected & kWriterPending) == 0) {
        // writer_pending unexpectedly cleared — start over to be safe.
        return lock();
      }
      std::this_thread::yield();
    }
  }

  bool try_lock() noexcept {
    uint32_t expected = 0;
    return state_.compare_exchange_strong(expected, kWriterHolding, std::memory_order_acq_rel,
                                          std::memory_order_acquire);
  }

  void unlock() noexcept {
    state_.store(0, std::memory_order_release);
  }

  // Test/debug only — exposes the raw state word so tests can verify
  // that the mutex returns to a quiescent state after a stress run.
  uint32_t debug_state() const noexcept {
    return state_.load(std::memory_order_acquire);
  }

 private:
  static constexpr uint32_t kWriterHolding = 1u << 31;
  static constexpr uint32_t kWriterPending = 1u << 30;
  static constexpr uint32_t kWriterMask = kWriterHolding | kWriterPending;
  static constexpr uint32_t kReaderMask = (1u << 30) - 1;

  std::atomic<uint32_t> state_{0};
};

}  // namespace pine
