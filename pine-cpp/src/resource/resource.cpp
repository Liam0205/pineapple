#include "pine/resource.hpp"

#include "pine/pine.hpp"

#include <chrono>
#include <future>
#include <iostream>
#include <stdexcept>
#include <utility>

namespace pine {
namespace resource {

namespace {

std::mutex& registry_mu() {
  static std::mutex m;
  return m;
}

std::map<std::string, FetcherFactory>& factory_registry() {
  static std::map<std::string, FetcherFactory> r;
  return r;
}

}  // namespace

bool register_fetcher_factory(const std::string& type_name, FetcherFactory factory) {
  std::lock_guard<std::mutex> lk(registry_mu());
  auto& reg = factory_registry();
  if (reg.count(type_name)) {
    throw std::runtime_error("resource: duplicate fetcher type \"" + type_name + "\"");
  }
  reg.emplace(type_name, std::move(factory));
  return true;
}

// Built-in Static fetcher registration triggered at static initialization time.
// This allows both server-side mtime config reload and CLI parsing to resolve "type": "static" on
// resource_config out-of-the-box.
const bool _static_fetcher_init = [] {
  register_fetcher_factory("static", [](const Variant& params, metrics::Provider*) {
    auto val_it = params.as_object().find("value");
    Variant val = (val_it != params.as_object().end()) ? val_it->second : Variant();
    return Fetcher{[val]() { return ResourceValue::data(val); }};
  });
  return true;
}();

const FetcherFactory* lookup_fetcher_factory(const std::string& type_name) {
  std::lock_guard<std::mutex> lk(registry_mu());
  auto& reg = factory_registry();
  auto it = reg.find(type_name);
  if (it == reg.end()) {
    return nullptr;
  }
  return &it->second;
}

std::vector<std::string> registered_fetcher_types() {
  std::lock_guard<std::mutex> lk(registry_mu());
  std::vector<std::string> out;
  for (const auto& kv : factory_registry()) {
    out.push_back(kv.first);
  }
  return out;  // map iteration is already sorted by key
}

void reset_fetcher_registry() {
  std::lock_guard<std::mutex> lk(registry_mu());
  factory_registry().clear();
}

Manager::Manager(metrics::Provider* mp) : metrics_(mp ? mp : metrics::nop_provider()) {
}

Manager::~Manager() {
  stop();
}

void Manager::register_resource(const std::string& name, Fetcher fetcher, std::chrono::seconds interval) {
  std::unique_lock<std::shared_mutex> lk(mu_);
  if (started_) {
    throw std::runtime_error("resource: register_resource called after start()");
  }
  if (resources_.count(name)) {
    throw std::runtime_error("resource: duplicate resource name \"" + name + "\"");
  }
  // Interval semantics mirror pine-go / pine-java: 0 means "use the default",
  // a negative value means "never refresh" (fetched once at start, held until
  // stop — used by long-lived resources such as connection pools that have no
  // meaningful refresh). A positive value is the refresh period in seconds.
  if (interval == std::chrono::seconds(0)) {
    interval = std::chrono::minutes(10);
  } else if (interval < std::chrono::seconds(0)) {
    interval = std::chrono::seconds(-1);
  }
  auto m = std::make_unique<Managed>();
  m->name = name;
  m->fetcher = std::move(fetcher);
  m->interval = interval;
  resources_.emplace(name, std::move(m));
}

void Manager::load_from_config(const Config& config) {
  if (config.resource_config.empty()) {
    return;
  }
  for (const auto& [name, entry] : config.resource_config) {
    const auto* factory = lookup_fetcher_factory(entry.type);
    if (!factory) {
      throw std::runtime_error("resource: unknown fetcher type \"" + entry.type + "\" for resource \"" +
                               name + "\"");
    }
    Fetcher fetcher = (*factory)(entry.params, metrics_);
    // Pass the sign through verbatim so register_resource can apply the
    // three-state rule (0 → default, <0 → never-refresh, >0 → period). A
    // negative interval must not collapse to 0, or a never-refresh resource
    // (e.g. a connection pool) would be rescheduled on the default period.
    register_resource(name, std::move(fetcher), std::chrono::seconds(entry.interval));
  }
  // Every load_from_config call now validates resource dependencies
  // against the operators in the same config. The previous design ran
  // validate_resource_deps only from server.cpp / pineapple-run; unit
  // tests, Python bindings, or any future caller that constructed an
  // Engine + Manager directly silently skipped the check. Closing the
  // validate-or-die loop here makes the invariant unsurvivable from any
  // path that loads config.
  validate_resource_deps(config);
}

void Manager::start() {
  std::vector<Managed*> to_refresh;
  {
    std::unique_lock<std::shared_mutex> lk(mu_);
    if (started_) {
      throw std::runtime_error("resource: already started");
    }
    // Synchronous initial load — propagate failure so callers see it.
    // Wrap each fetcher in std::async + wait_for(30s) so a
    // hung backend (DNS resolution stall, broker dead, etc.) does
    // NOT hang server startup forever. Fetchers themselves already
    // honour socket-level SO_RCVTIMEO / SO_SNDTIMEO, so 30 s is an
    // outer fence; the future's destructor will still block until
    // the task finishes, but the inner socket timeout bounds that
    // wait to a few seconds beyond the outer deadline.
    constexpr auto kInitDeadline = std::chrono::seconds(30);
    for (auto& [name, r] : resources_) {
      auto fut = std::async(std::launch::async, [&r] { return r->fetcher(); });
      if (fut.wait_for(kInitDeadline) != std::future_status::ready) {
        throw std::runtime_error("resource: initial fetch of \"" + name + "\" timed out after " +
                                 std::to_string(kInitDeadline.count()) + "s");
      }
      r->value = fut.get();
      r->loaded = true;
      // A non-positive interval (the -1 never-refresh sentinel) holds the
      // value fetched above until stop(); no refresh thread is scheduled.
      if (r->interval > std::chrono::seconds(0)) {
        to_refresh.push_back(r.get());
      }
    }
    started_ = true;
  }
  for (auto* r : to_refresh) {
    refresh_threads_.emplace_back([this, r]() { refresh_loop(r); });
  }
}

void Manager::stop() {
  {
    std::unique_lock<std::shared_mutex> lk(mu_);
    if (!started_) {
      return;
    }
  }
  stopping_.store(true, std::memory_order_release);
  stop_cv_.notify_all();
  for (auto& t : refresh_threads_) {
    if (t.joinable()) {
      t.join();
    }
  }
  refresh_threads_.clear();
  std::unique_lock<std::shared_mutex> lk(mu_);
  // Release handle-typed values so the underlying live object (e.g. a Redis
  // connection pool) is torn down at retirement, mirroring pine-go /
  // pine-java's stop() closing AutoCloseable resources. Destruction runs via
  // shared_ptr RAII once the last in-flight borrower drops its reference;
  // engine_mu_ lock ordering in the server guarantees no execute() is in
  // flight when stop() runs. Data values are left intact. Resetting to a
  // default (data) ResourceValue also makes a second stop() a no-op.
  for (auto& [name, r] : resources_) {
    if (r->loaded && r->value.is_handle()) {
      r->value = ResourceValue();
      r->loaded = false;
    }
  }
  started_ = false;
  stopping_.store(false, std::memory_order_release);
}

std::map<std::string, Variant> Manager::snapshot() const {
  std::shared_lock<std::shared_mutex> lk(mu_);
  std::map<std::string, Variant> out;
  for (const auto& [name, r] : resources_) {
    // Only data resources are exported into the per-execute resources map;
    // handle-typed resources (connection pools) are reached via borrow().
    if (r->loaded && r->value.is_data()) {
      out.emplace(name, r->value.as_data());
    }
  }
  return out;
}

std::shared_ptr<void> Manager::borrow(const std::string& name) const {
  std::shared_lock<std::shared_mutex> lk(mu_);
  auto it = resources_.find(name);
  if (it == resources_.end() || !it->second->loaded) {
    return nullptr;
  }
  // Returns nullptr for data-typed values; callers degrade.
  return it->second->value.raw_handle();
}

std::vector<std::string> Manager::names() const {
  std::shared_lock<std::shared_mutex> lk(mu_);
  std::vector<std::string> out;
  for (const auto& kv : resources_) {
    out.push_back(kv.first);
  }
  return out;
}

void Manager::validate_resource_deps(const Config& config) const {
  std::shared_lock<std::shared_mutex> lk(mu_);
  std::vector<std::string> missing;
  for (const auto& [op_key, params] : config.operators) {
    auto res_it = params.params.as_object().find("resource_name");
    if (res_it == params.params.as_object().end()) {
      continue;
    }
    if (!res_it->second.is_string()) {
      continue;
    }
    std::string name = res_it->second.as_string();
    if (name.empty()) {
      continue;
    }
    if (!resources_.count(name)) {
      missing.push_back(name + " (operator " + params.type_name + "/" + op_key + ")");
    }
  }
  if (!missing.empty()) {
    std::string err_msg = "resource: missing resource definitions: ";
    for (std::size_t i = 0; i < missing.size(); ++i) {
      if (i > 0) {
        err_msg += ", ";
      }
      err_msg += missing[i];
    }
    // Raise the canonical ConfigError so callers see the
    // `pine: config error: ...` prefix and exception type that
    // matches every other init-time config defect.
    throw ConfigError(err_msg);
  }
}

void Manager::refresh_loop(Managed* r) {
  while (!stopping_.load(std::memory_order_acquire)) {
    std::unique_lock<std::mutex> lk(stop_mu_);
    if (stop_cv_.wait_for(lk, r->interval, [this] { return stopping_.load(std::memory_order_acquire); })) {
      return;  // stopping
    }
    lk.unlock();
    try {
      ResourceValue val = r->fetcher();
      std::unique_lock<std::shared_mutex> wlk(mu_);
      r->value = std::move(val);
    } catch (const std::exception& e) {
      std::cerr << "[resource] refresh \"" << r->name << "\" failed: " << e.what()
                << " (keeping old value)\n";
    }
  }
}

}  // namespace resource
}  // namespace pine
