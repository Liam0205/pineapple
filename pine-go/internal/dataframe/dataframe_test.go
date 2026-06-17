package dataframe

import (
	"reflect"
	"sync"
	"testing"

	"github.com/Liam0205/pineapple/pine-go/internal/config"
	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

var testModes = []struct {
	name string
	mode StorageMode
}{
	{"row", StorageModeRow},
	{"column", StorageModeColumn},
}

func newTestFrame(mode StorageMode, common map[string]any, items []map[string]any) Frame {
	return NewFrame(mode, common, items)
}

func TestNewFrameIsolation(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			common := map[string]any{"user_id": "u1"}
			items := []map[string]any{{"item_id": int64(1)}}
			f := newTestFrame(tm.mode, common, items)

			common["user_id"] = "mutated"
			items[0]["item_id"] = int64(999)

			if f.Common("user_id") != "u1" {
				t.Error("frame common was mutated by caller")
			}
			if f.Item(0, "item_id") != int64(1) {
				t.Error("frame item was mutated by caller")
			}
		})
	}
}

func TestFrameReadAccessors(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode,
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
		})
	}
}

func TestBuildInputWithDefaults(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode,
				map[string]any{"age": int64(25)},
				[]map[string]any{
					{"price": 99.0},
					{"price": nil},
				},
			)

			spec := &config.InputFieldSpec{
				StrictCommon:    []string{"age"},
				DefaultedCommon: []config.DefaultedField{{Name: "missing_common", Default: "default_c"}},
				StrictItem:      nil,
				DefaultedItem: []config.DefaultedField{
					{Name: "price", Default: 0.0},
					{Name: "missing_item", Default: "default_i"},
				},
			}
			in, err := BuildInput(f, "test_op", spec)
			if err != nil {
				t.Fatal(err)
			}

			if in.Common("age") != int64(25) {
				t.Errorf("age = %v", in.Common("age"))
			}
			if in.Common("missing_common") != "default_c" {
				t.Errorf("missing_common = %v, want default_c", in.Common("missing_common"))
			}
			if in.Item(0, "price") != 99.0 {
				t.Errorf("item 0 price = %v", in.Item(0, "price"))
			}
			if in.Item(1, "price") != 0.0 {
				t.Errorf("item 1 price = %v, want 0.0", in.Item(1, "price"))
			}
			if in.Item(0, "missing_item") != "default_i" {
				t.Errorf("item 0 missing_item = %v", in.Item(0, "missing_item"))
			}
		})
	}
}

func TestBuildInputSparseItemPresence(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, nil, []map[string]any{
				{"a": nil},
				{"b": int64(2)},
			})

			// With the new API, fields without defaults are "strict" and error on nil.
			// To test sparse presence, use DefaultedItem with nil defaults so both
			// present-nil and missing produce nil without error.
			spec := &config.InputFieldSpec{
				DefaultedItem: []config.DefaultedField{
					{Name: "a", Default: nil},
					{Name: "b", Default: nil},
				},
			}
			in, err := BuildInput(f, "test_op", spec)
			if err != nil {
				t.Fatal(err)
			}

			// With defaulted fields, all keys appear in output regardless of presence
			keys0 := toSet(in.ItemKeys(0))
			if !keys0["a"] {
				t.Error("item 0: expected key 'a' present")
			}
			if !keys0["b"] {
				t.Error("item 0: expected key 'b' present (defaulted)")
			}
			if in.Item(0, "a") != nil {
				t.Errorf("item 0 a = %v, want nil", in.Item(0, "a"))
			}
			if in.Item(0, "b") != nil {
				t.Errorf("item 0 b = %v, want nil (default)", in.Item(0, "b"))
			}

			keys1 := toSet(in.ItemKeys(1))
			if !keys1["a"] {
				t.Error("item 1: expected key 'a' present (defaulted)")
			}
			if !keys1["b"] {
				t.Error("item 1: expected key 'b' present")
			}
			if in.Item(1, "b") != int64(2) {
				t.Errorf("item 1 b = %v, want 2", in.Item(1, "b"))
			}
		})
	}
}

