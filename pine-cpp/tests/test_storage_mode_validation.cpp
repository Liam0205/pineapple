#include <doctest/doctest.h>

#include <string>

#include "pine/pine.hpp"

using namespace pine;

namespace {

// Smallest config that passes validate_config(), so these cases exercise the
// storage_mode and root-string checks rather than tripping on something else.
std::string minimal_config(const std::string& extra_root) {
  return "{" + extra_root +
         R"("pipeline_config": {
           "operators": {"op": {"type_name": "transform_copy",
             "direction": "common_to_item",
             "$metadata": {"common_input": ["v"], "common_output": [],
               "item_input": [], "item_output": ["v"]}}},
           "pipeline_map": {"s": {"pipeline": ["op"]}}},
         "pipeline_group": {"main": {"pipeline": ["s"]}},
         "flow_contract": {"common_input": ["v"], "item_input": ["id"],
           "common_output": [], "item_output": ["id", "v"]}})";
}

std::string with_storage_mode(const std::string& raw_json) {
  return minimal_config("\"storage_mode\": " + raw_json + ",");
}

}  // namespace

// Pins the fail-fast added in issue #187. Before it, an invalid storage_mode was
// silently accepted and fell back to row storage in all three runtimes — that
// fallback DIRECTION was aligned in #179, but the silence itself remained, so a
// typo produced a working engine with the opposite memory and performance profile
// from the one requested.
//
// Rejecting at config load rather than in make_frame is deliberate: make_frame's
// rule is "only the exact literal column selects the column store, everything
// else is row", mirroring pine-go's NewFrame default branch. Validation makes the
// invalid value unreachable instead of complicating dispatch.
TEST_CASE("config: storage_mode accepts only row, column, empty or null") {
  for (const char* ok : {"\"row\"", "\"column\"", "\"\"", "null"}) {
    CHECK_NOTHROW(load_config_from_json(with_storage_mode(ok)));
  }
  // Absent key leaves the "row" default.
  CHECK_NOTHROW(load_config_from_json(minimal_config("")));

  // Every invalid string, including the two that diverged across runtimes before
  // #179: "colunm" (a typo) and "Column" (wrong case).
  for (const char* bad : {"\"colunm\"", "\"Column\"", "\"COLUMN\"", "\"col\"", "\"columns\"",
                          "\"column \"", "\" column\"", "\"unknown\""}) {
    const std::string cfg = with_storage_mode(bad);
    CHECK_THROWS_AS(load_config_from_json(cfg), ConfigError);
  }
}

// Pins the type half of #187, which was never specific to storage_mode.
//
// pine-go rejects a non-string for EVERY root string field, because they are all
// declared `string` and encoding/json fails the whole unmarshal. This runtime used
// to behave three different ways: storage_mode called the throwing as_string(),
// while log_prefix and the two _PINEAPPLE_* fields were guarded with is_string()
// and SILENTLY IGNORED a wrong type. So `"log_prefix": 123` was rejected by
// pine-go, coerced by pine-java, and dropped on the floor here.
TEST_CASE("config: root string fields reject a non-string value") {
  for (const char* field : {"storage_mode", "log_prefix", "_PINEAPPLE_VERSION",
                            "_PINEAPPLE_CREATE_TIME"}) {
    for (const char* val : {"123", "1.5", "true", "false", "[1,2]", "{\"a\":1}"}) {
      const std::string cfg =
          minimal_config(std::string("\"") + field + "\": " + val + ",");
      CHECK_THROWS_AS(load_config_from_json(cfg), ConfigError);
    }
    // null leaves the default rather than erroring, matching pine-go where
    // decoding a JSON null into a string field is a no-op.
    const std::string null_cfg =
        minimal_config(std::string("\"") + field + "\": null,");
    CHECK_NOTHROW(load_config_from_json(null_cfg));
  }
}
