#include "pine/pine.hpp"
#include "pine/resource.hpp"

#include <doctest/doctest.h>

#include <atomic>
#include <chrono>
#include <memory>
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
  mgr.register_resource("static_one", []() { return resource::ResourceValue::data(Variant(std::string("v1"))); }, std::chrono::seconds(60));
  mgr.start();

  auto snap = mgr.snapshot();
  CHECK(snap.count("static_one") == 1);
  CHECK(snap["static_one"].as_string() == "v1");
  mgr.stop();
}

TEST_CASE("resource: duplicate name throws") {
  resource::Manager mgr;
  mgr.register_resource("dup", []() { return resource::ResourceValue::data(Variant(1)); }, std::chrono::seconds(60));
  CHECK_THROWS_AS(mgr.register_resource(
                      "dup", []() { return resource::ResourceValue::data(Variant(2)); }, std::chrono::seconds(60)),
                  std::runtime_error);
}

TEST_CASE("resource: factory registry roundtrip") {
  RegistryFixture _;
  int factory_calls = 0;
  resource::register_fetcher_factory("test_factory", [&factory_calls](const Variant& /*params*/) {
    factory_calls++;
    return resource::Fetcher{[]() { return resource::ResourceValue::data(Variant(std::string("hello"))); }};
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
      "tick", [&counter]() { return resource::ResourceValue::data(Variant(counter.fetch_add(1) + 1)); }, std::chrono::seconds(1));
  mgr.start();

  auto initial = mgr.snapshot()["tick"].as_number();
  CHECK(initial == 1.0);

  // Wait just over the 1s refresh interval.
  std::this_thread::sleep_for(std::chrono::milliseconds(1200));
  auto next = mgr.snapshot()["tick"].as_number();
  CHECK(next > initial);
  mgr.stop();
}

TEST_CASE("resource: interval=-1 never refreshes") {
  std::atomic<int> calls{0};
  resource::Manager mgr;
  // interval -1 → fetched once at start, no refresh thread scheduled.
  mgr.register_resource(
      "conn", [&calls]() { return resource::ResourceValue::data(Variant(calls.fetch_add(1) + 1)); }, std::chrono::seconds(-1));
  mgr.start();

  CHECK(mgr.snapshot()["conn"].as_number() == 1.0);
  // Wait well beyond any plausible tick; the fetcher must still have run once.
  std::this_thread::sleep_for(std::chrono::milliseconds(1200));
  CHECK(calls.load() == 1);
  CHECK(mgr.snapshot()["conn"].as_number() == 1.0);
  mgr.stop();
}

TEST_CASE("resource: interval=-1 survives load_from_config") {
  RegistryFixture _;
  std::atomic<int> calls{0};
  resource::register_fetcher_factory("never_refresh", [&calls](const Variant& /*params*/) {
    return resource::Fetcher{[&calls]() { return resource::ResourceValue::data(Variant(calls.fetch_add(1) + 1)); }};
  });

  Config cfg;
  cfg.resource_config["db"] = ResourceEntry{"never_refresh", -1, Variant{}};

  resource::Manager mgr;
  mgr.load_from_config(cfg);
  mgr.start();
  CHECK(mgr.snapshot()["db"].as_number() == 1.0);
  std::this_thread::sleep_for(std::chrono::milliseconds(1200));
  CHECK(calls.load() == 1);
  mgr.stop();
}

namespace {
struct FakeHandle {
  int id;
};
}  // namespace

TEST_CASE("resource: handle-typed value is borrowable but absent from snapshot") {
  auto handle = std::make_shared<FakeHandle>(FakeHandle{42});
  resource::Manager mgr;
  // interval -1: handle resources are long-lived, fetched once, never refreshed.
  mgr.register_resource(
      "pool", [handle]() { return resource::ResourceValue::handle(handle); }, std::chrono::seconds(-1));
  mgr.start();

  // Handle resources are NOT exported into the per-execute data snapshot.
  CHECK(mgr.snapshot().count("pool") == 0);

  // ...but are reachable via borrow(), cast back to the concrete type.
  auto borrowed = std::static_pointer_cast<FakeHandle>(mgr.borrow("pool"));
  REQUIRE(borrowed != nullptr);
  CHECK(borrowed->id == 42);

  mgr.stop();
}

TEST_CASE("resource: borrow returns null for data-typed and missing names") {
  resource::Manager mgr;
  mgr.register_resource(
      "value", []() { return resource::ResourceValue::data(Variant(std::string("v"))); }, std::chrono::seconds(-1));
  mgr.start();

  // Data-typed resource: borrow() degrades to nullptr (use snapshot instead).
  CHECK(mgr.borrow("value") == nullptr);
  // Unknown name: nullptr.
  CHECK(mgr.borrow("nope") == nullptr);
  // Data resource is still visible in the snapshot.
  CHECK(mgr.snapshot()["value"].as_string() == "v");

  mgr.stop();
}

TEST_CASE("resource: stop() tears down handle resources") {
  std::weak_ptr<FakeHandle> weak;
  resource::Manager mgr;
  // The fetcher creates the handle internally and returns it, so the only
  // strong reference after start() lives in the Manager's stored value — not
  // in the fetcher closure (which captures weak by reference only).
  mgr.register_resource(
      "pool",
      [&weak]() {
        auto h = std::make_shared<FakeHandle>(FakeHandle{7});
        weak = h;
        return resource::ResourceValue::handle(h);
      },
      std::chrono::seconds(-1));
  mgr.start();
  // Manager holds the only strong reference.
  CHECK(weak.lock() != nullptr);

  mgr.stop();
  // stop() released the Manager's handle reference, so the live object is torn
  // down via shared_ptr RAII — mirrors Go/Java stop() closing the resource.
  CHECK(weak.expired());
}

