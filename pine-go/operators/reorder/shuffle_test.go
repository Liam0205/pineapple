package reorder

import (
	"context"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func TestShuffleBySaltDeterministic(t *testing.T) {
	op := &ShuffleBySaltOp{}
	op.SetMetadata([]string{"user_id"}, nil, []string{"item_id"}, nil)

	items := []map[string]any{
		{"item_id": "100"},
		{"item_id": "200"},
		{"item_id": "300"},
		{"item_id": "400"},
		{"item_id": "500"},
	}
	common := map[string]any{"user_id": "abc123"}

	// Run twice with the same salt → must produce identical order
	var orders [2][]int
	for run := 0; run < 2; run++ {
		in := pine.NewOperatorInput(common, items)
		out := pine.NewOperatorOutput()
		if err := op.Execute(context.Background(), in, out); err != nil {
			t.Fatal(err)
		}
		orders[run] = out.GetItemOrder()
	}
	if len(orders[0]) != len(orders[1]) {
		t.Fatalf("order lengths differ: %d vs %d", len(orders[0]), len(orders[1]))
	}
	for i := range orders[0] {
		if orders[0][i] != orders[1][i] {
			t.Errorf("order[%d]: %d vs %d", i, orders[0][i], orders[1][i])
		}
	}
}

func TestShuffleBySaltDifferentSalt(t *testing.T) {
	op := &ShuffleBySaltOp{}
	op.SetMetadata([]string{"user_id"}, nil, []string{"item_id"}, nil)

	items := []map[string]any{
		{"item_id": "100"},
		{"item_id": "200"},
		{"item_id": "300"},
		{"item_id": "400"},
		{"item_id": "500"},
	}

	in1 := pine.NewOperatorInput(map[string]any{"user_id": "salt_A"}, items)
	out1 := pine.NewOperatorOutput()
	_ = op.Execute(context.Background(), in1, out1)
	order1 := out1.GetItemOrder()

	in2 := pine.NewOperatorInput(map[string]any{"user_id": "salt_B"}, items)
	out2 := pine.NewOperatorOutput()
	_ = op.Execute(context.Background(), in2, out2)
	order2 := out2.GetItemOrder()

	// With different salts, order should (very likely) differ
	same := true
	for i := range order1 {
		if order1[i] != order2[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("different salts produced identical order (extremely unlikely)")
	}
}

func TestShuffleBySaltEmpty(t *testing.T) {
	op := &ShuffleBySaltOp{}
	op.SetMetadata([]string{"salt"}, nil, []string{"item_id"}, nil)

	in := pine.NewOperatorInput(map[string]any{"salt": "x"}, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
}

func TestShuffleBySaltSingleItem(t *testing.T) {
	op := &ShuffleBySaltOp{}
	op.SetMetadata([]string{"salt"}, nil, []string{"item_id"}, nil)

	items := []map[string]any{{"item_id": "42"}}
	in := pine.NewOperatorInput(map[string]any{"salt": "x"}, items)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
	order := out.GetItemOrder()
	if len(order) != 1 || order[0] != 0 {
		t.Errorf("single item order = %v, want [0]", order)
	}
}
