package transform

import (
	"context"
	"strings"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
	"github.com/alicebob/miniredis/v2"
)

func TestRedisSetOp_Init(t *testing.T) {
	op := &RedisSetOp{}
	err := op.Init(map[string]any{
		"redis_addr":     "localhost:6379",
		"redis_password": "secret",
		"redis_db":       float64(3),
		"key_prefix":     "wp:",
		"data_type":      "list",
		"ttl":            float64(60),
	})
	if err != nil {
		t.Fatal(err)
	}
	if op.keyPrefix != "wp:" {
		t.Errorf("keyPrefix = %q, want wp:", op.keyPrefix)
	}
	if op.dataType != "list" {
		t.Errorf("dataType = %q, want list", op.dataType)
	}
	if op.ttl.Seconds() != 60 {
		t.Errorf("ttl = %v, want 60s", op.ttl)
	}
	if op.rdb == nil {
		t.Error("expected redis client to be created")
	}
}

func TestRedisSetOp_InitDefaults(t *testing.T) {
	op := &RedisSetOp{}
	if err := op.Init(map[string]any{
		"redis_addr": "localhost:6379",
		"key_prefix": "p:",
	}); err != nil {
		t.Fatal(err)
	}
	if op.dataType != "string" {
		t.Errorf("default dataType = %q, want string", op.dataType)
	}
	if op.ttl != 0 {
		t.Errorf("default ttl = %v, want 0", op.ttl)
	}
}

func TestRedisSetOp_NilClient(t *testing.T) {
	op := &RedisSetOp{}
	if err := op.Init(map[string]any{"key_prefix": "k:"}); err != nil {
		t.Fatal(err)
	}
	op.SetMetadata([]string{"uid", "val"}, nil, nil, nil)

	in := pine.NewOperatorInput(map[string]any{"uid": "u1", "val": "v"}, nil)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Errorf("nil client should return nil, got %v", err)
	}
}

func TestRedisSetOp_String(t *testing.T) {
	s := miniredis.RunT(t)

	op := &RedisSetOp{}
	if err := op.Init(map[string]any{
		"redis_addr": s.Addr(),
		"key_prefix": "prefix:",
		"data_type":  "string",
	}); err != nil {
		t.Fatal(err)
	}
	op.SetMetadata([]string{"uid", "val"}, nil, nil, nil)

	in := pine.NewOperatorInput(map[string]any{"uid": "u1", "val": "hello"}, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get("prefix:u1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Errorf("redis value = %q, want hello", got)
	}
}

func TestRedisSetOp_StringBadType(t *testing.T) {
	s := miniredis.RunT(t)

	op := &RedisSetOp{}
	if err := op.Init(map[string]any{
		"redis_addr": s.Addr(),
		"key_prefix": "prefix:",
		"data_type":  "string",
	}); err != nil {
		t.Fatal(err)
	}
	op.SetMetadata([]string{"uid", "val"}, nil, nil, nil)

	in := pine.NewOperatorInput(map[string]any{"uid": "u1", "val": 12345}, nil)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Errorf("bad type should degrade gracefully, got error: %v", err)
	}
}

func TestRedisSetOp_Set(t *testing.T) {
	s := miniredis.RunT(t)

	op := &RedisSetOp{}
	if err := op.Init(map[string]any{
		"redis_addr": s.Addr(),
		"key_prefix": "prefix:",
		"data_type":  "set",
	}); err != nil {
		t.Fatal(err)
	}
	op.SetMetadata([]string{"uid", "tags"}, nil, nil, nil)

	in := pine.NewOperatorInput(map[string]any{
		"uid":  "u1",
		"tags": []string{"a", "b", "c"},
	}, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}

	members, err := s.Members("prefix:u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 3 {
		t.Errorf("set members = %d, want 3", len(members))
	}
}

func TestRedisSetOp_List(t *testing.T) {
	s := miniredis.RunT(t)

	op := &RedisSetOp{}
	if err := op.Init(map[string]any{
		"redis_addr": s.Addr(),
		"key_prefix": "prefix:",
		"data_type":  "list",
	}); err != nil {
		t.Fatal(err)
	}
	op.SetMetadata([]string{"uid", "items"}, nil, nil, nil)

	in := pine.NewOperatorInput(map[string]any{
		"uid":   "u1",
		"items": []any{"x", "y", "z"},
	}, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}

	vals, err := s.List("prefix:u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 3 || vals[0] != "x" {
		t.Errorf("list = %v, want [x y z]", vals)
	}
}

