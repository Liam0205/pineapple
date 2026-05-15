// Operator: transform_redis_set
// Type: Transform
// Description: Generic Redis write operator. Writes a value by key with optional TTL.
//
// Params:
//   - redis_addr (string, required): Redis server address (host:port).
//   - redis_password (string, optional, default=""): Redis password.
//   - redis_db (int, optional, default=0): Redis DB number.
//   - key_prefix (string, required): Key prefix prepended to the suffix built from common_input fields.
//   - data_type (string, optional, default="string"): Redis data type: "set", "string", or "list".
//   - ttl (int, optional, default=0): TTL in seconds. 0 means no expiry.
//   - fail_on_error (bool, optional, default=false): Return fatal error on Redis infrastructure failure instead of logging and continuing.
//
// Key construction: key_prefix + join(first N-1 common_input values, ":").
// Value is the last common_input field.
//
// Metadata contract (typical usage):
//   CommonInput:  [<key_suffix_fields...>, <value_field>]
//   CommonOutput: []
//   ItemInput:    []
//   ItemOutput:   []
package transform

import (
	"context"
	"fmt"
	"log"
	"time"

	pine "github.com/Liam0205/pineapple/pine-go"
	"github.com/redis/go-redis/v9"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "transform_redis_set",
		Type:        pine.OpTypeTransform,
		Description: "Generic Redis write operator. Writes a value by key with optional TTL.",
		Params: map[string]pine.ParamSpec{
			"redis_addr":     {Type: "string", Required: true, Description: "Redis server address (host:port)."},
			"redis_password": {Type: "string", Required: false, Default: "", Description: "Redis password."},
			"redis_db":       {Type: "int", Required: false, Default: 0, Description: "Redis DB number."},
			"key_prefix":     {Type: "string", Required: true, Description: "Key prefix prepended to the suffix built from common_input fields."},
			"data_type":      {Type: "string", Required: false, Default: "string", Description: `Redis data type: "set", "string", or "list".`},
			"ttl":            {Type: "int", Required: false, Default: 0, Description: "TTL in seconds. 0 means no expiry."},
			"fail_on_error":  {Type: "bool", Required: false, Default: false, Description: "Return fatal error on Redis infrastructure failure instead of logging and continuing."},
		},
	}, func() pine.Operator {
		return &RedisSetOp{}
	})
}

// RedisSetOp writes a value to Redis by constructed key.
type RedisSetOp struct {
	pine.MetadataHolder
	pine.ConcurrentSafeMarker
	rdb         *redis.Client
	keyPrefix   string
	dataType    string
	ttl         time.Duration
	failOnError bool
}

func (o *RedisSetOp) Init(params map[string]any) error {
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
	if v, ok := params["ttl"]; ok {
		o.ttl = time.Duration(toInt64Param(v)) * time.Second
	}
	if v, ok := params["fail_on_error"].(bool); ok {
		o.failOnError = v
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

func (o *RedisSetOp) Execute(ctx context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	if o.rdb == nil {
		return nil
	}

	n := len(o.CommonInput)
	if n < 2 {
		return fmt.Errorf("transform_redis_set: common_input must have at least 2 fields (key fields + value field)")
	}

	key := o.keyPrefix + buildKeySuffix(in, o.CommonInput[:n-1])
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
		pipe := o.rdb.Pipeline()
		pipe.Del(ctx, key)
		pipe.SAdd(ctx, key, strSliceToAny(members)...)
		if o.ttl > 0 {
			pipe.Expire(ctx, key, o.ttl)
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
		pipe := o.rdb.Pipeline()
		pipe.Del(ctx, key)
		pipe.RPush(ctx, key, strSliceToAny(members)...)
		if o.ttl > 0 {
			pipe.Expire(ctx, key, o.ttl)
		}
		_, err = pipe.Exec(ctx)

	case "string":
		s, ok := value.(string)
		if !ok {
			log.Printf("transform_redis_set: value for key %s is not string", key)
			return nil
		}
		err = o.rdb.Set(ctx, key, s, o.ttl).Err()

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
