#pragma once

// Dynamic in-memory resource management with background refresh.
//
// Mirrors pine-go/pkg/resource. The Manager exposes a thread-safe snapshot
// of named resources; downstream code passes that snapshot into
// `Engine::execute(request, resources)`. Business code registers
// FetcherFactory implementations via `register_fetcher_factory` (analog of
// pine-go's resource.Register).

#include "pine/metrics.hpp"
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

// ResourceValue is what a Fetcher produces and what the Manager stores per
// resource. It carries EITHER a plain data Variant (the common case: a value
// refreshed on an interval and exported via snapshot() into the per-execute
// resources map) OR a process-internal handle (e.g. a Redis connection pool)
// type-erased as shared_ptr<void>. The two are mutually exclusive.
//
// Handles are deliberately kept out of Variant: Variant participates in
// dump_json / parse_json and must stay a pure JSON value, whereas a handle is
// a live object that can't be serialized. Operators borrow handles by name via
// Manager::borrow (the ResourceProvider interface) and static_pointer_cast the
// returned shared_ptr<void> back to the concrete type the fetcher stored.
//
// This mirrors pine-go's resource value model (interface{} holding either a
// JSON value or a live handle) and pine-java's Object resource values, giving
// C++ full cross-runtime parity rather than a data-only subset.
class ResourceValue {
 public:
  ResourceValue() : data_(Variant()), is_handle_(false) {
  }

  static ResourceValue data(Variant v) {
    ResourceValue rv;
    rv.data_ = std::move(v);
    rv.is_handle_ = false;
    return rv;
  }

  static ResourceValue handle(std::shared_ptr<void> h) {
    ResourceValue rv;
    rv.handle_ = std::move(h);
    rv.is_handle_ = true;
    return rv;
  }

  bool is_handle() const {
    return is_handle_;
  }
  bool is_data() const {
    return !is_handle_;
  }

  const Variant& as_data() const {
    return data_;
  }

  // Cast the type-erased handle to its concrete type. Returns nullptr when this
  // value is data-typed (caller should degrade). The cast is by convention:
  // the fetcher that produced the handle and the operator that borrows it must
  // agree on T.
  template <typename T>
  std::shared_ptr<T> handle_as() const {
    if (!is_handle_) {
      return nullptr;
    }
    return std::static_pointer_cast<T>(handle_);
  }

  // Raw type-erased handle (nullptr when data-typed). Used by Manager::borrow.
  std::shared_ptr<void> raw_handle() const {
    return is_handle_ ? handle_ : nullptr;
  }

 private:
  Variant data_;
  std::shared_ptr<void> handle_;
  bool is_handle_ = false;
};

// Fetcher loads a resource value. Called by the background refresh loop (for
// data resources) or once at start() (for handle / never-refresh resources).
using Fetcher = std::function<ResourceValue()>;

// FetcherFactory creates a Fetcher from config params. It also receives the
// active metrics::Provider, so long-lived resources (e.g. connection pools) can
// create their own metrics instead of relying on global collectors. The
// provider is never null — callers with no provider receive metrics::nop_provider().
// Business code registers factories at static init time, keyed by ResourceEntry.type.
using FetcherFactory = std::function<Fetcher(const Variant& params, metrics::Provider* mp)>;

// Register a fetcher factory under a type name. Throws if name is duplicated.
// Returns true for static-init use:
//   static bool _ = register_fetcher_factory("my_type", &my_factory);
bool register_fetcher_factory(const std::string& type_name, FetcherFactory factory);

// ResourceSchema describes a resource type for registration, codegen, and
// documentation generation. Symmetric with OperatorSchema and mirrors Go's
// types.ResourceSchema / Java's Codegen.ResourceSchema so the three runtimes
// can emit byte-identical apple_generated/resources.py.
struct ResourceSchema {
  std::string name;                           // Resource type name (e.g. "redis_connection").
  std::string description;                    // One-line summary.
  int default_interval = 0;                   // Default refresh seconds; 0→10min; <0→never refresh.
  std::map<std::string, ParamSchema> params;  // Reuses operator ParamSchema.
};

// Register a ResourceSchema. Independent of register_fetcher_factory so the
// factory side (runtime behaviour) and the schema side (cross-runtime codegen
// contract) can be wired separately. Throws on duplicate name.
bool register_resource_schema(ResourceSchema schema);

// All registered ResourceSchemas, sorted by name. Used by the codegen tool.
std::vector<ResourceSchema> all_resource_schemas();

// For tests only: clear the ResourceSchema registry. The fetcher factory
// registry is left untouched — call reset_fetcher_registry() separately
// if both sides need clearing.
void reset_resource_schema_registry();

// Look up a factory by type name. Returns nullptr if absent.
const FetcherFactory* lookup_fetcher_factory(const std::string& type_name);

// All registered type names, sorted.
std::vector<std::string> registered_fetcher_types();

// For tests only.
void reset_fetcher_registry();

// For tests only: clear both the fetcher factory and ResourceSchema
// registries in one call. Equivalent to calling reset_fetcher_registry()
// and reset_resource_schema_registry() under the same registry lock.
void reset_all_resource_registries();

// Manager owns a set of named resources with background refresh. It implements
// ResourceProvider so it can be passed directly into EngineOptions and borrowed
// from by ResourceAware operators.
class Manager : public ResourceProvider {
 public:
  // Construct a Manager whose resource factories receive the given
  // metrics::Provider, so long-lived resources can emit their own metrics. A
  // null provider is replaced with metrics::nop_provider().
  explicit Manager(metrics::Provider* mp = nullptr);
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

  // Returns a snapshot of all currently-loaded DATA resources. Handle-typed
  // resources (connection pools etc.) are excluded — they are not JSON values
  // and are reached via borrow() instead. Pass the result to
  // `Engine::execute(request, snapshot)`.
  std::map<std::string, Variant> snapshot() const;

  // Returns the registered resource names, sorted.
  std::vector<std::string> names() const;

  // ResourceProvider: borrow a handle-typed resource by name. Returns the
  // type-erased handle, or nullptr when the resource is absent, not yet
  // loaded, or data-typed (in which case the caller degrades). The handle is
  // process-internal and must only be used within a single execute() call.
  std::shared_ptr<void> borrow(const std::string& name) const override;

  // ValidateResourceDeps checks that every resource_name referenced in the
  // Config's operators is registered in the Manager. Throws on missing.
  void validate_resource_deps(const Config& config) const;

 private:
  struct Managed {
    std::string name;
    Fetcher fetcher;
    std::chrono::seconds interval{0};
    ResourceValue value;
    bool loaded = false;
  };

  void refresh_loop(Managed* r);

  mutable std::shared_mutex mu_;
  metrics::Provider* metrics_ = nullptr;
  std::map<std::string, std::unique_ptr<Managed>> resources_;
  std::vector<std::thread> refresh_threads_;
  std::mutex stop_mu_;
  std::condition_variable stop_cv_;
  std::atomic<bool> stopping_{false};
  bool started_ = false;
};

}  // namespace resource
}  // namespace pine
