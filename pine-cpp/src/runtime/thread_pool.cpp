#include "runtime/thread_pool.hpp"

namespace pine {
namespace runtime {

ThreadPool::ThreadPool(std::size_t worker_count) : worker_count_(worker_count) {
    if (worker_count_ == 0) worker_count_ = 1;
    workers_.reserve(worker_count_);
    for (std::size_t i = 0; i < worker_count_; ++i) {
        workers_.emplace_back([this] { this->worker_loop(); });
    }
}

ThreadPool::~ThreadPool() {
    {
        std::lock_guard<std::mutex> lk(mu_);
        stopping_ = true;
    }
    cv_.notify_all();
    for (auto& t : workers_) {
        if (t.joinable()) t.join();
    }
}

std::future<void> ThreadPool::submit(std::function<void()> task) {
    std::packaged_task<void()> pt(std::move(task));
    std::future<void> fut = pt.get_future();
    {
        std::lock_guard<std::mutex> lk(mu_);
        tasks_.push(std::move(pt));
    }
    cv_.notify_one();
    return fut;
}

void ThreadPool::worker_loop() {
    for (;;) {
        std::packaged_task<void()> task;
        {
            std::unique_lock<std::mutex> lk(mu_);
            cv_.wait(lk, [this] { return stopping_ || !tasks_.empty(); });
            if (stopping_ && tasks_.empty()) return;
            task = std::move(tasks_.front());
            tasks_.pop();
        }
        task();
    }
}

}  // namespace runtime
}  // namespace pine
