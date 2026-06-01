// Resource: redis_connection
// Description: Shared Redis connection pool. Created once and held for the
// lifetime of the ResourceManager (never refreshed). Operators such as
// transform_redis_get / transform_redis_set reference it by resource_name and
// borrow the *redis.Client per request, so multiple operators pointing at the
// same connection resource share a single pool. The pool is closed when the
// ResourceManager retires and the last in-flight borrow is released.
//
// Params:
//   - addr (string, required): Redis server address (host:port).
//   - password (string, optional, default=""): Redis password.
//   - db (int, optional, default=0): Redis DB number.
package transform

import (
	"context"
	"fmt"

	pine "github.com/Liam0205/pineapple/pine-go"
	"github.com/Liam0205/pineapple/pine-go/pkg/resource"
	"github.com/redis/go-redis/v9"
)

func init() {
	pine.RegisterResource(pine.ResourceSchema{
		Name:            "redis_connection",
		Description:     "Shared Redis connection pool borrowed by Redis operators via resource_name.",
		DefaultInterval: -1, // never refresh: a connection pool has no meaningful refresh.
		Params: map[string]pine.ParamSpec{
			"addr":     {Type: "string", Required: true, Description: "Redis server address (host:port)."},
			"password": {Type: "string", Required: false, Default: "", Description: "Redis password."},
			"db":       {Type: "int", Required: false, Default: 0, Description: "Redis DB number."},
		},
	}, func(params map[string]any) (resource.Fetcher, error) {
		addr, _ := params["addr"].(string)
		if addr == "" {
			return nil, fmt.Errorf("redis_connection: addr is required")
		}
		password, _ := params["password"].(string)
		db := 0
		if v, ok := params["db"]; ok {
			db = int(toInt64Param(v))
		}
		return func(ctx context.Context) (any, error) {
			// *redis.Client implements io.Closer; the ResourceManager closes it
			// on retirement once the last borrow is released.
			return redis.NewClient(&redis.Options{
				Addr:     addr,
				Password: password,
				DB:       db,
			}), nil
		}, nil
	})
}
