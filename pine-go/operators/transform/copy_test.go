package transform

import (
	"context"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func TestCopyOpInitBadDirection(t *testing.T) {
	op := &CopyOp{}
	if err := op.Init(map[string]any{"direction": "invalid"}); err == nil {
		t.Error("expected error for invalid direction")
	}
}

func TestCopyOpCommonToCommon(t *testing.T) {
	op := &CopyOp{direction: "common_to_common"}
	op.SetMetadata([]string{"src_a", "src_b"}, []string{"dst_a", "dst_b"}, nil, nil)

	in := pine.NewOperatorInput(map[string]any{"src_a": "hello", "src_b": 42.0}, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
	cw := out.GetCommonWrites()
	if cw["dst_a"] != "hello" {
		t.Errorf("dst_a = %v, want hello", cw["dst_a"])
	}
	if cw["dst_b"] != 42.0 {
		t.Errorf("dst_b = %v, want 42", cw["dst_b"])
	}
}

func TestCopyOpCommonToItem(t *testing.T) {
	op := &CopyOp{direction: "common_to_item"}
	op.SetMetadata([]string{"user_tag"}, nil, nil, []string{"item_tag"})

	items := []map[string]any{{"id": 1}, {"id": 2}}
	in := pine.NewOperatorInput(map[string]any{"user_tag": "vip"}, items)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
	iw := out.ItemWriteMap()
	for i := 0; i < 2; i++ {
		if iw[i]["item_tag"] != "vip" {
			t.Errorf("item[%d].item_tag = %v, want vip", i, iw[i]["item_tag"])
		}
	}
}

func TestCopyOpItemToItem(t *testing.T) {
	op := &CopyOp{direction: "item_to_item"}
	op.SetMetadata(nil, nil, []string{"price"}, []string{"original_price"})

	items := []map[string]any{{"price": 10.0}, {"price": 20.0}}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
	iw := out.ItemWriteMap()
	if iw[0]["original_price"] != 10.0 {
		t.Errorf("item[0].original_price = %v, want 10", iw[0]["original_price"])
	}
	if iw[1]["original_price"] != 20.0 {
		t.Errorf("item[1].original_price = %v, want 20", iw[1]["original_price"])
	}
}

func TestCopyOpItemToCommon(t *testing.T) {
	op := &CopyOp{direction: "item_to_common"}
	op.SetMetadata(nil, []string{"price_list"}, []string{"price"}, nil)

	items := []map[string]any{{"price": 10.0}, {"price": 20.0}, {"price": 30.0}}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
	cw := out.GetCommonWrites()
	list, ok := cw["price_list"].([]any)
	if !ok {
		t.Fatalf("price_list type = %T, want []any", cw["price_list"])
	}
	if len(list) != 3 {
		t.Fatalf("price_list len = %d, want 3", len(list))
	}
	if list[0] != 10.0 || list[1] != 20.0 || list[2] != 30.0 {
		t.Errorf("price_list = %v", list)
	}
}

func TestCopyOpEmptyItems(t *testing.T) {
	op := &CopyOp{direction: "common_to_item"}
	op.SetMetadata([]string{"tag"}, nil, nil, []string{"item_tag"})

	in := pine.NewOperatorInput(map[string]any{"tag": "test"}, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
	// No items, no writes expected
	iw := out.GetItemWrites()
	if len(iw) != 0 {
		t.Errorf("expected no item writes, got %d", len(iw))
	}
}
