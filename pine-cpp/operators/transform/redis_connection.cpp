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

#include "pine/pine.hpp"
#include "pine/resource.hpp"
#include "redis/connection_pool.hpp"

#include <memory>
#include <stdexcept>
#include <string>

namespace pine {
namespace {

const bool _redis_connection_init = [] {
  resource::register_fetcher_factory("redis_connection", [](const Variant& params) {
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

    return resource::Fetcher{[host, port, password, db]() {
      return resource::ResourceValue::handle(
          std::make_shared<redis::RedisConnResource>(host, port, password, db));
    }};
  });
  return true;
}();

}  // namespace
}  // namespace pine
