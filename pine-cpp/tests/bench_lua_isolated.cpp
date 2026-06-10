// Isolated Lua-vs-native operator benchmark for pine-cpp.
// Mirrors pine-go benchmarks/bench_isolated_test.go: construct the operator
// directly from the registry, loop Execute on a fixed OperatorInput, measure
// ns/op. Bypasses the engine/DAG/HTTP layers entirely so the LuaJIT-vs-native
// gap is not diluted by framework overhead (end-to-end numbers understate
// the gap by 50-70%; see llmdoc/memory/reflections/
// bench-lua-vs-go-performance.md).
//
// Build target: pine_lua_isolated_bench (cmake -DPINE_CPP_BUILD_TESTS=ON).
// Run manually when re-measuring:
//
//   cmake --build build-tests --target pine_lua_isolated_bench -j12
//   ./build-tests/pine_lua_isolated_bench
//
// Latest archived results:
// .code-review/sharedmutex-deep-dive/lua-vs-native-three-runtimes.md
#include "pine/column_frame.hpp"
#include "pine/operator.hpp"
#include "pine/operator_input.hpp"
#include "pine/pine.hpp"
#include "pine/row_frame.hpp"

#include <chrono>
#include <iomanip>
#include <iostream>
#include <string>
#include <vector>

using namespace pine;
using clk = std::chrono::steady_clock;

namespace {

struct Case {
  std::string name;
  std::string lua_script;   // for transform_by_lua
  std::string native_type;  // registry type_name of the native equivalent
  Variant native_params;    // params for native op
  std::vector<std::string> item_input;
  std::vector<std::string> item_output;
  std::function<std::vector<Variant::object_t>(int)> gen;
};

std::unique_ptr<Operator> build_op(const std::string& type_name, const std::string& op_name, Variant params,
                                   const std::vector<std::string>& item_input,
                                   const std::vector<std::string>& item_output) {
  const auto* entry = registry_entry(type_name);
  if (!entry) {
    throw std::runtime_error("operator not registered: " + type_name);
  }
  auto op = entry->factory();
  OperatorConfig cfg;
  cfg.name = op_name;
  cfg.type_name = type_name;
  cfg.metadata.item_input = item_input;
  cfg.metadata.item_output = item_output;
  cfg.params = std::move(params);
  op->init(cfg);
  return op;
}

double bench_ns_per_op(Operator& op, const Frame& frame, const InputFieldSpec& spec, int iters) {
  // warmup
  for (int i = 0; i < 50; ++i) {
    OperatorOutput out;
    OperatorInput input = build_operator_input(frame, "bench", spec);
    op.execute(input, out);
  }
  auto t0 = clk::now();
  for (int i = 0; i < iters; ++i) {
    OperatorOutput out;
    OperatorInput input = build_operator_input(frame, "bench", spec);
    op.execute(input, out);
  }
  auto t1 = clk::now();
  return double(std::chrono::duration_cast<std::chrono::nanoseconds>(t1 - t0).count()) / iters;
}

}  // namespace

int main() {
  std::vector<Case> cases;

  // L1 identity: return item_x. Native = transform_copy item_to_item.
  cases.push_back(Case{
      "L1_identity",
      "function f() return item_x end",
      "transform_copy",
      [] {
        Variant::object_t p;
        p["direction"] = Variant(std::string("item_to_item"));
        return Variant(std::move(p));
      }(),
      {"item_x"},
      {"item_y"},
      [](int n) {
        std::vector<Variant::object_t> items;
        items.reserve(n);
        for (int i = 0; i < n; ++i) {
          Variant::object_t row;
          row["item_x"] = Variant(double(i));
          items.push_back(std::move(row));
        }
        return items;
      },
  });

  // L2 arithmetic: price * 0.85 + 10. Native = transform_normalize (closest
  // built-in arithmetic op: min_max over a field — same per-item float math
  // shape: read field, arithmetic, write field).
  cases.push_back(Case{
      "L2_arithmetic",
      "function f() return item_price * 0.85 + 10.0 end",
      "transform_normalize",
      [] {
        Variant::object_t p;
        p["method"] = Variant(std::string("min_max"));
        return Variant(std::move(p));
      }(),
      {"item_price"},
      {"item_result"},
      [](int n) {
        std::vector<Variant::object_t> items;
        items.reserve(n);
        for (int i = 0; i < n; ++i) {
          Variant::object_t row;
          row["item_price"] = Variant(double(100 + i));
          items.push_back(std::move(row));
        }
        return items;
      },
  });

  // L5 iterative: Horner 5th-degree polynomial in Lua. Native comparison =
  // transform_normalize again (no built-in polynomial op); the interesting
  // number here is the Lua column, native column is a floor reference.
  cases.push_back(Case{
      "L5_iterative",
      "function f()\n"
      "  local x = item_price\n"
      "  local acc = 1.0\n"
      "  for i = 1, 5 do acc = acc * x + i end\n"
      "  return acc\n"
      "end",
      "transform_normalize",
      [] {
        Variant::object_t p;
        p["method"] = Variant(std::string("min_max"));
        return Variant(std::move(p));
      }(),
      {"item_price"},
      {"item_result"},
      [](int n) {
        std::vector<Variant::object_t> items;
        items.reserve(n);
        for (int i = 0; i < n; ++i) {
          Variant::object_t row;
          row["item_price"] = Variant(double(100 + i));
          items.push_back(std::move(row));
        }
        return items;
      },
  });

  std::cout << std::left << std::setw(16) << "case" << std::setw(7) << "items" << std::setw(14)
            << "native ns/op" << std::setw(14) << "lua ns/op" << "lua/native\n";
  std::cout << std::string(64, '-') << "\n";

  for (const auto& tc : cases) {
    for (int n : {100, 1000}) {
      auto items = tc.gen(n);
      RowFrame frame({}, items);

      // Native op
      auto native = build_op(tc.native_type, "bench_native", tc.native_params, tc.item_input, tc.item_output);
      OperatorConfig spec_cfg;
      spec_cfg.metadata.item_input = tc.item_input;
      InputFieldSpec spec = compute_input_field_spec(spec_cfg);

      int iters = n >= 1000 ? 2000 : 10000;
      double native_ns = bench_ns_per_op(*native, frame, spec, iters);

      // Lua op
      Variant::object_t lua_params;
      lua_params["lua_script"] = Variant(tc.lua_script);
      lua_params["function_for_item"] = Variant(std::string("f"));
      lua_params["function_for_common"] = Variant(std::string(""));
      auto lua = build_op("transform_by_lua", "bench_lua", Variant(std::move(lua_params)), tc.item_input,
                          tc.item_output);
      double lua_ns = bench_ns_per_op(*lua, frame, spec, iters);

      std::cout << std::left << std::setw(16) << tc.name << std::setw(7) << n << std::setw(14) << std::fixed
                << std::setprecision(0) << native_ns << std::setw(14) << lua_ns << std::setprecision(2)
                << (lua_ns / native_ns) << "x\n";
    }
  }
}