func TestBuildInputSparseItemWithDefaults(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, nil, []map[string]any{
				{"a": nil},
				{"b": int64(2)},
			})

			spec := &config.InputFieldSpec{
				DefaultedItem: []config.DefaultedField{
					{Name: "a", Default: int64(0)},
					{Name: "b", Default: int64(0)},
				},
			}
			in, err := BuildInput(f, "test_op", spec)
			if err != nil {
				t.Fatal(err)
			}

			if in.Item(0, "a") != int64(0) {
				t.Errorf("item 0 a = %v, want 0 (default on present-nil)", in.Item(0, "a"))
			}
			if in.Item(0, "b") != int64(0) {
				t.Errorf("item 0 b = %v, want 0 (default on missing)", in.Item(0, "b"))
			}
			if in.Item(1, "a") != int64(0) {
				t.Errorf("item 1 a = %v, want 0 (default on missing)", in.Item(1, "a"))
			}
			if in.Item(1, "b") != int64(2) {
				t.Errorf("item 1 b = %v, want 2", in.Item(1, "b"))
			}

			for i := 0; i < 2; i++ {
				keys := toSet(in.ItemKeys(i))
				if !keys["a"] || !keys["b"] {
					t.Errorf("item %d: expected both keys present when defaults exist, got %v", i, in.ItemKeys(i))
				}
			}
		})
	}
}

func TestBuildInputSparseCommon(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, map[string]any{"x": nil}, nil)

			// With the new API, strict fields error on nil. Use DefaultedCommon
			// with nil defaults so nil/missing values produce nil output without error.
			spec := &config.InputFieldSpec{
				DefaultedCommon: []config.DefaultedField{
					{Name: "x", Default: nil},
					{Name: "y", Default: nil},
				},
			}
			in, err := BuildInput(f, "test_op", spec)
			if err != nil {
				t.Fatal(err)
			}

			keys := toSet(in.CommonKeys())
			// Both defaulted fields appear in the output regardless of frame presence
			if !keys["x"] {
				t.Error("expected common key 'x' present (defaulted)")
			}
			if !keys["y"] {
				t.Error("expected common key 'y' present (defaulted with nil)")
			}
		})
	}
}

func toSet(keys []string) map[string]bool {
	s := make(map[string]bool, len(keys))
	for _, k := range keys {
		s[k] = true
	}
	return s
}

func TestApplyOutputCommonWrites(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, map[string]any{"x": int64(1)}, nil)
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
		})
	}
}

func TestApplyOutputItemWrites(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, nil, []map[string]any{
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
		})
	}
}

func TestApplyOutputItemWriteOutOfRange(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, nil, []map[string]any{{"a": 1}})
			out := types.NewOperatorOutput()
			out.SetItem(5, "b", 2)

			if err := ApplyOutput(f, out, "op", false); err == nil {
				t.Error("expected error for out-of-range SetItem")
			}
		})
	}
}

func TestApplyOutputRemoveItems(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, nil, []map[string]any{
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
		})
	}
}

func TestApplyOutputRemoveItemOutOfRange(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, nil, []map[string]any{{"id": int64(1)}})
			out := types.NewOperatorOutput()
			out.RemoveItem(2)

			if err := ApplyOutput(f, out, "op", false); err == nil {
				t.Error("expected error for out-of-range RemoveItem")
			}
			if f.ItemCount() != 1 {
				t.Errorf("ItemCount changed after failed removal: got %d, want 1", f.ItemCount())
			}
		})
	}
}

func TestApplyOutputReorder(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, nil, []map[string]any{
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
		})
	}
}

