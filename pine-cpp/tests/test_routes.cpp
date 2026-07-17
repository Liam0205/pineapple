#include <doctest/doctest.h>

#include <map>
#include <string>
#include <vector>

#include "server/server.hpp"

using pine::server::normalize_path;
using pine::server::Route;
using pine::server::RouteRequest;
using pine::server::RouteResponse;
using pine::server::validate_routes;

namespace {

// Passthrough ingress / discard egress used to satisfy the non-nil checks.
// Mirrors pine-go routes_test.go's passthroughIngress / discardEgress.
Route make_valid_route(const std::string& method, const std::string& path) {
  Route r;
  r.method = method;
  r.path = path;
  r.ingress = [](const RouteRequest&) { return pine::Request{}; };
  r.egress = [](RouteResponse&, const RouteRequest&, const pine::Result*, const std::string&) {};
  return r;
}

}  // namespace

// Mirrors pine-go TestValidateRoutes: every rejection message plus the
// success path filling `known` with built-ins and custom paths. The error
// strings are asserted for byte-exact parity with pine-go's validateRoutes.
TEST_CASE("validate_routes: no routes yields built-in known set") {
  std::map<std::string, bool> known;
  std::string err;
  CHECK(validate_routes({}, known, err));
  CHECK(err.empty());
  CHECK(known.count("/execute") == 1);
  CHECK(known.count("/health") == 1);
  CHECK(known.count("/stats") == 1);
  CHECK(known.count("/dag") == 1);
  CHECK(known.size() == 4);
}

TEST_CASE("validate_routes: valid route added to known set") {
  std::map<std::string, bool> known;
  std::string err;
  std::vector<Route> routes{make_valid_route("POST", "/api/v1/report")};
  CHECK(validate_routes(routes, known, err));
  CHECK(err.empty());
  CHECK(known.count("/api/v1/report") == 1);
  // Built-ins still present alongside the custom path.
  CHECK(known.count("/execute") == 1);
  CHECK(known.size() == 5);
}

TEST_CASE("validate_routes: empty path rejected") {
  std::map<std::string, bool> known;
  std::string err;
  std::vector<Route> routes{make_valid_route("", "")};
  CHECK_FALSE(validate_routes(routes, known, err));
  CHECK(err == "custom route path \"\" must start with '/'");
}

TEST_CASE("validate_routes: relative path rejected") {
  std::map<std::string, bool> known;
  std::string err;
  std::vector<Route> routes{make_valid_route("", "api")};
  CHECK_FALSE(validate_routes(routes, known, err));
  CHECK(err == "custom route path \"api\" must start with '/'");
}

TEST_CASE("validate_routes: root path conflicts with not-found handler") {
  std::map<std::string, bool> known;
  std::string err;
  std::vector<Route> routes{make_valid_route("", "/")};
  CHECK_FALSE(validate_routes(routes, known, err));
  CHECK(err == "custom route path \"/\" conflicts with the built-in not-found handler");
}

TEST_CASE("validate_routes: conflicts with built-in endpoints") {
  for (const std::string builtin : {"/execute", "/health", "/stats", "/dag"}) {
    std::map<std::string, bool> known;
    std::string err;
    std::vector<Route> routes{make_valid_route("", builtin)};
    CHECK_FALSE(validate_routes(routes, known, err));
    CHECK(err == "custom route \"" + builtin + "\" conflicts with built-in endpoint");
  }
}

TEST_CASE("validate_routes: duplicate custom route rejected") {
  std::map<std::string, bool> known;
  std::string err;
  std::vector<Route> routes{make_valid_route("POST", "/api/v1/report"),
                            make_valid_route("POST", "/api/v1/report")};
  CHECK_FALSE(validate_routes(routes, known, err));
  CHECK(err == "duplicate custom route \"/api/v1/report\"");
}

TEST_CASE("validate_routes: nil ingress rejected") {
  std::map<std::string, bool> known;
  std::string err;
  Route r;
  r.path = "/a";
  r.egress = [](RouteResponse&, const RouteRequest&, const pine::Result*, const std::string&) {};
  // ingress left default-constructed (empty std::function).
  CHECK_FALSE(validate_routes({r}, known, err));
  CHECK(err == "custom route \"/a\" has nil Ingress");
}

TEST_CASE("validate_routes: nil egress rejected") {
  std::map<std::string, bool> known;
  std::string err;
  Route r;
  r.path = "/a";
  r.ingress = [](const RouteRequest&) { return pine::Request{}; };
  // egress left default-constructed (empty std::function).
  CHECK_FALSE(validate_routes({r}, known, err));
  CHECK(err == "custom route \"/a\" has nil Egress");
}

// Mirrors the intent of pine-go's normalizePath tests: built-in endpoints and
// registered custom paths report under their own path; anything else collapses
// to "_other" to keep the metric label cardinality bounded.
TEST_CASE("normalize_path: built-ins and custom paths are known, rest is _other") {
  std::map<std::string, bool> known;
  std::string err;
  std::vector<Route> routes{make_valid_route("POST", "/api/echo")};
  REQUIRE(validate_routes(routes, known, err));

  // Built-in endpoints stay under their own label.
  CHECK(normalize_path("/execute", known) == "/execute");
  CHECK(normalize_path("/health", known) == "/health");
  CHECK(normalize_path("/stats", known) == "/stats");
  CHECK(normalize_path("/dag", known) == "/dag");

  // The registered custom path reports under itself, not "_other".
  CHECK(normalize_path("/api/echo", known) == "/api/echo");

  // Anything unknown collapses to "_other".
  CHECK(normalize_path("/unknown", known) == "_other");
  CHECK(normalize_path("/api/echo/extra", known) == "_other");
  CHECK(normalize_path("/", known) == "_other");
  CHECK(normalize_path("", known) == "_other");
}
