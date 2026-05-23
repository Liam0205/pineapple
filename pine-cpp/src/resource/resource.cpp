#include "pine/resource.hpp"

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
// This allows both server-side mtime config reload and CLI parsing to resolve "type": "static" on resource_config out-of-the-box.
const bool _static_fetcher_init = [] {
    register_fetcher_factory("static", [](const JsonValue& params) {
        auto val_it = params.as_object().find("value");
        JsonValue val = (val_it != params.as_object().end()) ? val_it->second : JsonValue();
        return Fetcher{[val]() { return val; }};
    });
    return true;
}();

const FetcherFactory* lookup_fetcher_factory(const std::string& type_name) {
    std::lock_guard<std::mutex> lk(registry_mu());
    auto& reg = factory_registry();
    auto it = reg.find(type_name);
    if (it == reg.end()) return nullptr;
    return &it->second;
}

std::vector<std::string> registered_fetcher_types() {
    std::lock_guard<std::mutex> lk(registry_mu());
    std::vector<std::string> out;
    for (const auto& kv : factory_registry()) out.push_back(kv.first);
    return out;  // map iteration is already sorted by key
}

void reset_fetcher_registry() {
    std::lock_guard<std::mutex> lk(registry_mu());
    factory_registry().clear();
}

Manager::Manager() = default;

Manager::~Manager() { stop(); }

void Manager::register_resource(const std::string& name, Fetcher fetcher,
                                std::chrono::seconds interval) {
    std::unique_lock<std::shared_mutex> lk(mu_);
    if (started_) {
        throw std::runtime_error("resource: register_resource called after start()");
    }
    if (resources_.count(name)) {
        throw std::runtime_error("resource: duplicate resource name \"" + name + "\"");
    }
    if (interval <= std::chrono::seconds(0)) {
        interval = std::chrono::minutes(10);
    }
    auto m = std::make_unique<Managed>();
    m->name = name;
    m->fetcher = std::move(fetcher);
    m->interval = interval;
    resources_.emplace(name, std::move(m));
}

void Manager::load_from_config(const Config& config) {
    if (config.resource_config.empty()) return;
    for (const auto& [name, entry] : config.resource_config) {
        const auto* factory = lookup_fetcher_factory(entry.type);
        if (!factory) {
            throw std::runtime_error("resource: unknown fetcher type \"" + entry.type +
                                     "\" for resource \"" + name + "\"");
        }
        Fetcher fetcher = (*factory)(entry.params);
        auto interval = entry.interval > 0 ? std::chrono::seconds(entry.interval)
                                           : std::chrono::seconds(0);
        register_resource(name, std::move(fetcher), interval);
    }
}

void Manager::start() {
    std::vector<Managed*> to_refresh;
    {
        std::unique_lock<std::shared_mutex> lk(mu_);
        if (started_) {
            throw std::runtime_error("resource: already started");
        }
        // Synchronous initial load — propagate failure so callers see it.
        // P1-P6: wrap each fetcher in std::async + wait_for(30s) so a
        // hung backend (DNS resolution stall, broker dead, etc.) does
        // NOT hang server startup forever. Fetchers themselves already
        // honour socket-level SO_RCVTIMEO / SO_SNDTIMEO, so 30 s is an
        // outer fence; the future's destructor will still block until
        // the task finishes, but the inner socket timeout bounds that
        // wait to a few seconds beyond the outer deadline.
        constexpr auto kInitDeadline = std::chrono::seconds(30);
        for (auto& [name, r] : resources_) {
            auto fut = std::async(std::launch::async, [&r] {
                return r->fetcher();
            });
            if (fut.wait_for(kInitDeadline) != std::future_status::ready) {
                throw std::runtime_error(
                    "resource: initial fetch of \"" + name +
                    "\" timed out after " + std::to_string(kInitDeadline.count()) + "s");
            }
            r->value = fut.get();
            r->loaded = true;
            to_refresh.push_back(r.get());
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
        if (!started_) return;
    }
    stopping_.store(true, std::memory_order_release);
    stop_cv_.notify_all();
    for (auto& t : refresh_threads_) {
        if (t.joinable()) t.join();
    }
    refresh_threads_.clear();
    std::unique_lock<std::shared_mutex> lk(mu_);
    started_ = false;
    stopping_.store(false, std::memory_order_release);
}

std::map<std::string, JsonValue> Manager::snapshot() const {
    std::shared_lock<std::shared_mutex> lk(mu_);
    std::map<std::string, JsonValue> out;
    for (const auto& [name, r] : resources_) {
        if (r->loaded) out.emplace(name, r->value);
    }
    return out;
}

std::vector<std::string> Manager::names() const {
    std::shared_lock<std::shared_mutex> lk(mu_);
    std::vector<std::string> out;
    for (const auto& kv : resources_) out.push_back(kv.first);
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
            if (i > 0) err_msg += ", ";
            err_msg += missing[i];
        }
        throw std::runtime_error(err_msg);
    }
}

void Manager::refresh_loop(Managed* r) {
    while (!stopping_.load(std::memory_order_acquire)) {
        std::unique_lock<std::mutex> lk(stop_mu_);
        if (stop_cv_.wait_for(lk, r->interval, [this] {
                return stopping_.load(std::memory_order_acquire);
            })) {
            return;  // stopping
        }
        lk.unlock();
        try {
            JsonValue val = r->fetcher();
            std::unique_lock<std::shared_mutex> wlk(mu_);
            r->value = std::move(val);
        } catch (const std::exception& e) {
            std::cerr << "[resource] refresh \"" << r->name
                      << "\" failed: " << e.what() << " (keeping old value)\n";
        }
    }
}

}  // namespace resource
}  // namespace pine