func TestApplyOutputReorderLengthMismatch(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, nil, []map[string]any{{"a": 1}, {"b": 2}})
			out := types.NewOperatorOutput()
			out.SetItemOrder([]int{0})

			if err := ApplyOutput(f, out, "op", false); err == nil {
				t.Error("expected error for length mismatch")
			}
		})
	}
}

func TestApplyOutputReorderOutOfRange(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, nil, []map[string]any{{"a": 1}, {"b": 2}})
			out := types.NewOperatorOutput()
			out.SetItemOrder([]int{0, 5})

			if err := ApplyOutput(f, out, "op", false); err == nil {
				t.Error("expected error for out-of-range reorder index")
			}
		})
	}
}

func TestApplyOutputReorderOutOfRangeWithoutItemFields(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, nil, []map[string]any{{}, {}})
			out := types.NewOperatorOutput()
			out.SetItemOrder([]int{0, -1})

			if err := ApplyOutput(f, out, "op", false); err == nil {
				t.Error("expected error for out-of-range reorder index")
			}
		})
	}
}

func TestApplyOutputAddItems(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, nil, []map[string]any{{"id": "existing"}})
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
		})
	}
}

func TestApplyOutputRecallInjectsSource(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, nil, nil)
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
		})
	}
}

func TestApplyOutputNonRecallNoSource(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, nil, nil)
			out := types.NewOperatorOutput()
			out.AddItem(map[string]any{"item_id": int64(100)})

			if err := ApplyOutput(f, out, "merge_op", false); err != nil {
				t.Fatal(err)
			}
			if f.Item(0, "_source") != nil {
				t.Errorf("non-recall should not inject _source, got %v", f.Item(0, "_source"))
			}
		})
	}
}

func TestApplyOutputCombinedRemoveThenAdd(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, nil, []map[string]any{
				{"id": int64(1)},
				{"id": int64(2)},
				{"id": int64(3)},
			})
			out := types.NewOperatorOutput()
			out.SetItem(0, "score", 10.0)
			out.RemoveItem(1)
			out.AddItem(map[string]any{"id": int64(4)})

			if err := ApplyOutput(f, out, "op", false); err != nil {
				t.Fatal(err)
			}
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
		})
	}
}

// TestApplyOutputCombinedRemoveThenReorder pins the boundary between
// stage 3 (remove) and stage 4 (reorder) in ApplyOutput. The reorder
// permutation must be sized to the *post-compact* row count, and its
// indices must address post-compact slots — feeding original indices
// would silently re-ingest removed rows. Existing coverage:
// TestApplyOutputRemoveItems and TestApplyOutputReorder each test one
// stage in isolation; the combination chain only surfaces in the
// 5-stage Java fiveStageOrdering test, with no Go counterpart.
func TestApplyOutputCombinedRemoveThenReorder(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, nil, []map[string]any{
				{"id": int64(0)},
				{"id": int64(1)},
				{"id": int64(2)},
				{"id": int64(3)},
				{"id": int64(4)},
			})
			out := types.NewOperatorOutput()
			out.RemoveItem(1) // post-compact rows: [0, 2, 3, 4]
			out.RemoveItem(3) // drops original id=3 → post-compact: [0, 2, 4]
			// Reorder must use the post-compact length (3) and the
			// post-compact slot indices. order = [2, 0, 1] means
			// new[i] = post[order[i]] → expected ids [4, 0, 2].
			out.SetItemOrder([]int{2, 0, 1})

			if err := ApplyOutput(f, out, "op", false); err != nil {
				t.Fatal(err)
			}
			if f.ItemCount() != 3 {
				t.Fatalf("ItemCount = %d, want 3", f.ItemCount())
			}
			expected := []int64{4, 0, 2}
			for i, want := range expected {
				if got := f.Item(i, "id"); got != want {
					t.Errorf("item[%d] id = %v, want %v", i, got, want)
				}
			}
		})
	}
}

