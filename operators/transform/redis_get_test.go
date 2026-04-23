package transform

import (
	"context"
	"testing"

	pine "github.com/Liam0205/pineapple"
	"github.com/alicebob/miniredis/v2"
)

func TestRedisGetOp_Init(t *testing.T) {
	op := &RedisGetOp{}
	err := op.Init(map[string]any{
		"redis_addr":     "localhost:6379",
		"redis_password": "secret",
		"redis_db":       float64(2),
		"key_prefix":     "prefix:",
		"data_type":      "set",
	})
	if err != nil {
		t.Fatal(err)
	}
	if op.keyPrefix != "prefix:" {
		t.Errorf("keyPrefix = %q, want prefix:", op.keyPrefix)
	}
	if op.dataType != "set" {
		t.Errorf("dataType = %q, want set", op.dataType)
	}
	if op.rdb == nil {
		t.Error("expected redis client to be created")
	}
}

func TestRedisGetOp_InitDefaults(t *testing.T) {
	op := &RedisGetOp{}
	err := op.Init(map[string]any{
		"redis_addr": "localhost:6379",
		"key_prefix": "p:",
	})
	if err != nil {
		t.Fatal(err)
	}
	if op.dataType != "string" {
		t.Errorf("default dataType = %q, want string", op.dataType)
	}
}

func TestRedisGetOp_NilClient(t *testing.T) {
	op := &RedisGetOp{}
	op.Init(map[string]any{"key_prefix": "k:"})
	op.SetMetadata([]string{"uid"}, []string{"result", "cache_hit"}, nil, nil)

	in := pine.NewOperatorInput(map[string]any{"uid": "u1"}, nil)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
	cw := out.GetCommonWrites()
	if cw["cache_hit"] != false {
		t.Errorf("cache_hit = %v, want false", cw["cache_hit"])
	}
}

func TestRedisGetOp_String(t *testing.T) {
	s := miniredis.RunT(t)
	s.Set("prefix:u1", "hello")

	op := &RedisGetOp{}
	op.Init(map[string]any{
		"redis_addr": s.Addr(),
		"key_prefix": "prefix:",
		"data_type":  "string",
	})
	op.SetMetadata([]string{"uid"}, []string{"result", "cache_hit"}, nil, nil)

	in := pine.NewOperatorInput(map[string]any{"uid": "u1"}, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
	cw := out.GetCommonWrites()
	if cw["result"] != "hello" {
		t.Errorf("result = %v, want hello", cw["result"])
	}
	if cw["cache_hit"] != true {
		t.Errorf("cache_hit = %v, want true", cw["cache_hit"])
	}
}

func TestRedisGetOp_StringMiss(t *testing.T) {
	s := miniredis.RunT(t)

	op := &RedisGetOp{}
	op.Init(map[string]any{
		"redis_addr": s.Addr(),
		"key_prefix": "prefix:",
		"data_type":  "string",
	})
	op.SetMetadata([]string{"uid"}, []string{"result", "cache_hit"}, nil, nil)

	in := pine.NewOperatorInput(map[string]any{"uid": "missing"}, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
	cw := out.GetCommonWrites()
	if cw["cache_hit"] != false {
		t.Errorf("cache_hit = %v, want false", cw["cache_hit"])
	}
}

func TestRedisGetOp_Set(t *testing.T) {
	s := miniredis.RunT(t)
	s.SAdd("prefix:u1", "a", "b", "c")

	op := &RedisGetOp{}
	op.Init(map[string]any{
		"redis_addr": s.Addr(),
		"key_prefix": "prefix:",
		"data_type":  "set",
	})
	op.SetMetadata([]string{"uid"}, []string{"result", "cache_hit"}, nil, nil)

	in := pine.NewOperatorInput(map[string]any{"uid": "u1"}, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
	cw := out.GetCommonWrites()
	if cw["cache_hit"] != true {
		t.Errorf("cache_hit = %v, want true", cw["cache_hit"])
	}
	members, ok := cw["result"].([]string)
	if !ok {
		t.Fatalf("result type = %T, want []string", cw["result"])
	}
	if len(members) != 3 {
		t.Errorf("members len = %d, want 3", len(members))
	}
}

func TestRedisGetOp_SetEmpty(t *testing.T) {
	s := miniredis.RunT(t)

	op := &RedisGetOp{}
	op.Init(map[string]any{
		"redis_addr": s.Addr(),
		"key_prefix": "prefix:",
		"data_type":  "set",
	})
	op.SetMetadata([]string{"uid"}, []string{"result", "cache_hit"}, nil, nil)

	in := pine.NewOperatorInput(map[string]any{"uid": "empty"}, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
	cw := out.GetCommonWrites()
	if cw["cache_hit"] != false {
		t.Errorf("cache_hit = %v, want false", cw["cache_hit"])
	}
}

func TestRedisGetOp_List(t *testing.T) {
	s := miniredis.RunT(t)
	s.RPush("prefix:u1", "x", "y")

	op := &RedisGetOp{}
	op.Init(map[string]any{
		"redis_addr": s.Addr(),
		"key_prefix": "prefix:",
		"data_type":  "list",
	})
	op.SetMetadata([]string{"uid"}, []string{"result", "cache_hit"}, nil, nil)

	in := pine.NewOperatorInput(map[string]any{"uid": "u1"}, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
	cw := out.GetCommonWrites()
	if cw["cache_hit"] != true {
		t.Errorf("cache_hit = %v, want true", cw["cache_hit"])
	}
	vals, ok := cw["result"].([]string)
	if !ok {
		t.Fatalf("result type = %T, want []string", cw["result"])
	}
	if len(vals) != 2 || vals[0] != "x" || vals[1] != "y" {
		t.Errorf("result = %v, want [x y]", vals)
	}
}

func TestRedisGetOp_UnsupportedType(t *testing.T) {
	s := miniredis.RunT(t)

	op := &RedisGetOp{}
	op.Init(map[string]any{
		"redis_addr": s.Addr(),
		"key_prefix": "prefix:",
		"data_type":  "hash",
	})
	op.SetMetadata([]string{"uid"}, []string{"result", "cache_hit"}, nil, nil)

	in := pine.NewOperatorInput(map[string]any{"uid": "u1"}, nil)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err == nil {
		t.Error("expected error for unsupported data_type")
	}
}

func TestBuildKeySuffix(t *testing.T) {
	in := pine.NewOperatorInput(map[string]any{"a": "1", "b": "2", "c": "3"}, nil)

	if got := buildKeySuffix(in, nil); got != "" {
		t.Errorf("empty fields: got %q, want empty", got)
	}
	if got := buildKeySuffix(in, []string{"a"}); got != "1" {
		t.Errorf("single field: got %q, want 1", got)
	}
	if got := buildKeySuffix(in, []string{"a", "b", "c"}); got != "1:2:3" {
		t.Errorf("multi fields: got %q, want 1:2:3", got)
	}
}
