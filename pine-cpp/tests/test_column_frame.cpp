#include <doctest/doctest.h>

#include "pine/column_frame.hpp"

#include <algorithm>

using namespace pine;

namespace {

ColumnFrame make_frame() {
    std::vector<std::map<std::string, JsonValue>> items;
    items.push_back({{"id", JsonValue(1.0)}, {"score", JsonValue(10.0)}});
    items.push_back({{"id", JsonValue(2.0)}, {"score", JsonValue(20.0)}});
    items.push_back({{"id", JsonValue(3.0)}, {"score", JsonValue(30.0)}});
    return ColumnFrame({{"region", JsonValue(std::string("us"))}}, std::move(items));
}

}  // namespace

TEST_CASE("ColumnFrame: construction populates typed columns") {
    auto frame = make_frame();
    CHECK(frame.item_count() == 3);
    CHECK(frame.common("region").as_string() == "us");
    CHECK(frame.item(0, "id").as_number() == 1.0);
    CHECK(frame.item(2, "score").as_number() == 30.0);
    auto fields = frame.item_fields();
    CHECK(std::find(fields.begin(), fields.end(), "id") != fields.end());
    CHECK(std::find(fields.begin(), fields.end(), "score") != fields.end());
}

TEST_CASE("ColumnFrame: missing field returns null JsonValue") {
    auto frame = make_frame();
    CHECK(frame.item(0, "missing").is_null());
    CHECK(frame.common("nope").is_null());
}

TEST_CASE("ColumnFrame: apply_output common writes") {
    auto frame = make_frame();
    OperatorOutput out;
    out.set_common("region", JsonValue(std::string("eu")));
    out.set_common("new_field", JsonValue(42.0));
    frame.apply_output(out, "op", false);
    CHECK(frame.common("region").as_string() == "eu");
    CHECK(frame.common("new_field").as_number() == 42.0);
}

TEST_CASE("ColumnFrame: apply_output item writes (existing typed column)") {
    auto frame = make_frame();
    OperatorOutput out;
    out.set_item(0, "score", JsonValue(99.0));
    out.set_item(2, "score", JsonValue(77.0));
    frame.apply_output(out, "op", false);
    CHECK(frame.item(0, "score").as_number() == 99.0);
    CHECK(frame.item(1, "score").as_number() == 20.0);
    CHECK(frame.item(2, "score").as_number() == 77.0);
}

TEST_CASE("ColumnFrame: apply_output creates new column on first write") {
    auto frame = make_frame();
    OperatorOutput out;
    out.set_item(1, "tag", JsonValue(std::string("x")));
    frame.apply_output(out, "op", false);
    CHECK(frame.item(0, "tag").is_null());
    CHECK(frame.item(1, "tag").as_string() == "x");
    CHECK(frame.item(2, "tag").is_null());
}

TEST_CASE("ColumnFrame: apply_output type-mismatch promotes column to Json") {
    auto frame = make_frame();
    OperatorOutput out;
    // 'score' is currently typed (Int64/Double); writing a string forces promotion.
    out.set_item(1, "score", JsonValue(std::string("not-a-number")));
    frame.apply_output(out, "op", false);
    CHECK(frame.item(0, "score").as_number() == 10.0);
    CHECK(frame.item(1, "score").as_string() == "not-a-number");
    CHECK(frame.item(2, "score").as_number() == 30.0);
}

TEST_CASE("ColumnFrame: apply_output removes rows preserves remaining fields") {
    auto frame = make_frame();
    OperatorOutput out;
    out.remove_item(1);
    frame.apply_output(out, "op", false);
    CHECK(frame.item_count() == 2);
    CHECK(frame.item(0, "id").as_number() == 1.0);
    CHECK(frame.item(1, "id").as_number() == 3.0);
    CHECK(frame.item(1, "score").as_number() == 30.0);
}

