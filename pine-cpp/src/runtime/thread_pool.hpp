#pragma once

#include <atomic>
#include <condition_variable>
#include <cstddef>
#include <functional>
#include <future>
#include <memory>
#include <mutex>
#include <queue>
#include <thread>
#include <vector>

namespace pine {
namespace runtime {

// Fixed-size worker pool with an unbounded FIFO task queue.
//
// Intended for tasks that do NOT internally wait on the completion of other
// tasks submitted to the same pool — such waits can deadlock when all workers
// are stuck on inner futures. Data-parallel shards (parallel_execute) and
// future request-thread reuse fit naturally; DAG node scheduling currently
// uses per-node std::thread to avoid this hazard.
class ThreadPool {
public:
    explicit ThreadPool(std::size_t worker_count);
    ~ThreadPool();

    ThreadPool(const ThreadPool&) = delete;
    ThreadPool& operator=(const ThreadPool&) = delete;

    // Submit a task that will run on one of the pool workers. Returns a
    // future the caller can wait on.
    std::future<void> submit(std::function<void()> task);

    std::size_t worker_count() const { return worker_count_; }

private:
    void worker_loop();

    std::size_t worker_count_;
    std::vector<std::thread> workers_;
    std::queue<std::packaged_task<void()>> tasks_;
    std::mutex mu_;
    std::condition_variable cv_;
    bool stopping_ = false;
};

}  // namespace runtime
}  // namespace pine
