// Mirrors pine-go internal/runtime/template_test.go and pine-java
// TemplateResolverTest.java byte-for-byte so cross-runtime error wording
// stays in lockstep (issue #74).
#include "pine/column_frame.hpp"
#include "pine/operator.hpp"
#include "pine/operator_input.hpp"
#include "pine/pine.hpp"
#include "pine/template.hpp"

#include <doctest/doctest.h>

#include <string>
#include <unordered_map>
#include <vector>

using namespace pine;

namespace {

// Build a minimal OperatorSchema with one param.
OperatorSchema schema_with(const std::string& param_name, const std::string& type, bool templatable) {
  OperatorSchema s;
  s.name = "op_a";
  s.type = OpType::Transform;
  s.description = "test";
  ParamSchema p;
  p.type = type;
  p.required = false;
  p.description = "x";
  p.templatable = templatable;
  s.params.emplace(param_name, std::move(p));
  return s;
}

// Wrap a common-only frame for resolve_templated_params, which reads
// from the raw request Frame (post-#74 bucket split — template source
// fields are visible on the frame even when hidden from the
// operator-visible input).
struct InputHarness {
  ColumnFrame frame;

  explicit InputHarness(Variant::object_t common) : frame(std::move(common), {}) {
  }
};

Variant make_params(std::initializer_list<std::pair<std::string, Variant>> entries) {
  Variant::object_t obj;
  for (auto& e : entries) {
    obj[e.first] = e.second;
  }
  return Variant(obj);
}

}  // namespace

TEST_CASE("is_templated_string classifies inputs") {
  CHECK_FALSE(is_templated_string(Variant("plain")));
  CHECK(is_templated_string(Variant("{{x}}")));
  CHECK(is_templated_string(Variant("prefix-{{x}}-suffix")));
  CHECK_FALSE(is_templated_string(Variant("{{}}")));
  CHECK_FALSE(is_templated_string(Variant(nullptr)));
  CHECK_FALSE(is_templated_string(Variant(42.0)));
  CHECK_FALSE(is_templated_string(Variant(true)));
}

TEST_CASE("build_templated_param_plan: no markers → empty plan") {
  auto plan = build_templated_param_plan("name", schema_with("k", "string", true),
                                         make_params({{"k", Variant("no markers")}}));
  CHECK(plan.empty());
}

TEST_CASE("build_templated_param_plan: happy path captures field") {
  auto plan = build_templated_param_plan("name", schema_with("k", "int64", true),
                                         make_params({{"k", Variant("{{user_id}}")}}));
  REQUIRE(plan.size() == 1);
  CHECK(plan[0].name == "k");
  CHECK(plan[0].scalar_type == "int64");
  CHECK(plan[0].field == "user_id");
}

TEST_CASE("build_templated_param_plan: rejects non-bare marker") {
  // L0 contract: literal text around the marker is rejected at engine
  // build time. Apple validator catches this earlier, but the runtime
  // re-checks in case of hand-edited JSON.
  for (const char* bad : {"prefix-{{x}}", "{{x}}-suffix", "tenant:{{tenant_id}}:", "{{a}}{{b}}"}) {
    try {
      build_templated_param_plan("name", schema_with("k", "string", true),
                                 make_params({{"k", Variant(bad)}}));
      FAIL("expected ConfigError for value: " << bad);
    } catch (const ConfigError& e) {
      std::string msg = e.what();
      CHECK(msg.find("must be a bare {{field}} marker") != std::string::npos);
    }
  }
}

TEST_CASE("build_templated_param_plan: rejects non-templatable param") {
  try {
    build_templated_param_plan("name", schema_with("k", "string", false),
                               make_params({{"k", Variant("{{x}}")}}));
    FAIL("expected ConfigError");
  } catch (const ConfigError& e) {
    std::string msg = e.what();
    CHECK(msg.find("param \"k\" is not declared templatable") != std::string::npos);
  }
}

