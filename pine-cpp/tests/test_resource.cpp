#include "pine/pine.hpp"
#include "pine/resource.hpp"

#include <doctest/doctest.h>

#include <atomic>
#include <chrono>
#include <string>
#include <thread>

using namespace pine;

namespace {

struct RegistryFixture {
  RegistryFixture() {
    resource::reset_fetcher_registry();
  }
  ~RegistryFixture() {
    resource::reset_fetcher_registry();
  }
};

}  // namespace

TEST_CASE("resource: register_resource + snapshot returns loaded values") {
  resource::Manager mgr;
  mgr.register_resource("static_one", []() { return Variant(std::string("v1")); }, std::chrono::seconds(60));
  mgr.start();

  auto snap = mgr.snapshot();
  CHECK(snap.count("static_one") == 1);
  CHECK(snap["static_one"].as_string() == "v1");
  mgr.stop();
}

TEST_CASE("resource: duplicate name throws") {
  resource::Manager mgr;
  mgr.register_resource("dup", []() { return Variant(1); }, std::chrono::seconds(60));
  CHECK_THROWS_AS(mgr.register_resource(
                      "dup", []() { return Variant(2); }, std::chrono::seconds(60)),
                  std::runtime_error);
}

TEST_CASE("resource: factory registry roundtrip") {
  RegistryFixture _;
  int factory_calls = 0;
  resource::register_fetcher_factory("test_factory", [&factory_calls](const Variant& /*params*/) {
    factory_calls++;
    return resource::Fetcher{[]() { return Variant(std::string("hello")); }};
  });
  CHECK(resource::lookup_fetcher_factory("test_factory") != nullptr);
  CHECK(resource::lookup_fetcher_factory("missing") == nullptr);
  auto types = resource::registered_fetcher_types();
  CHECK(types.size() == 1);
  CHECK(types[0] == "test_factory");

  Config cfg;
  cfg.resource_config["r1"] = ResourceEntry{"test_factory", 0, Variant{}};

  resource::Manager mgr;
  mgr.load_from_config(cfg);
  CHECK(factory_calls == 1);
  mgr.start();
  auto snap = mgr.snapshot();
  CHECK(snap["r1"].as_string() == "hello");
  mgr.stop();
}

TEST_CASE("resource: load_from_config rejects unknown types") {
  RegistryFixture _;
  Config cfg;
  cfg.resource_config["x"] = ResourceEntry{"unknown_type", 0, Variant{}};

  resource::Manager mgr;
  CHECK_THROWS_AS(mgr.load_from_config(cfg), std::runtime_error);
}

TEST_CASE("resource: background refresh updates the value") {
  std::atomic<int> counter{0};
  resource::Manager mgr;
  mgr.register_resource(
      "tick", [&counter]() { return Variant(counter.fetch_add(1) + 1); }, std::chrono::seconds(1));
  mgr.start();

  auto initial = mgr.snapshot()["tick"].as_number();
  CHECK(initial == 1.0);

  // Wait just over the 1s refresh interval.
  std::this_thread::sleep_for(std::chrono::milliseconds(1200));
  auto next = mgr.snapshot()["tick"].as_number();
  CHECK(next > initial);
  mgr.stop();
}
