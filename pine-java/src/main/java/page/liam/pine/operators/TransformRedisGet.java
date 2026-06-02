package page.liam.pine.operators;

import page.liam.pine.*;
import page.liam.pine.GoFormat;
import redis.clients.jedis.Jedis;
import redis.clients.jedis.JedisPool;

import java.util.*;

/**
 * Operator: transform_redis_get
 * Metadata contract
 *   CommonInput:  [<key_suffix_fields...>]
 *   CommonOutput: [<result_field>, <cache_hit_field>]
 *   ItemInput:    []
 *   ItemOutput:   []
 *
 * <p>The Redis connection pool is owned by the ResourceManager (resource type
 * redis_connection), not the operator: the pool is borrowed per request via
 * resource_name. Multiple Redis operators referencing the same resource_name
 * share one pool.
 */
public class TransformRedisGet extends AbstractOperator implements ConcurrentSafe, ResourceAware {
    private String resourceName;
    private ResourceProvider resourceProvider;
    private String keyPrefix;
    private String dataType = "string";
    private boolean failOnError;

    @Override
    public void init(OperatorParams params) {
        resourceName = params.getString("resource_name", "");
        keyPrefix = params.getString("key_prefix", "");
        Object dt = params.get("data_type");
        if (dt instanceof String && !((String) dt).isEmpty()) {
            dataType = (String) dt;
        }
        Object foe = params.get("fail_on_error");
        if (foe instanceof Boolean) {
            failOnError = (Boolean) foe;
        }
    }

    @Override
    public void setResourceProvider(ResourceProvider provider) {
        this.resourceProvider = provider;
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) throws PineErrors.OperatorException {
        String resultField = commonOutput().get(0);
        String cacheHitField = commonOutput().get(1);

        JedisPool pool = borrowPool(resourceProvider, resourceName);
        if (pool == null) {
            output.setCommon(cacheHitField, false);
            return;
        }

        // key_prefix is templatable (#74). When the DSL configured a
        // {{field}} marker the engine resolved it against this request's
        // common frame before execute; otherwise the raw init-time string
        // is used.
        String prefix = keyPrefix;
        Object resolved = input.templatedParam("key_prefix");
        if (resolved instanceof String) {
            prefix = (String) resolved;
        }
        String key = prefix + buildKeySuffix(input, commonInput());

        try (Jedis jedis = pool.getResource()) {
            switch (dataType) {
                case "set": {
                    Set<String> members = jedis.smembers(key);
                    if (members != null && !members.isEmpty()) {
                        output.setCommon(resultField, new ArrayList<>(members));
                        output.setCommon(cacheHitField, true);
                    } else {
                        output.setCommon(cacheHitField, false);
                    }
                    break;
                }
                case "list": {
                    List<String> vals = jedis.lrange(key, 0, -1);
                    if (vals != null && !vals.isEmpty()) {
                        output.setCommon(resultField, vals);
                        output.setCommon(cacheHitField, true);
                    } else {
                        output.setCommon(cacheHitField, false);
                    }
                    break;
                }
                case "string": {
                    String val = jedis.get(key);
                    if (val != null && !val.isEmpty()) {
                        output.setCommon(resultField, val);
                        output.setCommon(cacheHitField, true);
                    } else {
                        output.setCommon(cacheHitField, false);
                    }
                    break;
                }
                default:
                    throw new IllegalArgumentException("transform_redis_get: unsupported data_type \"" + dataType + "\"");
            }
        } catch (IllegalArgumentException e) {
            throw new PineErrors.OperatorException(e.getMessage(), e);
        } catch (Exception e) {
            String redisCmd;
            switch (dataType) {
                case "set":
                    redisCmd = "SMembers";
                    break;
                case "list":
                    redisCmd = "LRange";
                    break;
                default:
                    redisCmd = "Get";
                    break;
            }
            output.setWarning(new PineErrors.OperatorException(
                    "transform_redis_get: " + redisCmd + "(" + key + "): " + e.getMessage(), e));
            if (failOnError) {
                throw new PineErrors.OperatorException("transform_redis_get: " + redisCmd + "(" + key + "): " + e.getMessage(), e);
            }
            output.setCommon(cacheHitField, false);
        }
    }

    /**
     * Borrows a {@link JedisPool} from a redis_connection resource by name.
     * Returns null when no provider is injected, the resource is missing, or its
     * value is not a RedisConnResource — in which case the caller degrades (a get
     * treats it as a cache miss; a set becomes a no-op), mirroring Go's borrowRedis.
     */
    static JedisPool borrowPool(ResourceProvider provider, String resourceName) {
        if (provider == null) {
            return null;
        }
        ResourceProvider.GetResult r = provider.get(resourceName);
        if (!r.exists()) {
            return null;
        }
        Object v = r.value();
        return (v instanceof RedisConnResource) ? ((RedisConnResource) v).pool() : null;
    }

    static String buildKeySuffix(OperatorInput input, List<String> fields) {
        if (fields.isEmpty()) return "";
        if (fields.size() == 1) return sprintValue(input.common(fields.get(0)));
        StringBuilder sb = new StringBuilder();
        for (int i = 0; i < fields.size(); i++) {
            if (i > 0) sb.append(':');
            sb.append(sprintValue(input.common(fields.get(i))));
        }
        return sb.toString();
    }

    static String sprintValue(Object v) {
        return GoFormat.sprint(v);
    }
}
