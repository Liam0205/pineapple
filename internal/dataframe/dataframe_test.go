package dataframe

import (
	"testing"

	"github.com/Liam0205/pineapple/internal/types"
)

func TestNewFrameIsolation(t *testing.T) {
	common := map[string]any{"user_id": "u1"}
	items := []map[string]any{{"item_id": int64(1)}}
	f := New(common, items)

	// Mutate originals — frame should be unaffected
	common["user_id"] = "mutated"
	items[0]["item_id"] = int64(999)

	if f.Common("user_id") != "u1" {
		t.Error("frame common was mutated by caller")
	}
	if f.Item(0, "item_id") != int64(1) {
		t.Error("frame item was mutated by caller")
	}
}

func TestFrameReadAccessors(t *testing.T) {
	f := New(
		map[string]any{"age": int64(25)},
		[]map[string]any{
			{"price": 99.0},
			{"price": 50.0},
		},
	)
	if f.Common("age") != int64(25) {
		t.Errorf("Common(age) = %v", f.Common("age"))
	}
	if f.Common("missing") != nil {
		t.Error("Common(missing) should be nil")
	}
	if f.ItemCount() != 2 {
		t.Errorf("ItemCount = %d", f.ItemCount())
	}
	if f.Item(0, "price") != 99.0 {
		t.Errorf("Item(0, price) = %v", f.Item(0, "price"))
	}
	if f.Item(5, "price") != nil {
		t.Error("out-of-range Item should be nil")
	}
}

func TestBuildInputWithDefaults(t *testing.T) {
	f := New(
		map[string]any{"age": int64(25)},
		[]map[string]any{
			{"price": 99.0},
			{"price": nil}, // explicit nil
		},
	)

	in := BuildInput(f,
		[]string{"age", "missing_common"},
		[]string{"price", "missing_item"},
		map[string]any{"missing_common": "default_c"},
		map[string]any{"price": 0.0, "missing_item": "default_i"},
	)

	if in.Common("age") != int64(25) {
		t.Errorf("age = %v", in.Common("age"))
	}
	if in.Common("missing_common") != "default_c" {
		t.Errorf("missing_common = %v, want default_c", in.Common("missing_common"))
	}
	// item 0: price=99.0, not defaulted
	if in.Item(0, "price") != 99.0 {
		t.Errorf("item 0 price = %v", in.Item(0, "price"))
	}
	// item 1: price=nil -> defaulted to 0.0
	if in.Item(1, "price") != 0.0 {
		t.Errorf("item 1 price = %v, want 0.0", in.Item(1, "price"))
	}
	// missing_item defaulted
	if in.Item(0, "missing_item") != "default_i" {
		t.Errorf("item 0 missing_item = %v", in.Item(0, "missing_item"))
	}
}

func TestApplyOutputCommonWrites(t *testing.T) {
	f := New(map[string]any{"x": int64(1)}, nil)
	out := types.NewOperatorOutput()
	out.SetCommon("x", int64(2))
	out.SetCommon("y", "new")

	if err := ApplyOutput(f, out, "op", false); err != nil {
		t.Fatal(err)
	}
	if f.Common("x") != int64(2) {
		t.Errorf("x = %v", f.Common("x"))
	}
	if f.Common("y") != "new" {
		t.Errorf("y = %v", f.Common("y"))
	}
}

func TestApplyOutputItemWrites(t *testing.T) {
	f := New(nil, []map[string]any{
		{"score": 1.0},
		{"score": 2.0},
	})
	out := types.NewOperatorOutput()
	out.SetItem(0, "score", 10.0)
	out.SetItem(1, "rank", int64(1))

	if err := ApplyOutput(f, out, "op", false); err != nil {
		t.Fatal(err)
	}
	if f.Item(0, "score") != 10.0 {
		t.Errorf("item 0 score = %v", f.Item(0, "score"))
	}
	if f.Item(1, "rank") != int64(1) {
		t.Errorf("item 1 rank = %v", f.Item(1, "rank"))
	}
}

func TestApplyOutputItemWriteOutOfRange(t *testing.T) {
	f := New(nil, []map[string]any{{"a": 1}})
	out := types.NewOperatorOutput()
	out.SetItem(5, "b", 2)

	if err := ApplyOutput(f, out, "op", false); err == nil {
		t.Error("expected error for out-of-range SetItem")
	}
}

