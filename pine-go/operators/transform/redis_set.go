// Operator: transform_redis_set
// Type: Transform
// Description: Generic Redis write operator. Writes a value by key with optional TTL.
//
// Params:
//   - resource_name (string, required): Name of a redis_connection resource to borrow the client from.
//   - key_prefix (string, required, templatable): Key prefix prepended to the suffix built from common_input fields.
//     Supports {{field}} interpolation — the engine resolves the markers against
//     the request's common frame per request (issue #74).
//   - data_type (string, optional, default="string"): Redis data type: "set", "string", or "list".
//   - ttl (int, optional, default=0, templatable): TTL in seconds. 0 means no expiry. Supports {{field}} interpolation.
//   - fail_on_error (bool, optional, default=false): Return fatal error on Redis infrastructure failure instead of logging and continuing.
//
// The Redis connection pool is owned by the ResourceManager (resource type
// redis_connection), not the operator: the client is borrowed per request and
// released when Execute returns. Multiple Redis operators referencing the same
// resource_name share one pool.
//
// Key construction: key_prefix + join(first N-1 common_input values, ":").
// Value is the last common_input field.
//
// Metadata contract (typical usage):
//
//	CommonInput:  [<key_suffix_fields...>, <value_field>]
//	CommonOutput: []
//	ItemInput:    []
//	ItemOutput:   []
package transform

import (
	"context"
	"fmt"
	"log"
	"time"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "transform_redis_set",
		Type:        pine.OpTypeTransform,
		Description: "Generic Redis write operator. Writes a value by key with optional TTL.",
		Params: map[string]pine.ParamSpec{
			"resource_name": {Type: "string", Required: true, Description: "Name of a redis_connection resource to borrow the client from."},
			"key_prefix":    {Type: "string", Required: true, Templatable: true, Description: "Key prefix prepended to the suffix built from common_input fields. Supports {{field}} interpolation."},
			"data_type":     {Type: "string", Required: false, Default: "string", Description: `Redis data type: "set", "string", or "list".`},
			"ttl":           {Type: "int", Required: false, Default: 0, Templatable: true, Description: "TTL in seconds. 0 means no expiry. Supports {{field}} interpolation."},
			"fail_on_error": {Type: "bool", Required: false, Default: false, Description: "Return fatal error on Redis infrastructure failure instead of logging and continuing."},
		},
	}, func() pine.Operator {
		return &RedisSetOp{}
	})
}

// RedisSetOp writes a value to Redis by constructed key. The Redis client is
// borrowed from a redis_connection resource per request; the operator holds no
// connection of its own.
type RedisSetOp struct {
	pine.MetadataHolder
	pine.ConcurrentSafeMarker
	resourceName string
	keyPrefix    string
	dataType     string
	ttl          time.Duration
	failOnError  bool
}

func (o *RedisSetOp) Init(params map[string]any) error {
	o.resourceName, _ = params["resource_name"].(string)
	o.keyPrefix, _ = params["key_prefix"].(string)
	o.dataType, _ = params["data_type"].(string)
	if o.dataType == "" {
		o.dataType = "string"
	}
	if v, ok := params["ttl"]; ok {
		o.ttl = time.Duration(toInt64Param(v)) * time.Second
	}
	if v, ok := params["fail_on_error"].(bool); ok {
		o.failOnError = v
	}
	return nil
}

func (o *RedisSetOp) Execute(ctx context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	rdb, release, ok := borrowRedis(ctx, o.resourceName)
	if !ok {
		return nil
	}
	defer release()

	n := len(o.CommonInput)
	if n < 2 {
		return fmt.Errorf("transform_redis_set: common_input must have at least 2 fields (key fields + value field)")
	}

	// key_prefix and ttl are both templatable (#74). When the DSL configured
	// a {{field}} marker the engine resolved it against this request's
	// common frame before Execute; otherwise the init-time value is used.
	prefix := o.keyPrefix
	if v, ok := in.TemplatedParam("key_prefix"); ok {
		// Inner type assertion is unreachable: BuildTemplatedParamPlan
		// rejects any non-string declared type for key_prefix, and
		// ResolveTemplatedParams normalizes the resolved value through
		// GoFormat.sprint for string-typed params. Kept as defense in
		// depth — a missed cast would otherwise surface as the
		// init-time string with a literal {{field}} marker.
		if s, ok := v.(string); ok {
			prefix = s
		}
	}
	ttl := o.ttl
	if v, ok := in.TemplatedParam("ttl"); ok {
		// Inner type assertion is unreachable: ttl is declared int in
		// schema, ResolveTemplatedParams coerces the substituted string
		// via strconv.ParseInt → int64. Defense-in-depth fallback as
		// above.
		if n, ok := v.(int64); ok {
			ttl = time.Duration(n) * time.Second
		}
	}

	key := prefix + buildKeySuffix(in, o.CommonInput[:n-1])
	value := in.Common(o.CommonInput[n-1])

	var err error
	switch o.dataType {
	case "set":
		members, ok := toStringSlice(value)
		if !ok {
			log.Printf("transform_redis_set: value for key %s is not []string", key)
			return nil
		}
		if len(members) == 0 {
			return nil
		}
		pipe := rdb.Pipeline()
		pipe.Del(ctx, key)
		pipe.SAdd(ctx, key, strSliceToAny(members)...)
		if ttl > 0 {
			pipe.Expire(ctx, key, ttl)
		}
		_, err = pipe.Exec(ctx)

	case "list":
		members, ok := toStringSlice(value)
		if !ok {
			log.Printf("transform_redis_set: value for key %s is not []string", key)
			return nil
		}
		if len(members) == 0 {
			return nil
		}
		pipe := rdb.Pipeline()
		pipe.Del(ctx, key)
		pipe.RPush(ctx, key, strSliceToAny(members)...)
		if ttl > 0 {
			pipe.Expire(ctx, key, ttl)
		}
		_, err = pipe.Exec(ctx)

	case "string":
		s, ok := value.(string)
		if !ok {
			log.Printf("transform_redis_set: value for key %s is not string", key)
			return nil
		}
		err = rdb.Set(ctx, key, s, ttl).Err()

	default:
		return fmt.Errorf("transform_redis_set: unsupported data_type %q", o.dataType)
	}

	if err != nil {
		log.Printf("transform_redis_set: write key %s: %v", key, err)
		if o.failOnError {
			return fmt.Errorf("transform_redis_set: write key %s: %w", key, err)
		}
		out.SetWarning(fmt.Errorf("transform_redis_set: write key %s: %w", key, err))
	}
	return nil
}

func toStringSlice(v any) ([]string, bool) {
	switch x := v.(type) {
	case []string:
		return x, true
	case []any:
		ss := make([]string, len(x))
		for i, elem := range x {
			ss[i] = fmt.Sprint(elem)
		}
		return ss, true
	default:
		return nil, false
	}
}

func strSliceToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
