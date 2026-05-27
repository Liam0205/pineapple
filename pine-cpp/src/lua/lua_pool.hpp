#pragma once

#include "pine/metrics.hpp"
#include "pine/pine.hpp"

#include <atomic>
#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <vector>

#include "lua/lua_bridge.hpp"

namespace pine {
namespace lua {

class StatePool {
 public:
  explicit StatePool(std::string script, std::string op_name);
  ~StatePool();

  StatePool(const StatePool&) = delete;
  StatePool& operator=(const StatePool&) = delete;

  // Borrow returns a unique_ptr to a LuaVM that will be returned to the pool on destruction.
  // The VM is reset to its baseline state automatically.
  struct Releaser {
    StatePool* pool;
    std::map<std::string, int> snap;
    void operator()(LuaVM* vm) const;
  };
  using BorrowedVM = std::unique_ptr<LuaVM, Releaser>;

  BorrowedVM borrow();

  std::map<std::string, int64_t> stats_snapshot() const;
  void set_metrics(metrics::Provider* provider, const std::string& op_name);

 private:
  LuaVM* acquire_vm(std::map<std::string, int>& out_snap);
  void release_vm(LuaVM* vm, const std::map<std::string, int>& snap);

  std::string script_;
  std::string op_name_;
  LuaSnapshot baseline_;

  std::mutex mu_;
  std::vector<LuaVM*> free_vms_;
  std::vector<std::unique_ptr<LuaVM>> all_vms_;
  bool closed_ = false;

  // Fast atomics
  std::atomic<int64_t> borrow_count_{0};
  std::atomic<int64_t> return_count_{0};
  std::atomic<int64_t> create_count_{0};
  std::atomic<int64_t> active_count_{0};

  // External metrics
  metrics::Counter* m_borrow_ = nullptr;
  metrics::Counter* m_return_ = nullptr;
  metrics::Counter* m_create_ = nullptr;
  metrics::Gauge* m_active_ = nullptr;
};

}  // namespace lua
}  // namespace pine