func TestToResultIsolation(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode,
				map[string]any{"k": "v"},
				[]map[string]any{{"a": int64(1)}},
			)
			result := ToResult(f, []string{"k"}, []string{"a"})

			result.Common["k"] = "mutated"
			result.Items[0]["a"] = int64(999)

			if f.Common("k") != "v" {
				t.Error("frame common was mutated via result")
			}
			if f.Item(0, "a") != int64(1) {
				t.Error("frame item was mutated via result")
			}
		})
	}
}

func TestToResultProjection(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode,
				map[string]any{"a": 1, "b": 2, "c": 3},
				[]map[string]any{
					{"x": 10, "y": 20, "z": 30},
					{"x": 40, "y": 50, "z": 60},
				},
			)

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

			full := ToResult(f, nil, nil)
			if len(full.Common) != 0 {
				t.Errorf("full common len = %d, want 0", len(full.Common))
			}
			if len(full.Items[0]) != 0 {
				t.Errorf("full item len = %d, want 0", len(full.Items[0]))
			}
		})
	}
}

func TestToResultOmitsMissingSparseItemFields(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode,
				nil,
				[]map[string]any{
					{"a": nil},
					{"b": 2},
				},
			)

			result := ToResult(f, nil, []string{"a", "b"})

			if _, ok := result.Items[0]["a"]; !ok {
				t.Error("item 0 should retain explicitly present nil field a")
			}
			if _, ok := result.Items[0]["b"]; ok {
				t.Error("item 0 should omit missing field b")
			}
			if _, ok := result.Items[1]["a"]; ok {
				t.Error("item 1 should omit missing field a")
			}
		})
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

	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					f := newTestFrame(tm.mode, map[string]any{}, nil)
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
		})
	}
}

func TestApplyOutputItemTypeValidation(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, nil, []map[string]any{{"x": 1}})
			out := types.NewOperatorOutput()
			out.SetItem(0, "bad", make(chan string))

			err := ApplyOutput(f, out, "op", false)
			if err == nil {
				t.Error("expected error for channel type in item write")
			}
		})
	}
}

func TestApplyOutputAddItemTypeValidation(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, nil, nil)
			out := types.NewOperatorOutput()
			out.AddItem(map[string]any{"bad": func() {}})

			err := ApplyOutput(f, out, "op", false)
			if err == nil {
				t.Error("expected error for func type in added item")
			}
		})
	}
}

// --- Concurrency tests ---

func TestColumnFrameConcurrentBuildInput(t *testing.T) {
	items := make([]map[string]any, 100)
	for i := range items {
		items[i] = map[string]any{"a": i, "b": i * 10}
	}
	f := NewFrame(StorageModeColumn, map[string]any{"x": 1}, items)

	spec := &config.InputFieldSpec{
		StrictCommon: []string{"x"},
		StrictItem:   []string{"a", "b"},
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in, err := f.BuildInput("test_op", spec)
			if err != nil {
				t.Errorf("BuildInput error: %v", err)
				return
			}
			if in.ItemCount() != 100 {
				t.Errorf("expected 100 items, got %d", in.ItemCount())
			}
		}()
	}
	wg.Wait()
}

func TestColumnFrameConcurrentDisjointFieldWrites(t *testing.T) {
	items := make([]map[string]any, 50)
	for i := range items {
		items[i] = map[string]any{"a": 0, "b": 0}
	}
	f := NewFrame(StorageModeColumn, map[string]any{}, items)

	var wg sync.WaitGroup
	// Goroutine 1 writes field "a"
	wg.Add(1)
	go func() {
		defer wg.Done()
		out := types.NewOperatorOutput()
		for i := 0; i < 50; i++ {
			out.SetItem(i, "a", 100+i)
		}
		if err := f.ApplyOutput(out, "op_a", false); err != nil {
			t.Errorf("op_a: %v", err)
		}
	}()
	// Goroutine 2 writes field "b"
	wg.Add(1)
	go func() {
		defer wg.Done()
		out := types.NewOperatorOutput()
		for i := 0; i < 50; i++ {
			out.SetItem(i, "b", 200+i)
		}
		if err := f.ApplyOutput(out, "op_b", false); err != nil {
			t.Errorf("op_b: %v", err)
		}
	}()
	wg.Wait()

	for i := 0; i < 50; i++ {
		if f.Item(i, "a") != 100+i {
			t.Errorf("item %d field a: got %v, want %d", i, f.Item(i, "a"), 100+i)
		}
		if f.Item(i, "b") != 200+i {
			t.Errorf("item %d field b: got %v, want %d", i, f.Item(i, "b"), 200+i)
		}
	}
}

