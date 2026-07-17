#include "server/server.hpp"

namespace pine {
namespace server {

namespace {

// The built-in endpoints. Seeds the dynamic known-path set used for HTTP
// metrics normalization and for detecting custom route conflicts. Mirrors
// pine-go's defaultKnownPaths.
const char* const kDefaultKnownPaths[] = {"/execute", "/health", "/stats", "/dag"};

}  // namespace

bool validate_routes(const std::vector<Route>& routes, std::map<std::string, bool>& known, std::string& err) {
  known.clear();
  for (const char* p : kDefaultKnownPaths) {
    known[p] = true;
  }

  // Track the built-in endpoints separately so a custom route colliding with a
  // built-in reports the "conflicts with built-in endpoint" message rather than
  // the generic "duplicate" one, matching pine-go's two-map check
  // (defaultKnownPaths for built-ins, known for the growing set).
  std::map<std::string, bool> builtins = known;

  for (const auto& route : routes) {
    // Error messages are byte-for-byte identical to pine-go's validateRoutes;
    // Go's %q wraps the path in double quotes, which for ordinary ASCII paths
    // is just surrounding quotes.
    if (route.path.empty() || route.path[0] != '/') {
      err = "custom route path \"" + route.path + "\" must start with '/'";
      return false;
    }
    if (route.path == "/") {
      err = "custom route path \"/\" conflicts with the built-in not-found handler";
      return false;
    }
    if (builtins.count(route.path) != 0) {
      err = "custom route \"" + route.path + "\" conflicts with built-in endpoint";
      return false;
    }
    if (known.count(route.path) != 0) {
      err = "duplicate custom route \"" + route.path + "\"";
      return false;
    }
    if (!route.ingress) {
      err = "custom route \"" + route.path + "\" has nil Ingress";
      return false;
    }
    if (!route.egress) {
      err = "custom route \"" + route.path + "\" has nil Egress";
      return false;
    }
    known[route.path] = true;
  }
  return true;
}

std::string normalize_path(const std::string& path, const std::map<std::string, bool>& known) {
  if (known.count(path) != 0) {
    return path;
  }
  return "_other";
}

}  // namespace server
}  // namespace pine