func TestApplyOutputRemoveItems(t *testing.T) {
	f := New(nil, []map[string]any{
		{"id": int64(0)},
		{"id": int64(1)},
		{"id": int64(2)},
		{"id": int64(3)},
	})
	out := types.NewOperatorOutput()
	out.RemoveItem(1)
	out.RemoveItem(3)

	if err := ApplyOutput(f, out, "op", false); err != nil {
		t.Fatal(err)
	}
	if f.ItemCount() != 2 {
		t.Fatalf("ItemCount = %d, want 2", f.ItemCount())
	}
	if f.Item(0, "id") != int64(0) {
		t.Errorf("item 0 id = %v", f.Item(0, "id"))
	}
	if f.Item(1, "id") != int64(2) {
		t.Errorf("item 1 id = %v", f.Item(1, "id"))
	}
}

func TestApplyOutputReorder(t *testing.T) {
	f := New(nil, []map[string]any{
		{"id": "a"},
		{"id": "b"},
		{"id": "c"},
	})
	out := types.NewOperatorOutput()
	out.SetItemOrder([]int{2, 0, 1})

	if err := ApplyOutput(f, out, "op", false); err != nil {
		t.Fatal(err)
	}
	want := []string{"c", "a", "b"}
	for i, w := range want {
		if f.Item(i, "id") != w {
			t.Errorf("item %d id = %v, want %s", i, f.Item(i, "id"), w)
		}
	}
}

func TestApplyOutputReorderLengthMismatch(t *testing.T) {
	f := New(nil, []map[string]any{{"a": 1}, {"b": 2}})
	out := types.NewOperatorOutput()
	out.SetItemOrder([]int{0})

	if err := ApplyOutput(f, out, "op", false); err == nil {
		t.Error("expected error for length mismatch")
	}
}

func TestApplyOutputReorderOutOfRange(t *testing.T) {
	f := New(nil, []map[string]any{{"a": 1}, {"b": 2}})
	out := types.NewOperatorOutput()
	out.SetItemOrder([]int{0, 5})

	if err := ApplyOutput(f, out, "op", false); err == nil {
		t.Error("expected error for out-of-range reorder index")
	}
}

func TestApplyOutputAddItems(t *testing.T) {
	f := New(nil, []map[string]any{{"id": "existing"}})
	out := types.NewOperatorOutput()
	out.AddItem(map[string]any{"id": "new1"})
	out.AddItem(map[string]any{"id": "new2"})

	if err := ApplyOutput(f, out, "op", false); err != nil {
		t.Fatal(err)
	}
	if f.ItemCount() != 3 {
		t.Fatalf("ItemCount = %d, want 3", f.ItemCount())
	}
	if f.Item(1, "id") != "new1" {
		t.Errorf("item 1 = %v", f.Item(1, "id"))
	}
}

func TestApplyOutputRecallInjectsSource(t *testing.T) {
	f := New(nil, nil)
	out := types.NewOperatorOutput()
	out.AddItem(map[string]any{"item_id": int64(100)})
	out.AddItem(map[string]any{"item_id": int64(200)})

	if err := ApplyOutput(f, out, "recall_main_idx", true); err != nil {
		t.Fatal(err)
	}
	if f.ItemCount() != 2 {
		t.Fatalf("ItemCount = %d", f.ItemCount())
	}
	for i := 0; i < f.ItemCount(); i++ {
		if f.Item(i, "_source") != "recall_main_idx" {
			t.Errorf("item %d _source = %v", i, f.Item(i, "_source"))
		}
	}
}

func TestApplyOutputNonRecallNoSource(t *testing.T) {
	f := New(nil, nil)
	out := types.NewOperatorOutput()
	out.AddItem(map[string]any{"item_id": int64(100)})

	if err := ApplyOutput(f, out, "merge_op", false); err != nil {
		t.Fatal(err)
	}
	if f.Item(0, "_source") != nil {
		t.Errorf("non-recall should not inject _source, got %v", f.Item(0, "_source"))
	}
}