func TestColumnFrameConcurrentCommonAndItemAccess(t *testing.T) {
	items := make([]map[string]any, 20)
	for i := range items {
		items[i] = map[string]any{"v": i}
	}
	f := NewFrame(StorageModeColumn, map[string]any{"c": 0}, items)

	var wg sync.WaitGroup
	// Write common field
	wg.Add(1)
	go func() {
		defer wg.Done()
		out := types.NewOperatorOutput()
		out.SetCommon("c", 42)
		if err := f.ApplyOutput(out, "op_common", false); err != nil {
			t.Errorf("common write: %v", err)
		}
	}()
	// Read item fields concurrently
	wg.Add(1)
	go func() {
		defer wg.Done()
		spec := &config.InputFieldSpec{
			StrictItem: []string{"v"},
		}
		_, _ = f.BuildInput("test_op", spec)
	}()
	wg.Wait()
}

func TestColumnFrameLazyColumnCreation(t *testing.T) {
	items := make([]map[string]any, 10)
	for i := range items {
		items[i] = map[string]any{"x": i}
	}
	f := NewFrame(StorageModeColumn, map[string]any{}, items)

	var wg sync.WaitGroup
	// Two goroutines create different new columns
	wg.Add(1)
	go func() {
		defer wg.Done()
		out := types.NewOperatorOutput()
		for i := 0; i < 10; i++ {
			out.SetItem(i, "new_a", i*2)
		}
		if err := f.ApplyOutput(out, "op_a", false); err != nil {
			t.Errorf("new_a: %v", err)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		out := types.NewOperatorOutput()
		for i := 0; i < 10; i++ {
			out.SetItem(i, "new_b", i*3)
		}
		if err := f.ApplyOutput(out, "op_b", false); err != nil {
			t.Errorf("new_b: %v", err)
		}
	}()
	wg.Wait()

	for i := 0; i < 10; i++ {
		if f.Item(i, "new_a") != i*2 {
			t.Errorf("item %d new_a: got %v, want %d", i, f.Item(i, "new_a"), i*2)
		}
		if f.Item(i, "new_b") != i*3 {
			t.Errorf("item %d new_b: got %v, want %d", i, f.Item(i, "new_b"), i*3)
		}
	}
}

// TestColumnFrameKColumnsConcurrentWriteThenRemoveReorder exercises the
// extension of TestColumnFrameLazyColumnCreation to K=10 newly-created
// columns followed by a single ApplyOutput that combines remove + reorder.
// The contract being pinned: after the structural pass every one of the K
// columns must observe the same per-row permutation, and the per-column
// `visited` scratch buffer is reset between columns inside ApplyOutput
// (column_frame.go:237-241).
//
// Run under `-race` to catch any per-column write that escapes ApplyOutput's
// frame-level Lock; without this case, K column reorder consistency was
// only indirectly covered by single-engine integration tests.
func TestColumnFrameKColumnsConcurrentWriteThenRemoveReorder(t *testing.T) {
	const N = 8 // initial row count
	const K = 10

	items := make([]map[string]any, N)
	for i := range items {
		items[i] = map[string]any{"id": i}
	}
	f := NewFrame(StorageModeColumn, map[string]any{}, items)

	// Phase 1: K goroutines each create a unique column with a deterministic
	// per-row value so we can compute the post-permutation expectation
	// without depending on goroutine ordering.
	var wg sync.WaitGroup
	for k := 0; k < K; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			out := types.NewOperatorOutput()
			for i := 0; i < N; i++ {
				// Encode (column, row) so any cross-column bleed is detectable.
				out.SetItem(i, "col_"+itoa(k), k*1000+i)
			}
			if err := f.ApplyOutput(out, "writer_"+itoa(k), false); err != nil {
				t.Errorf("col_%d write: %v", k, err)
			}
		}(k)
	}
	wg.Wait()

	// Sanity: every (k, i) pair landed correctly before structural ops.
	for k := 0; k < K; k++ {
		for i := 0; i < N; i++ {
			if got := f.Item(i, "col_"+itoa(k)); got != k*1000+i {
				t.Fatalf("pre-structural col_%d[%d]: got %v, want %d", k, i, got, k*1000+i)
			}
		}
	}

	// Phase 2: single ApplyOutput that removes one row then reorders the
	// remainder. Both stages walk every column and share the visited
	// bitmap (column_frame.go:237-241); a missing reset between columns
	// would leave later columns in a partially-permuted state while
	// earlier columns reordered correctly.
	out := types.NewOperatorOutput()
	out.RemoveItem(2) // drop row originally carrying id=2
	// After removal we have rows whose original ids are 0,1,3,4,5,6,7
	// (length 7). Apply a non-identity permutation with two cycles plus
	// a fixed point to make a per-column reset bug visible.
	// new[i] = old[order[i]]: order = [3, 0, 5, 6, 1, 4, 2]
	out.SetItemOrder([]int{3, 0, 5, 6, 1, 4, 2})
	if err := f.ApplyOutput(out, "structural", false); err != nil {
		t.Fatalf("structural ApplyOutput: %v", err)
	}

	// Compute expected post-structural id sequence the same way ApplyOutput
	// does: remove first, then reorder.
	postRemove := []int{0, 1, 3, 4, 5, 6, 7}
	order := []int{3, 0, 5, 6, 1, 4, 2}
	expectedIDs := make([]int, len(order))
	for i, oi := range order {
		expectedIDs[i] = postRemove[oi]
	}

	if f.ItemCount() != len(expectedIDs) {
		t.Fatalf("item count: got %d, want %d", f.ItemCount(), len(expectedIDs))
	}
	for i, expectedID := range expectedIDs {
		// `id` field validates the remove+reorder are consistent on the
		// pre-existing column.
		if got := f.Item(i, "id"); got != expectedID {
			t.Errorf("post-structural id[%d]: got %v, want %d", i, got, expectedID)
		}
		// Every K newly-created column must follow the same permutation;
		// the original row carrying col_k = k*1000+expectedID must now
		// sit at row i.
		for k := 0; k < K; k++ {
			want := k*1000 + expectedID
			if got := f.Item(i, "col_"+itoa(k)); got != want {
				t.Errorf("post-structural col_%d[%d]: got %v, want %d", k, i, got, want)
			}
		}
	}
}

