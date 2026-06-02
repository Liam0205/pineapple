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
//   - metrics_name (string, optional, default=""): When set, the pool emits its
//     own metrics (pool gauges + PING-probe latency) labelled name=<metrics_name>.
//     Empty disables resource-level metrics.

#include "pine/pine.hpp"
#include "pine/resource.hpp"

#include <memory>
#include <stdexcept>
#include <string>

#include "redis/connection_pool.hpp"

namespace pine {
namespace {

const bool _redis_connection_schema_init = [] {
  resource::ResourceSchema schema;
  schema.name = "redis_connection";
  schema.description = "Shared Redis connection pool borrowed by Redis operators via resource_name.";
  schema.default_interval = -1;
  schema.params["addr"] = ParamSchema{"string", true, Variant(), "Redis server address (host:port)."};
  schema.params["password"] = ParamSchema{"string", false, Variant(std::string("")), "Redis password."};
  schema.params["db"] = ParamSchema{"int", false, Variant(static_cast<double>(0)), "Redis DB number."};
  schema.params["metrics_name"] = ParamSchema{"string", false, Variant(std::string("")),
                                              "When set, the pool emits its own metrics labelled "
                                              "name=<metrics_name>. Empty disables resource-level metrics."};
  resource::register_resource_schema(std::move(schema));
  return true;
}();

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
    std::string metrics_name;
    if (auto it = obj.find("metrics_name"); it != obj.end() && it->second.is_string()) {
      metrics_name = it->second.as_string();
    }

    return resource::Fetcher{[host, port, password, db, metrics_name, mp]() {
      return resource::ResourceValue::handle(
          std::make_shared<redis::RedisConnResource>(host, port, password, db, metrics_name, mp));
    }};
  });
  return true;
}();

}  // namespace
}  // namespace pine