TEST_CASE("build_templated_param_plan: rejects unknown param") {
  try {
    build_templated_param_plan("name", schema_with("k", "string", true),
                               make_params({{"missing", Variant("{{x}}")}}));
    FAIL("expected ConfigError");
  } catch (const ConfigError& e) {
    std::string msg = e.what();
    CHECK(msg.find("param \"missing\" is not declared in schema") != std::string::npos);
  }
}

TEST_CASE("build_templated_param_plan: rejects non-scalar type") {
  try {
    build_templated_param_plan("name", schema_with("k", "string_list", true),
                               make_params({{"k", Variant("{{x}}")}}));
    FAIL("expected ConfigError");
  } catch (const ConfigError& e) {
    std::string msg = e.what();
    CHECK(msg.find("does not support templating") != std::string::npos);
  }
}

TEST_CASE("build_templated_param_plan: all six scalar types accepted") {
  for (const char* typ : {"string", "int", "int64", "float", "float64", "bool"}) {
    build_templated_param_plan("name", schema_with("k", typ, true), make_params({{"k", Variant("{{x}}")}}));
  }
}

TEST_CASE("resolve_templated_params: string binds field") {
  std::vector<TemplatedParam> plan;
  plan.push_back(TemplatedParam{"k", "string", "id"});
  InputHarness h({{"id", Variant(std::string("42"))}});
  auto got = resolve_templated_params("op", plan, h.frame);
  REQUIRE(got.count("k") == 1);
  CHECK(got.at("k").as_string() == "42");
}

// Pins the cross-runtime stringify contract: a numeric-valued source
// field bound to a string-typed templatable param must serialize via
// operators::go_format_g (5.0 -> "5"), mirroring Go fmt.Sprint(5.0).
// Without this pin the Redis key would diverge across runtimes when
// the template source is float-typed.
TEST_CASE("resolve_templated_params: float source + string target matches go_format_g") {
  std::vector<TemplatedParam> plan;
  plan.push_back(TemplatedParam{"k", "string", "x"});
  InputHarness h({{"x", Variant(5.0)}});
  auto got = resolve_templated_params("op", plan, h.frame);
  REQUIRE(got.count("k") == 1);
  CHECK(got.at("k").as_string() == "5");
}

TEST_CASE("resolve_templated_params: int coercion") {
  std::vector<TemplatedParam> plan;
  plan.push_back(TemplatedParam{"k", "int64", "n"});
  InputHarness h({{"n", Variant(7.0)}});
  auto got = resolve_templated_params("op", plan, h.frame);
  REQUIRE(got.count("k") == 1);
  CHECK(got.at("k").as_number() == 7.0);
}

TEST_CASE("resolve_templated_params: bool coercion") {
  std::vector<TemplatedParam> plan;
  plan.push_back(TemplatedParam{"k", "bool", "b"});
  InputHarness h({{"b", Variant(true)}});
  auto got = resolve_templated_params("op", plan, h.frame);
  REQUIRE(got.count("k") == 1);
  CHECK(got.at("k").as_bool() == true);
}

TEST_CASE("resolve_templated_params: missing field error byte-exact") {
  std::vector<TemplatedParam> plan;
  plan.push_back(TemplatedParam{"k", "string", "absent"});
  InputHarness h({});
  try {
    resolve_templated_params("op", plan, h.frame);
    FAIL("expected ExecutionError");
  } catch (const ExecutionError& e) {
    CHECK(std::string(e.what()) ==
          "templated param \"k\" references common field \"absent\" which is missing");
  }
}

TEST_CASE("resolve_templated_params: coerce-failure error byte-exact") {
  std::vector<TemplatedParam> plan;
  plan.push_back(TemplatedParam{"k", "int64", "x"});
  InputHarness h({{"x", Variant(std::string("not-a-number"))}});
  try {
    resolve_templated_params("op", plan, h.frame);
    FAIL("expected ExecutionError");
  } catch (const ExecutionError& e) {
    CHECK(std::string(e.what()) == "templated param \"k\" cannot coerce \"not-a-number\" to int64");
  }
}

TEST_CASE("resolve_templated_params: empty plan returns empty map") {
  InputHarness h({});
  auto got = resolve_templated_params("op", {}, h.frame);
  CHECK(got.empty());
}