// Local int-to-string helper to avoid pulling strconv into the test file
// solely for diagnostic field names.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	x := n
	for x > 0 {
		digits = append([]byte{byte('0' + x%10)}, digits...)
		x /= 10
	}
	return string(digits)
}

func TestColumnFrameStructuralBlocksReaders(t *testing.T) {
	items := make([]map[string]any, 20)
	for i := range items {
		items[i] = map[string]any{"id": i}
	}
	f := NewFrame(StorageModeColumn, map[string]any{}, items)

	var wg sync.WaitGroup
	// Structural: add items
	wg.Add(1)
	go func() {
		defer wg.Done()
		out := types.NewOperatorOutput()
		for i := 0; i < 10; i++ {
			out.AddItem(map[string]any{"id": 100 + i})
		}
		if err := f.ApplyOutput(out, "recall", true); err != nil {
			t.Errorf("additions: %v", err)
		}
	}()
	// Reader
	wg.Add(1)
	go func() {
		defer wg.Done()
		spec := &config.InputFieldSpec{
			StrictItem: []string{"id"},
		}
		_, _ = f.BuildInput("test_op", spec)
	}()
	wg.Wait()

	if f.ItemCount() != 30 {
		t.Errorf("expected 30 items after additions, got %d", f.ItemCount())
	}
}

