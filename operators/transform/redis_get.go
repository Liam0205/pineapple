// Operator: transform_redis_get
// Type: Transform
// Description: Generic Redis read operator. Reads a value by key and outputs the result and a cache-hit flag.
//
// Params:
//   - redis_addr (string, required): Redis server address (host:port).
//   - redis_password (string, optional, default=""): Redis password.
//   - redis_db (int, optional, default=0): Redis DB number.
//   - key_prefix (string, required): Key prefix prepended to the suffix built from common_input fields.
//   - data_type (string, optional, default="string"): Redis data type: "set", "string", or "list".
//
// Key construction: key_prefix + join(common_input values, ":").
// common_output[0] = result value, common_output[1] = cache hit flag (bool).
//
// Metadata contract (typical usage):
//   CommonInput:  [<key_suffix_fields...>]
//   CommonOutput: [<result_field>, <cache_hit_field>]
//   ItemInput:    []
//   ItemOutput:   []
package transform

import (
	"context"
	"fmt"
	"log"

	pine "github.com/Liam0205/pineapple"
	"github.com/redis/go-redis/v9"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "transform_redis_get",
		Type:        pine.OpTypeTransform,
		Description: "Generic Redis read operator. Reads a value by key and outputs the result and a cache-hit flag.",
		Params: map[string]pine.ParamSpec{
			"redis_addr":     {Type: "string", Required: true, Description: "Redis server address (host:port)."},
			"redis_password": {Type: "string", Required: false, Default: "", Description: "Redis password."},
			"redis_db":       {Type: "int", Required: false, Default: 0, Description: "Redis DB number."},
			"key_prefix":     {Type: "string", Required: true, Description: "Key prefix prepended to the suffix built from common_input fields."},
			"data_type":      {Type: "string", Required: false, Default: "string", Description: `Redis data type: "set", "string", or "list".`},
		},
	}, func() pine.Operator {
		return &RedisGetOp{}
	})
}

// RedisGetOp reads a value from Redis by constructed key.
type RedisGetOp struct {
	pine.MetadataHolder
	pine.ConcurrentSafeMarker
	rdb       *redis.Client
	keyPrefix string
	dataType  string
}

func (o *RedisGetOp) Init(params map[string]any) error {
	addr, _ := params["redis_addr"].(string)
	password, _ := params["redis_password"].(string)
	db := 0
	if v, ok := params["redis_db"]; ok {
		db = int(toInt64Param(v))
	}
	o.keyPrefix, _ = params["key_prefix"].(string)
	o.dataType, _ = params["data_type"].(string)
	if o.dataType == "" {
		o.dataType = "string"
	}

	if addr != "" {
		o.rdb = redis.NewClient(&redis.Options{
			Addr:     addr,
			Password: password,
			DB:       db,
		})
	}
	return nil
}

func (o *RedisGetOp) Execute(ctx context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	resultField := o.CommonOutput[0]
	cacheHitField := o.CommonOutput[1]

	if o.rdb == nil {
		out.SetCommon(cacheHitField, false)
		return nil
	}

	key := o.keyPrefix + buildKeySuffix(in, o.CommonInput)

	switch o.dataType {
	case "set":
		members, err := o.rdb.SMembers(ctx, key).Result()
		if err != nil && err != redis.Nil {
			log.Printf("transform_redis_get: SMembers(%s): %v", key, err)
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
		vals, err := o.rdb.LRange(ctx, key, 0, -1).Result()
		if err != nil && err != redis.Nil {
			log.Printf("transform_redis_get: LRange(%s): %v", key, err)
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
		val, err := o.rdb.Get(ctx, key).Result()
		if err != nil && err != redis.Nil {
			log.Printf("transform_redis_get: Get(%s): %v", key, err)
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
