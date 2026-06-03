// Operator: transform_redis_get
// Type: Transform
// Description: Generic Redis read operator. Reads a value by key and outputs the result and a cache-hit flag.
//
// Params:
//   - resource_name (string, required): Name of a redis_connection resource to borrow the client from.
//   - key_prefix (string, required, templatable): Key prefix prepended to the suffix built from common_input fields.
//     Supports {{field}} interpolation — the engine resolves the markers against
//     the request's common frame per request (issue #74).
//   - data_type (string, optional, default="string"): Redis data type: "set", "string", or "list".
//   - fail_on_error (bool, optional, default=false): Return fatal error on Redis infrastructure failure instead of treating as cache miss.
//
// The Redis connection pool is owned by the ResourceManager (resource type
// redis_connection), not the operator: the client is borrowed per request and
// released when Execute returns. Multiple Redis operators referencing the same
// resource_name share one pool.
//
// Key construction: key_prefix + join(common_input values, ":").
// common_output[0] = result value, common_output[1] = cache hit flag (bool).
//
// Metadata contract (typical usage):
//
//	CommonInput:  [<key_suffix_fields...>]
//	CommonOutput: [<result_field>, <cache_hit_field>]
//	ItemInput:    []
//	ItemOutput:   []
package transform

import (
	"context"
	"fmt"

	pine "github.com/Liam0205/pineapple/pine-go"
	"github.com/Liam0205/pineapple/pine-go/pkg/resource"
	"github.com/redis/go-redis/v9"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "transform_redis_get",
		Type:        pine.OpTypeTransform,
		Description: "Generic Redis read operator. Reads a value by key and outputs the result and a cache-hit flag.",
		Params: map[string]pine.ParamSpec{
			"resource_name": {Type: "string", Required: true, Description: "Name of a redis_connection resource to borrow the client from."},
			"key_prefix":    {Type: "string", Required: true, Templatable: true, Description: "Key prefix prepended to the suffix built from common_input fields. Supports {{field}} interpolation."},
			"data_type":     {Type: "string", Required: false, Default: "string", Description: `Redis data type: "set", "string", or "list".`},
			"fail_on_error": {Type: "bool", Required: false, Default: false, Description: "Return fatal error on Redis infrastructure failure instead of treating as cache miss."},
		},
	}, func() pine.Operator {
		return &RedisGetOp{}
	})
}

// RedisGetOp reads a value from Redis by constructed key. The Redis client is
// borrowed from a redis_connection resource per request; the operator holds no
// connection of its own.
type RedisGetOp struct {
	pine.MetadataHolder
	pine.ConcurrentSafeMarker
	resourceName string
	keyPrefix    string
	dataType     string
	failOnError  bool
}

func (o *RedisGetOp) Init(params map[string]any) error {
	o.resourceName, _ = params["resource_name"].(string)
	o.keyPrefix, _ = params["key_prefix"].(string)
	o.dataType, _ = params["data_type"].(string)
	if o.dataType == "" {
		o.dataType = "string"
	}
	if v, ok := params["fail_on_error"].(bool); ok {
		o.failOnError = v
	}
	return nil
}

func (o *RedisGetOp) Execute(ctx context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	resultField := o.CommonOutput[0]
	cacheHitField := o.CommonOutput[1]

	rdb, release, ok := borrowRedis(ctx, o.resourceName)
	if !ok {
		out.SetCommon(cacheHitField, false)
		return nil
	}
	defer release()

	// key_prefix is templatable (#74). When the DSL configured a {{field}}
	// marker the engine resolved it against this request's common frame
	// before Execute; otherwise the raw init-time string is used.
	prefix := o.keyPrefix
	if v, ok := in.TemplatedParam("key_prefix"); ok {
		// Inner type assertion is unreachable: BuildTemplatedParamPlan
		// rejects any non-string declared type for key_prefix, and
		// ResolveTemplatedParams normalizes the resolved value through
		// GoFormat.sprint for string-typed params. The fallback is
		// kept as defense in depth — a missed cast would otherwise
		// surface as the init-time string with literal {{field}} marker.
		if s, ok := v.(string); ok {
			prefix = s
		}
	}
	key := prefix + buildKeySuffix(in, o.CommonInput)

	switch o.dataType {
	case "set":
		members, err := rdb.SMembers(ctx, key).Result()
		if err != nil && err != redis.Nil {
			out.SetWarning(fmt.Errorf("transform_redis_get: %s(%s): %v", "SMembers", key, err))
			if o.failOnError {
				return fmt.Errorf("transform_redis_get: SMembers(%s): %v", key, err)
			}
			out.SetCommon(cacheHitField, false)
			return nil
		}
		if len(members) > 0 {
			out.SetCommon(resultField, members)
			out.SetCommon(cacheHitField, true)
		} else {
			out.SetCommon(cacheHitField, false)
		}

	case "list":
		vals, err := rdb.LRange(ctx, key, 0, -1).Result()
		if err != nil && err != redis.Nil {
			out.SetWarning(fmt.Errorf("transform_redis_get: %s(%s): %v", "LRange", key, err))
			if o.failOnError {
				return fmt.Errorf("transform_redis_get: LRange(%s): %v", key, err)
			}
			out.SetCommon(cacheHitField, false)
			return nil
		}
		if len(vals) > 0 {
			out.SetCommon(resultField, vals)
			out.SetCommon(cacheHitField, true)
		} else {
			out.SetCommon(cacheHitField, false)
		}

	case "string":
		val, err := rdb.Get(ctx, key).Result()
		if err != nil && err != redis.Nil {
			out.SetWarning(fmt.Errorf("transform_redis_get: %s(%s): %v", "Get", key, err))
			if o.failOnError {
				return fmt.Errorf("transform_redis_get: Get(%s): %v", key, err)
			}
			out.SetCommon(cacheHitField, false)
			return nil
		}
		if err == redis.Nil || val == "" {
			out.SetCommon(cacheHitField, false)
		} else {
			out.SetCommon(resultField, val)
			out.SetCommon(cacheHitField, true)
		}

	default:
		return fmt.Errorf("transform_redis_get: unsupported data_type %q", o.dataType)
	}

	return nil
}

// borrowRedis borrows a *redis.Client from a redis_connection resource by name.
// It returns the client, a release function the caller must defer, and ok=false
// (with a no-op release) when no provider is injected, the resource is missing,
// or its value is not a *RedisConnResource — in which case the caller degrades.
func borrowRedis(ctx context.Context, name string) (*redis.Client, func(), bool) {
	rp := resource.FromContext(ctx)
	if rp == nil {
		return nil, func() {}, false
	}
	h, ok := rp.Get(name)
	if !ok {
		return nil, func() {}, false
	}
	conn, ok := h.Value().(*RedisConnResource)
	if !ok {
		h.Release()
		return nil, func() {}, false
	}
	return conn.Client(), h.Release, true
}

// buildKeySuffix joins the values of the given common fields with ":".
func buildKeySuffix(in *pine.OperatorInput, fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	if len(fields) == 1 {
		return fmt.Sprint(in.Common(fields[0]))
	}
	var s string
	for i, f := range fields {
		if i > 0 {
			s += ":"
		}
		s += fmt.Sprint(in.Common(f))
	}
	return s
}

// toInt64Param converts a param value to int64.
func toInt64Param(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case float64:
		return int64(x)
	case int:
		return int64(x)
	default:
		return 0
	}
}