func TestRowFrameConcurrentBuildInput(t *testing.T) {
	items := make([]map[string]any, 100)
	for i := range items {
		items[i] = map[string]any{"a": i, "b": i * 10}
	}
	f := NewFrame(StorageModeRow, map[string]any{"x": 1}, items)

	spec := &config.InputFieldSpec{
		StrictCommon: []string{"x"},
		StrictItem:   []string{"a", "b"},
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in, err := f.BuildInput("test_op", spec)
			if err != nil {
				t.Errorf("BuildInput error: %v", err)
				return
			}
			if in.ItemCount() != 100 {
				t.Errorf("expected 100 items, got %d", in.ItemCount())
			}
		}()
	}
	wg.Wait()
}

// TestApplyOutput_AddedItemSurvivesOutputReset pins the lifetime contract
// that scheduler's outputPool reclaim depends on: ApplyOutput on the
// row path take-ownership transfers added-item maps into the frame
// (`f.items = append(f.items, added)`), so a subsequent OperatorOutput.Reset
// must not erase data the frame now holds. The contract is "Reset nils the
// slice slot, never the map itself" — if a future refactor changes Reset
// to clear(map) or wipe inner fields, this test breaks.
func TestApplyOutput_AddedItemSurvivesOutputReset(t *testing.T) {
	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			f := newTestFrame(tm.mode, nil, nil)
			out := types.NewOperatorOutput()
			out.AddItem(map[string]any{"id": "new0", "score": 1.5})
			out.AddItem(map[string]any{"id": "new1", "score": 2.5})

			if err := ApplyOutput(f, out, "op", false); err != nil {
				t.Fatal(err)
			}

			// Simulate the scheduler's defer: Reset and "return to pool".
			// The frame already owns the added rows; Reset must not break
			// them.
			out.Reset()

			if f.ItemCount() != 2 {
				t.Fatalf("ItemCount = %d, want 2", f.ItemCount())
			}
			if got := f.Item(0, "id"); got != "new0" {
				t.Errorf("item[0].id = %v, want new0", got)
			}
			if got := f.Item(0, "score"); got != 1.5 {
				t.Errorf("item[0].score = %v, want 1.5", got)
			}
			if got := f.Item(1, "id"); got != "new1" {
				t.Errorf("item[1].id = %v, want new1", got)
			}
			if got := f.Item(1, "score"); got != 2.5 {
				t.Errorf("item[1].score = %v, want 2.5", got)
			}
		})
	}
}

func FuzzApplyOutputStorageEquivalence(f *testing.F) {
	f.Add([]byte{3, 1, 2, 3, 4, 5, 6, 7, 8})
	f.Add([]byte{1, 0, 4, 1, 0, 2, 9, 2, 1})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			t.Skip("DataFrame fuzz input exceeds CI budget")
		}
		common, items, recall := fuzzFrameState(data)
		row := NewFrame(StorageModeRow, cloneMap(common), cloneItems(items))
		column := NewFrame(StorageModeColumn, cloneMap(common), cloneItems(items))

		rowErr := ApplyOutput(row, fuzzOperatorOutput(data), "fuzz_op", recall)
		columnErr := ApplyOutput(column, fuzzOperatorOutput(data), "fuzz_op", recall)
		if (rowErr == nil) != (columnErr == nil) {
			t.Fatalf("row err = %v, column err = %v", rowErr, columnErr)
		}
		if rowErr != nil {
			return
		}

		rowResult := ToResult(row, fuzzCommonProjection, fuzzItemProjection)
		columnResult := ToResult(column, fuzzCommonProjection, fuzzItemProjection)
		if !reflect.DeepEqual(rowResult.Common, columnResult.Common) {
			t.Fatalf("common mismatch: row=%v column=%v", rowResult.Common, columnResult.Common)
		}
		if !reflect.DeepEqual(rowResult.Items, columnResult.Items) {
			t.Fatalf("items mismatch: row=%v column=%v", rowResult.Items, columnResult.Items)
		}
	})
}

