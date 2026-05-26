#include "lua_pool.hpp"

namespace pine {
namespace lua {

StatePool::StatePool(std::string script, std::string op_name)
    : script_(std::move(script)), op_name_(std::move(op_name)) {

    // Create the first state to validate the script and capture baseline
    auto first = std::make_unique<LuaVM>();
    first->load_script(script_, op_name_);
    baseline_ = LuaSnapshot(first->state());
    create_count_.fetch_add(1, std::memory_order_relaxed);

    free_vms_.push_back(first.get());
    all_vms_.push_back(std::move(first));
}

StatePool::~StatePool() {
    std::lock_guard<std::mutex> lock(mu_);
    closed_ = true;
    // all_vms_ destruction cleans up LuaVMs
}

void StatePool::Releaser::operator()(LuaVM* vm) const {
    if (pool && vm) {
        // Releaser is a unique_ptr deleter — invoked from ~unique_ptr,
        // which is noexcept. release_vm() can throw if reset_to_baseline
        // faults on a script-corrupted state; letting that exception cross
        // the noexcept boundary triggers std::terminate (inc-6 阻塞 B1).
        // Counters and metrics are still updated inside release_vm's catch
        // block before it rethrows, so swallowing here loses no observability.
        try {
            pool->release_vm(vm, snap);
        } catch (...) {
            // intentionally swallow: cannot propagate past unique_ptr dtor.
        }
    }
}

StatePool::BorrowedVM StatePool::borrow() {
    std::map<std::string, int> snap;
    LuaVM* vm = acquire_vm(snap);
    return BorrowedVM(vm, Releaser{this, std::move(snap)});
}

LuaVM* StatePool::acquire_vm(std::map<std::string, int>& out_snap) {
    borrow_count_.fetch_add(1, std::memory_order_relaxed);
    active_count_.fetch_add(1, std::memory_order_relaxed);
    if (m_borrow_) m_borrow_->inc();
    if (m_active_) m_active_->add(1);

    LuaVM* vm = nullptr;
    {
        std::lock_guard<std::mutex> lock(mu_);
        if (closed_) {
            borrow_count_.fetch_sub(1, std::memory_order_relaxed);
            active_count_.fetch_sub(1, std::memory_order_relaxed);
            throw ExecutionError(op_name_, "lua pool is closed");
        }
        if (!free_vms_.empty()) {
            vm = free_vms_.back();
            free_vms_.pop_back();
        } else {
            auto fresh = std::make_unique<LuaVM>();
            fresh->load_script(script_, op_name_);
            vm = fresh.get();
            all_vms_.push_back(std::move(fresh));
            create_count_.fetch_add(1, std::memory_order_relaxed);
            if (m_create_) m_create_->inc();
        }
    }

    out_snap = baseline_.capture_values(vm->state());
    return vm;
}

void StatePool::release_vm(LuaVM* vm, const std::map<std::string, int>& snap) {
    bool closed = false;
    {
        std::lock_guard<std::mutex> lock(mu_);
        closed = closed_;
    }

    if (!closed) {
        baseline_.reset_to_baseline(vm->state(), snap);
        std::lock_guard<std::mutex> lock(mu_);
        if (!closed_) {
            free_vms_.push_back(vm);
        }
    }

    return_count_.fetch_add(1, std::memory_order_relaxed);
    active_count_.fetch_sub(1, std::memory_order_relaxed);
    if (m_return_) m_return_->inc();
    if (m_active_) m_active_->add(-1);
}

std::map<std::string, int64_t> StatePool::stats_snapshot() const {
    return {
        {"borrow_count", borrow_count_.load(std::memory_order_relaxed)},
        {"return_count", return_count_.load(std::memory_order_relaxed)},
        {"create_count", create_count_.load(std::memory_order_relaxed)},
        {"active_count", active_count_.load(std::memory_order_relaxed)},
    };
}

void StatePool::set_metrics(metrics::Provider* provider, const std::string& op_name) {
    if (!provider) return;
    std::vector<std::string> labels = {op_name};
    m_borrow_ = provider->new_counter({"pine_lua_pool_borrow_total", "Total Lua state borrows.", {"operator"}})->with(labels);
    m_return_ = provider->new_counter({"pine_lua_pool_return_total", "Total Lua state returns.", {"operator"}})->with(labels);
    m_create_ = provider->new_counter({"pine_lua_pool_create_total", "Total Lua states created.", {"operator"}})->with(labels);
    m_active_ = provider->new_gauge({"pine_lua_pool_active", "Lua states currently borrowed.", {"operator"}})->with(labels);
}

}  // namespace lua
}  // namespace pine
