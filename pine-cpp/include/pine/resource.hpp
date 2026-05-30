#pragma once

// Dynamic in-memory resource management with background refresh.
//
// Mirrors pine-go/pkg/resource. The Manager exposes a thread-safe snapshot
// of named resources; downstream code passes that snapshot into
// `Engine::execute(request, resources)`. Business code registers
// FetcherFactory implementations via `register_fetcher_factory` (analog of
// pine-go's resource.Register).

#include "pine/pine.hpp"

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <functional>
#include <map>
#include <memory>
#include <mutex>
#include <shared_mutex>
#include <string>
#include <thread>
#include <vector>

namespace pine {
namespace resource {

// Fetcher loads a resource value. Called by the background refresh loop.
using Fetcher = std::function<Variant()>;

// FetcherFactory creates a Fetcher from config params. Business code
// registers factories at static init time, keyed by ResourceEntry.type.
using FetcherFactory = std::function<Fetcher(const Variant& params)>;

// Register a fetcher factory under a type name. Throws if name is duplicated.
// Returns true for static-init use:
//   static bool _ = register_fetcher_factory("my_type", &my_factory);
bool register_fetcher_factory(const std::string& type_name, FetcherFactory factory);

// Look up a factory by type name. Returns nullptr if absent.
const FetcherFactory* lookup_fetcher_factory(const std::string& type_name);

// All registered type names, sorted.
std::vector<std::string> registered_fetcher_types();

// For tests only.
void reset_fetcher_registry();

// Manager owns a set of named resources with background refresh.
class Manager {
 public:
  Manager();
  ~Manager();
  Manager(const Manager&) = delete;
  Manager& operator=(const Manager&) = delete;

  // Register a fetcher under a name with a refresh interval (must be > 0).
  // Throws on duplicate name or if start() already ran.
  void register_resource(const std::string& name, Fetcher fetcher, std::chrono::seconds interval);

  // Load resources from a parsed Config. Each `resource_config` entry is
  // resolved against the global FetcherFactory registry. Returns silently
  // when no resources are configured.
  void load_from_config(const Config& config);

  // Synchronous initial load for all registered resources, then launches
  // background refresh threads. Throws on initial-load failure.
  void start();

  // Cancel refresh threads and wait for them to exit. Safe to call from any
  // state; safe to call multiple times.
  void stop();

  // Returns a snapshot of all currently-loaded resources. Pass to
  // `Engine::execute(request, snapshot)`.
  std::map<std::string, Variant> snapshot() const;

  // Returns the registered resource names, sorted.
  std::vector<std::string> names() const;

  // ValidateResourceDeps checks that every resource_name referenced in the
  // Config's operators is registered in the Manager. Throws on missing.
  void validate_resource_deps(const Config& config) const;

 private:
  struct Managed {
    std::string name;
    Fetcher fetcher;
    std::chrono::seconds interval{0};
    Variant value;
    bool loaded = false;
  };

  void refresh_loop(Managed* r);

  mutable std::shared_mutex mu_;
  std::map<std::string, std::unique_ptr<Managed>> resources_;
  std::vector<std::thread> refresh_threads_;
  std::mutex stop_mu_;
  std::condition_variable stop_cv_;
  std::atomic<bool> stopping_{false};
  bool started_ = false;
};

}  // namespace resource
}  // namespace pine