var (
	fuzzCommonProjection = []string{"c0", "c1", "c2", "c3"}
	fuzzItemProjection   = []string{"i0", "i1", "i2", "i3", "added", "_source"}
)

func fuzzFrameState(data []byte) (map[string]any, []map[string]any, bool) {
	cursor := fuzzCursor{data: data}
	common := make(map[string]any, len(fuzzCommonProjection))
	for i, field := range fuzzCommonProjection {
		if cursor.next(0)%2 == 0 {
			common[field] = fuzzValue(cursor.next(byte(i)))
		}
	}
	itemCount := int(cursor.next(3) % 12)
	items := make([]map[string]any, itemCount)
	for i := range items {
		row := make(map[string]any, len(fuzzItemProjection))
		for j, field := range fuzzItemProjection[:4] {
			if cursor.next(0)%2 == 0 {
				row[field] = fuzzValue(cursor.next(byte(i + j)))
			}
		}
		items[i] = row
	}
	return common, items, cursor.next(0)%2 == 0
}

func fuzzOperatorOutput(data []byte) *types.OperatorOutput {
	cursor := fuzzCursor{data: data}
	out := types.NewOperatorOutput()
	ops := int(cursor.next(4)%16) + 1
	for i := 0; i < ops; i++ {
		switch cursor.next(0) % 5 {
		case 0:
			field := fuzzCommonProjection[int(cursor.next(0))%len(fuzzCommonProjection)]
			out.SetCommon(field, fuzzValue(cursor.next(0)))
		case 1:
			idx := fuzzIndex(cursor.next(0))
			field := fuzzItemProjection[int(cursor.next(0))%4]
			out.SetItem(idx, field, fuzzValue(cursor.next(0)))
		case 2:
			out.AddItem(map[string]any{
				"added": fuzzValue(cursor.next(0)),
				"i0":    fuzzValue(cursor.next(1)),
			})
		case 3:
			out.RemoveItem(fuzzIndex(cursor.next(0)))
		case 4:
			n := int(cursor.next(0) % 12)
			order := make([]int, n)
			for j := range order {
				order[j] = fuzzIndex(cursor.next(byte(j)))
			}
			out.SetItemOrder(order)
		}
	}
	return out
}

type fuzzCursor struct {
	data []byte
	pos  int
}

func (c *fuzzCursor) next(defaultValue byte) byte {
	if c.pos >= len(c.data) {
		return defaultValue
	}
	b := c.data[c.pos]
	c.pos++
	return b
}

func fuzzValue(b byte) any {
	switch b % 6 {
	case 0:
		return nil
	case 1:
		return int64(b)
	case 2:
		return float64(b) / 3.0
	case 3:
		return b%2 == 0
	case 4:
		return string([]byte{'v', '0' + b%10})
	default:
		return []any{int64(b)}
	}
}

func fuzzIndex(b byte) int {
	idx := int(b % 16)
	if b%7 == 0 {
		return -idx - 1
	}
	return idx
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneItems(in []map[string]any) []map[string]any {
	out := make([]map[string]any, len(in))
	for i, item := range in {
		out[i] = cloneMap(item)
	}
	return out
}
