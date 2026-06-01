package page.liam.pine.operators;

import page.liam.pine.*;
import redis.clients.jedis.Jedis;
import redis.clients.jedis.JedisPool;
import redis.clients.jedis.Pipeline;

import java.util.*;
import java.util.stream.Collectors;

/**
 * Operator: transform_redis_set
 * Metadata contract
 *   CommonInput:  [<key_suffix_fields...>, <value_field>]
 *   CommonOutput: []
 *   ItemInput:    []
 *   ItemOutput:   []
 *
 * <p>The Redis connection pool is owned by the ResourceManager (resource type
 * redis_connection), not the operator: the pool is borrowed per request via
 * resource_name. Multiple Redis operators referencing the same resource_name
 * share one pool.
 */
public class TransformRedisSet extends AbstractOperator implements ConcurrentSafe, ResourceAware {
    private String resourceName;
    private ResourceProvider resourceProvider;
    private String keyPrefix;
    private String dataType = "string";
    private int ttlSeconds;
    private boolean failOnError;

    @Override
    public void init(OperatorParams params) {
        resourceName = params.getString("resource_name", "");
        keyPrefix = params.getString("key_prefix", "");
        Object dt = params.get("data_type");
        if (dt instanceof String && !((String) dt).isEmpty()) {
            dataType = (String) dt;
        }
        ttlSeconds = params.getInt("ttl", 0);
        Object foe = params.get("fail_on_error");
        if (foe instanceof Boolean) failOnError = (Boolean) foe;
    }

    @Override
    public void setResourceProvider(ResourceProvider provider) {
        this.resourceProvider = provider;
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) throws PineErrors.OperatorException {
        JedisPool pool = TransformRedisGet.borrowPool(resourceProvider, resourceName);
        if (pool == null) return;

        int n = commonInput().size();
        if (n < 2) {
            throw new PineErrors.OperatorException("transform_redis_set: common_input must have at least 2 fields (key fields + value field)");
        }

        String key = keyPrefix + TransformRedisGet.buildKeySuffix(input, commonInput().subList(0, n - 1));
        Object value = input.common(commonInput().get(n - 1));

        try (Jedis jedis = pool.getResource()) {
            switch (dataType) {
                case "set": {
                    List<String> members = toStringList(value);
                    if (members == null || members.isEmpty()) {
                        if (members == null) System.err.printf("transform_redis_set: value for key %s is not []string%n", key);
                        return;
                    }
                    Pipeline pipe = jedis.pipelined();
                    pipe.del(key);
                    pipe.sadd(key, members.toArray(new String[0]));
                    if (ttlSeconds > 0) pipe.expire(key, ttlSeconds);
                    pipe.sync();
                    break;
                }
                case "list": {
                    List<String> members = toStringList(value);
                    if (members == null || members.isEmpty()) {
                        if (members == null) System.err.printf("transform_redis_set: value for key %s is not []string%n", key);
                        return;
                    }
                    Pipeline pipe = jedis.pipelined();
                    pipe.del(key);
                    pipe.rpush(key, members.toArray(new String[0]));
                    if (ttlSeconds > 0) pipe.expire(key, ttlSeconds);
                    pipe.sync();
                    break;
                }
                case "string": {
                    if (!(value instanceof String)) {
                        System.err.printf("transform_redis_set: value for key %s is not string%n", key);
                        return;
                    }
                    if (ttlSeconds > 0) {
                        jedis.setex(key, ttlSeconds, (String) value);
                    } else {
                        jedis.set(key, (String) value);
                    }
                    break;
                }
                default:
                    throw new IllegalArgumentException("transform_redis_set: unsupported data_type \"" + dataType + "\"");
            }
        } catch (IllegalArgumentException e) {
            throw new PineErrors.OperatorException(e.getMessage(), e);
        } catch (Exception e) {
            if (failOnError) {
                throw new PineErrors.OperatorException("transform_redis_set: write key " + key + ": " + e.getMessage(), e);
            }
            System.err.printf("transform_redis_set: write key %s: %s%n", key, e.getMessage());
            output.setWarning(new Exception("transform_redis_set: write key " + key + ": " + e.getMessage(), e));
        }
    }

    @SuppressWarnings("unchecked")
    private static List<String> toStringList(Object v) {
        if (v instanceof List) {
            return ((List<?>) v).stream().map(GoFormat::sprint).collect(Collectors.toList());
        }
        return null;
    }
}