func TestApplyOutputCombinedRemoveThenAdd(t *testing.T) {
	f := New(nil, []map[string]any{
		{"id": int64(1)},
		{"id": int64(2)},
		{"id": int64(3)},
	})
	out := types.NewOperatorOutput()
	out.SetItem(0, "score", 10.0) // write to item 0
	out.RemoveItem(1)              // remove item 1
	out.AddItem(map[string]any{"id": int64(4)})

	if err := ApplyOutput(f, out, "op", false); err != nil {
		t.Fatal(err)
	}
	// After: item writes applied first, then removal of index 1, then addition
	// Surviving: [0: {id:1, score:10}, 2: {id:3}], then add {id:4}
	if f.ItemCount() != 3 {
		t.Fatalf("ItemCount = %d, want 3", f.ItemCount())
	}
	if f.Item(0, "score") != 10.0 {
		t.Errorf("item 0 score = %v", f.Item(0, "score"))
	}
	if f.Item(0, "id") != int64(1) {
		t.Errorf("item 0 id = %v", f.Item(0, "id"))
	}
	if f.Item(1, "id") != int64(3) {
		t.Errorf("item 1 id = %v", f.Item(1, "id"))
	}
	if f.Item(2, "id") != int64(4) {
		t.Errorf("item 2 id = %v", f.Item(2, "id"))
	}
}

func TestToResultIsolation(t *testing.T) {
	f := New(
		map[string]any{"k": "v"},
		[]map[string]any{{"a": int64(1)}},
	)
	result := ToResult(f, []string{"k"}, []string{"a"})

	// Mutate result — frame should be unaffected
	result.Common["k"] = "mutated"
	result.Items[0]["a"] = int64(999)

	if f.Common("k") != "v" {
		t.Error("frame common was mutated via result")
	}
	if f.Item(0, "a") != int64(1) {
		t.Error("frame item was mutated via result")
	}
}

func TestToResultProjection(t *testing.T) {
	f := New(
		map[string]any{"a": 1, "b": 2, "c": 3},
		[]map[string]any{
			{"x": 10, "y": 20, "z": 30},
			{"x": 40, "y": 50, "z": 60},
		},
	)

	// Project to subset
	result := ToResult(f, []string{"a", "c"}, []string{"x", "z"})

	if len(result.Common) != 2 {
		t.Errorf("common len = %d, want 2", len(result.Common))
	}
	if _, ok := result.Common["b"]; ok {
		t.Error("common should not contain 'b'")
	}
	for _, item := range result.Items {
		if len(item) != 2 {
			t.Errorf("item len = %d, want 2", len(item))
		}
		if _, ok := item["y"]; ok {
			t.Error("item should not contain 'y'")
		}
	}

	// Empty/nil slices → return empty maps (no fields projected)
	full := ToResult(f, nil, nil)
	if len(full.Common) != 0 {
		t.Errorf("full common len = %d, want 0", len(full.Common))
	}
	if len(full.Items[0]) != 0 {
		t.Errorf("full item len = %d, want 0", len(full.Items[0]))
	}
}

func TestApplyOutputTypeValidation(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		wantErr bool
	}{
		{"nil", nil, false},
		{"bool", true, false},
		{"int", 42, false},
		{"int64", int64(42), false},
		{"float64", 3.14, false},
		{"string", "hello", false},
		{"slice_any", []any{1, 2}, false},
		{"map_string_any", map[string]any{"k": "v"}, false},
		{"channel", make(chan int), true},
		{"func", func() {}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := New(map[string]any{}, nil)
			out := types.NewOperatorOutput()
			out.SetCommon("field", tt.value)

			err := ApplyOutput(f, out, "op", false)
			if tt.wantErr && err == nil {
				t.Errorf("expected error for type %T", tt.value)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for type %T: %v", tt.value, err)
			}
		})
	}
}

func TestApplyOutputItemTypeValidation(t *testing.T) {
	f := New(nil, []map[string]any{{"x": 1}})
	out := types.NewOperatorOutput()
	out.SetItem(0, "bad", make(chan string))

	err := ApplyOutput(f, out, "op", false)
	if err == nil {
		t.Error("expected error for channel type in item write")
	}
}

func TestApplyOutputAddItemTypeValidation(t *testing.T) {
	f := New(nil, nil)
	out := types.NewOperatorOutput()
	out.AddItem(map[string]any{"bad": func() {}})

	err := ApplyOutput(f, out, "op", false)
	if err == nil {
		t.Error("expected error for func type in added item")
	}
}