TEST_CASE("ColumnFrame: apply_output reorders items") {
    auto frame = make_frame();
    OperatorOutput out;
    out.set_item_order({2, 0, 1});
    frame.apply_output(out, "op", false);
    CHECK(frame.item(0, "id").as_number() == 3.0);
    CHECK(frame.item(1, "id").as_number() == 1.0);
    CHECK(frame.item(2, "id").as_number() == 2.0);
}

TEST_CASE("ColumnFrame: apply_output adds items, recall stamps _source") {
    auto frame = make_frame();
    OperatorOutput out;
    out.add_item({{"id", JsonValue(99.0)}, {"score", JsonValue(50.0)}});
    out.add_item({{"id", JsonValue(100.0)}});
    frame.apply_output(out, "recall_op", true);
    CHECK(frame.item_count() == 5);
    CHECK(frame.item(3, "id").as_number() == 99.0);
    CHECK(frame.item(3, "_source").as_string() == "recall_op");
    CHECK(frame.item(4, "id").as_number() == 100.0);
    CHECK(frame.item(4, "score").is_null());
    CHECK(frame.item(0, "_source").is_null());  // not added by recall, no stamp
}

TEST_CASE("ColumnFrame: apply_output runs 5 stages in order (writes -> removes -> reorder -> additions)") {
    auto frame = make_frame();
    OperatorOutput out;
    out.set_item(0, "score", JsonValue(100.0));  // stage 2
    out.remove_item(2);                          // stage 3 (item index in original numbering)
    // After stage 3 we have 2 rows. Reorder must reference those 2.
    out.set_item_order({1, 0});                  // stage 4
    out.add_item({{"id", JsonValue(99.0)}});     // stage 5
    frame.apply_output(out, "op", false);
    CHECK(frame.item_count() == 3);
    // After writes: row0.score=100, row1.score=20, row2.score=30
    // After remove [2]: rows are (id=1,score=100), (id=2,score=20)
    // After reorder [1,0]: rows are (id=2,score=20), (id=1,score=100)
    // After add: (id=2,score=20), (id=1,score=100), (id=99)
    CHECK(frame.item(0, "id").as_number() == 2.0);
    CHECK(frame.item(0, "score").as_number() == 20.0);
    CHECK(frame.item(1, "id").as_number() == 1.0);
    CHECK(frame.item(1, "score").as_number() == 100.0);
    CHECK(frame.item(2, "id").as_number() == 99.0);
    CHECK(frame.item(2, "score").is_null());
}

TEST_CASE("ColumnFrame: to_result projects strict fields") {
    auto frame = make_frame();
    auto r = frame.to_result({"region"}, {"id", "score"});
    CHECK(r.common.at("region").as_string() == "us");
    REQUIRE(r.items.size() == 3);
    CHECK(r.items[0].at("id").as_number() == 1.0);
    CHECK(r.items[2].at("score").as_number() == 30.0);
}

TEST_CASE("ColumnFrame: warnings collected per operator with operator-name prefix") {
    // apply_output prepends `operator "<name>": ` to each warning, mirroring
    // pine-go pine.go:246 (`fmt.Errorf("operator %q: %w", w.Operator, w.Err)`).
    auto frame = make_frame();
    OperatorOutput out;
    out.set_warning("op A warning");
    frame.apply_output(out, "opA", false);

    OperatorOutput out2;
    out2.set_warning("op B warning");
    frame.apply_output(out2, "opB", false);

    auto w = frame.take_warnings();
    REQUIRE(w.size() == 2);
    CHECK(w[0] == "operator \"opA\": op A warning");
    CHECK(w[1] == "operator \"opB\": op B warning");
}

TEST_CASE("ColumnFrame: out-of-range item write raises ExecutionError") {
    auto frame = make_frame();
    OperatorOutput out;
    out.set_item(99, "score", JsonValue(1.0));
    CHECK_THROWS_AS(frame.apply_output(out, "op", false), ExecutionError);
}
