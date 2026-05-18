package pine_test

import (
	"context"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func TestOperatorInputCommon(t *testing.T) {
	in := pine.NewOperatorInput(
		map[string]any{"user_age": int64(25), "user_id": "u_123"},
		nil,
	)
	if v := in.Common("user_age"); v != int64(25) {
		t.Errorf("Common(user_age) = %v, want 25", v)
	}
	if v := in.Common("missing"); v != nil {
		t.Errorf("Common(missing) = %v, want nil", v)
	}
}

func TestOperatorInputNilCommon(t *testing.T) {
	in := pine.NewOperatorInput(nil, nil)
	if v := in.Common("anything"); v != nil {
		t.Errorf("Common on nil map = %v, want nil", v)
	}
}

func TestOperatorInputItems(t *testing.T) {
	items := []map[string]any{
		{"item_id": int64(1), "price": 99.0},
		{"item_id": int64(2), "price": 50.0},
	}
	in := pine.NewOperatorInput(nil, items)

	if in.ItemCount() != 2 {
		t.Fatalf("ItemCount() = %d, want 2", in.ItemCount())
	}
	if v := in.Item(0, "price"); v != 99.0 {
		t.Errorf("Item(0, price) = %v, want 99.0", v)
	}
	if v := in.Item(1, "missing"); v != nil {
		t.Errorf("Item(1, missing) = %v, want nil", v)
	}
	// Out of range
	if v := in.Item(-1, "price"); v != nil {
		t.Errorf("Item(-1, price) = %v, want nil", v)
	}
	if v := in.Item(5, "price"); v != nil {
		t.Errorf("Item(5, price) = %v, want nil", v)
	}
}

func TestOperatorOutputSetCommon(t *testing.T) {
	out := pine.NewOperatorOutput()
	out.SetCommon("score", 0.95)
	out.SetCommon("tag", "test")

	writes := out.GetCommonWrites()
	if writes["score"] != 0.95 {
		t.Errorf("score = %v, want 0.95", writes["score"])
	}
	if writes["tag"] != "test" {
		t.Errorf("tag = %v, want test", writes["tag"])
	}
}

func TestOperatorOutputSetItem(t *testing.T) {
	out := pine.NewOperatorOutput()
	out.SetItem(0, "rank", int64(1))
	out.SetItem(1, "rank", int64(2))
	out.SetItem(0, "score", 0.9)

	writes := out.GetItemWrites()
	if writes[0]["rank"] != int64(1) {
		t.Errorf("item 0 rank = %v, want 1", writes[0]["rank"])
	}
	if writes[0]["score"] != 0.9 {
		t.Errorf("item 0 score = %v, want 0.9", writes[0]["score"])
	}
	if writes[1]["rank"] != int64(2) {
		t.Errorf("item 1 rank = %v, want 2", writes[1]["rank"])
	}
}

func TestOperatorOutputAddItem(t *testing.T) {
	out := pine.NewOperatorOutput()
	out.AddItem(map[string]any{"item_id": int64(100)})
	out.AddItem(map[string]any{"item_id": int64(200)})

	added := out.GetAddedItems()
	if len(added) != 2 {
		t.Fatalf("added items = %d, want 2", len(added))
	}
	if added[0]["item_id"] != int64(100) {
		t.Errorf("added[0] item_id = %v, want 100", added[0]["item_id"])
	}
}

func TestOperatorOutputRemoveItem(t *testing.T) {
	out := pine.NewOperatorOutput()
	out.RemoveItem(1)
	out.RemoveItem(3)

	removed := out.GetRemovedItems()
	if _, ok := removed[1]; !ok {
		t.Error("index 1 not marked for removal")
	}
	if _, ok := removed[3]; !ok {
		t.Error("index 3 not marked for removal")
	}
	if len(removed) != 2 {
		t.Errorf("removed count = %d, want 2", len(removed))
	}
}

func TestOperatorOutputSetItemOrder(t *testing.T) {
	out := pine.NewOperatorOutput()
	order := []int{2, 0, 1}
	out.SetItemOrder(order)

	if got := out.GetItemOrder(); len(got) != 3 || got[0] != 2 || got[1] != 0 || got[2] != 1 {
		t.Errorf("item order = %v, want [2 0 1]", got)
	}
}

func TestOperatorOutputWarningFirstWins(t *testing.T) {
	out := pine.NewOperatorOutput()
	first := &pine.ExecutionError{Operator: "op1", Err: context.DeadlineExceeded}
	second := &pine.ExecutionError{Operator: "op2", Err: context.Canceled}
	out.SetWarning(first)
	out.SetWarning(second)

	if out.GetWarning() != first {
		t.Errorf("warning = %v, want first warning", out.GetWarning())
	}
}

func TestErrorTypes(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{&pine.ConfigError{Message: "bad json"}, "pine: config error: bad json"},
		{&pine.RegistryError{Operator: "foo", Message: "not found"}, "pine: registry error [foo]: not found"},
		{&pine.ValidationError{Message: "missing field"}, "pine: validation error: missing field"},
		{&pine.ExecutionError{Operator: "bar", Err: context.Canceled}, `pine: execution error in operator "bar": context canceled`},
		{&pine.PanicError{Operator: "baz", Value: "oops", Stack: "stack"}, "pine: panic in operator \"baz\": oops"},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("Error() = %q, want %q", got, tt.want)
		}
	}
}