func TestRedisSetOp_WithTTL(t *testing.T) {
	s := miniredis.RunT(t)

	op := &RedisSetOp{}
	if err := op.Init(map[string]any{
		"redis_addr": s.Addr(),
		"key_prefix": "prefix:",
		"data_type":  "string",
		"ttl":        float64(300),
	}); err != nil {
		t.Fatal(err)
	}
	op.SetMetadata([]string{"uid", "val"}, nil, nil, nil)

	in := pine.NewOperatorInput(map[string]any{"uid": "u1", "val": "data"}, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}

	ttl := s.TTL("prefix:u1")
	if ttl.Seconds() != 300 {
		t.Errorf("ttl = %v, want 300s", ttl)
	}
}

func TestRedisSetOp_TooFewFields(t *testing.T) {
	s := miniredis.RunT(t)

	op := &RedisSetOp{}
	if err := op.Init(map[string]any{
		"redis_addr": s.Addr(),
		"key_prefix": "prefix:",
	}); err != nil {
		t.Fatal(err)
	}
	op.SetMetadata([]string{"uid"}, nil, nil, nil)

	in := pine.NewOperatorInput(map[string]any{"uid": "u1"}, nil)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err == nil {
		t.Error("expected error for too few common_input fields")
	}
}

func TestRedisSetOp_UnsupportedType(t *testing.T) {
	s := miniredis.RunT(t)

	op := &RedisSetOp{}
	if err := op.Init(map[string]any{
		"redis_addr": s.Addr(),
		"key_prefix": "prefix:",
		"data_type":  "hash",
	}); err != nil {
		t.Fatal(err)
	}
	op.SetMetadata([]string{"uid", "val"}, nil, nil, nil)

	in := pine.NewOperatorInput(map[string]any{"uid": "u1", "val": "v"}, nil)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err == nil {
		t.Error("expected error for unsupported data_type")
	}
}

func TestRedisSetOp_SetEmptyMembers(t *testing.T) {
	s := miniredis.RunT(t)

	op := &RedisSetOp{}
	if err := op.Init(map[string]any{
		"redis_addr": s.Addr(),
		"key_prefix": "prefix:",
		"data_type":  "set",
	}); err != nil {
		t.Fatal(err)
	}
	op.SetMetadata([]string{"uid", "tags"}, nil, nil, nil)

	in := pine.NewOperatorInput(map[string]any{
		"uid":  "u1",
		"tags": []string{},
	}, nil)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Errorf("empty set should be no-op, got %v", err)
	}
	if s.Exists("prefix:u1") {
		t.Error("key should not exist for empty set")
	}
}

func TestRedisSetOp_FailOnError_True(t *testing.T) {
	s := miniredis.RunT(t)

	op := &RedisSetOp{}
	if err := op.Init(map[string]any{
		"redis_addr":    s.Addr(),
		"key_prefix":    "prefix:",
		"data_type":     "string",
		"fail_on_error": true,
	}); err != nil {
		t.Fatal(err)
	}
	op.SetMetadata([]string{"uid", "val"}, nil, nil, nil)

	// Close miniredis to simulate infrastructure failure
	s.Close()

	in := pine.NewOperatorInput(map[string]any{"uid": "u1", "val": "hello"}, nil)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err == nil {
		t.Fatal("expected fatal error with fail_on_error=true")
	}
	if !strings.Contains(err.Error(), "transform_redis_set") {
		t.Errorf("error should have operator prefix, got: %v", err)
	}
}

func TestRedisSetOp_FailOnError_False_SetWarning(t *testing.T) {
	s := miniredis.RunT(t)

	op := &RedisSetOp{}
	if err := op.Init(map[string]any{
		"redis_addr":    s.Addr(),
		"key_prefix":    "prefix:",
		"data_type":     "string",
		"fail_on_error": false,
	}); err != nil {
		t.Fatal(err)
	}
	op.SetMetadata([]string{"uid", "val"}, nil, nil, nil)

	// Close miniredis to simulate infrastructure failure
	s.Close()

	in := pine.NewOperatorInput(map[string]any{"uid": "u1", "val": "hello"}, nil)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Errorf("expected nil error (default fail_on_error=false), got %v", err)
	}
	if out.GetWarning() == nil {
		t.Error("expected warning to be set on infrastructure failure")
	}
	if !strings.Contains(out.GetWarning().Error(), "transform_redis_set") {
		t.Errorf("warning should have operator prefix, got: %v", out.GetWarning())
	}
}
