// Resource: redis_connection
// Description: Shared Redis connection pool. Created once and held for the
// lifetime of the ResourceManager (never refreshed: interval=-1). Operators
// such as transform_redis_get / transform_redis_set reference it by
// resource_name and borrow a pooled client per request, so multiple operators
// pointing at the same connection resource share a single pool. The pool is
// torn down when the ResourceManager retires and the last in-flight borrow is
// released. Mirrors pine-go's redis_connection resource and pine-java's
// equivalent.
//
// Params:
//   - addr (string, required): Redis server address (host:port).
//   - password (string, optional, default=""): Redis password.
//   - db (int, optional, default=0): Redis DB number.
//   - dial_timeout_ms (int, optional, default=2000): TCP dial timeout in ms.
//   - read_timeout_ms (int, optional, default=2000): Per-command read timeout
//     in ms; primary cascade-safety knob (see pine-go for the full incident
//     background).
//   - write_timeout_ms (int, optional, default=2000): Per-command write
//     timeout in ms.
//   - pool_timeout_ms (int, optional, default=2000): Accepted for cross-engine
//     parity. The C++ pool's acquire never blocks on a borrow (acquire
//     constructs a fresh client when no idle handle is available), so this
//     knob is currently a no-op in C++ — it is documented and kept on the
//     schema so a deployment can configure all three engines from the same
//     resource block. If a future refactor caps total concurrent connections,
//     this value will become the borrow wait deadline.
//   - pool_size (int, optional, default=0): Per-host idle-queue cap. 0 keeps
//     the legacy default (16). Mirrors pine-go's pool_size in spirit but
//     the C++ pool has no hard ceiling on total connections — only this
//     idle-queue cap.
//   - metrics_name (string, optional, default=""): When set, the pool emits its
//     own metrics (pool gauges + PING-probe latency) labelled name=<metrics_name>.
//     Empty disables resource-level metrics.

#include "pine/pine.hpp"
#include "pine/resource.hpp"

#include <chrono>
#include <memory>
#include <stdexcept>
#include <string>

#include "redis/connection_pool.hpp"

namespace pine {
namespace {

// Default cascade-safety timeouts — match pine-go / pine-java.
constexpr int kDefaultDialTimeoutMs = 2000;
constexpr int kDefaultReadTimeoutMs = 2000;
constexpr int kDefaultWriteTimeoutMs = 2000;
constexpr int kDefaultPoolTimeoutMs = 2000;

const bool _redis_connection_schema_init = [] {
  resource::ResourceSchema schema;
  schema.name = "redis_connection";
  schema.description = "Shared Redis connection pool borrowed by Redis operators via resource_name.";
  schema.default_interval = -1;
  schema.params["addr"] = ParamSchema{"string", true, Variant(), "Redis server address (host:port)."};
  schema.params["password"] = ParamSchema{"string", false, Variant(std::string("")), "Redis password."};
  schema.params["db"] = ParamSchema{"int", false, Variant(static_cast<double>(0)), "Redis DB number."};
  schema.params["dial_timeout_ms"] = ParamSchema{
      "int", false, Variant(static_cast<double>(kDefaultDialTimeoutMs)), "TCP dial timeout in ms."};
  schema.params["read_timeout_ms"] =
      ParamSchema{"int", false, Variant(static_cast<double>(kDefaultReadTimeoutMs)),
                  "Per-command read timeout in ms; primary cascade-safety knob."};
  schema.params["write_timeout_ms"] = ParamSchema{
      "int", false, Variant(static_cast<double>(kDefaultWriteTimeoutMs)), "Per-command write timeout in ms."};
  schema.params["pool_timeout_ms"] =
      ParamSchema{"int", false, Variant(static_cast<double>(kDefaultPoolTimeoutMs)),
                  "How long a borrower waits for a free pool connection in ms; "
                  "no-op in C++ (acquire never blocks)."};
  schema.params["pool_size"] = ParamSchema{"int", false, Variant(static_cast<double>(0)),
                                           "Per-host idle-queue cap; 0 = pool default (16)."};
  schema.params["metrics_name"] = ParamSchema{"string", false, Variant(std::string("")),
                                              "When set, the pool emits its own metrics labelled "
                                              "name=<metrics_name>. Empty disables resource-level metrics."};
  resource::register_resource_schema(std::move(schema));
  return true;
}();

int int_param_or_default(const Variant::object_t& obj, const std::string& key, int fallback) {
  auto it = obj.find(key);
  if (it != obj.end() && it->second.is_number()) {
    return static_cast<int>(it->second.as_number());
  }
  return fallback;
}

const bool _redis_connection_init = [] {
  resource::register_fetcher_factory("redis_connection", [](const Variant& params, metrics::Provider* mp) {
    const auto& obj = params.as_object();

    std::string host;
    int port = 6379;
    if (auto it = obj.find("addr"); it != obj.end() && it->second.is_string()) {
      const auto& addr = it->second.as_string();
      auto colon = addr.rfind(':');
      if (colon != std::string::npos) {
        host = addr.substr(0, colon);
        port = std::stoi(addr.substr(colon + 1));
      } else {
        host = addr;
      }
    }
    if (host.empty()) {
      throw std::runtime_error("redis_connection: addr is required");
    }

    std::string password;
    if (auto it = obj.find("password"); it != obj.end() && it->second.is_string()) {
      password = it->second.as_string();
    }
    int db = 0;
    if (auto it = obj.find("db"); it != obj.end() && it->second.is_number()) {
      db = static_cast<int>(it->second.as_number());
    }
    redis::ClientOptions opts;
    opts.dial_timeout =
        std::chrono::milliseconds(int_param_or_default(obj, "dial_timeout_ms", kDefaultDialTimeoutMs));
    opts.read_timeout =
        std::chrono::milliseconds(int_param_or_default(obj, "read_timeout_ms", kDefaultReadTimeoutMs));
    opts.write_timeout =
        std::chrono::milliseconds(int_param_or_default(obj, "write_timeout_ms", kDefaultWriteTimeoutMs));
    // pool_timeout_ms is accepted but currently a no-op in C++ — documented
    // on the schema so cross-engine config blocks stay symmetrical.
    (void)int_param_or_default(obj, "pool_timeout_ms", kDefaultPoolTimeoutMs);
    int pool_size = int_param_or_default(obj, "pool_size", 0);
    std::string metrics_name;
    if (auto it = obj.find("metrics_name"); it != obj.end() && it->second.is_string()) {
      metrics_name = it->second.as_string();
    }

    return resource::Fetcher{[host, port, password, db, metrics_name, mp, opts, pool_size]() {
      return resource::ResourceValue::handle(std::make_shared<redis::RedisConnResource>(
          host, port, password, db, metrics_name, mp, opts, static_cast<std::size_t>(pool_size)));
    }};
  });
  return true;
}();

}  // namespace
}  // namespace pine
