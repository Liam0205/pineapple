package page.liam.pine.operators;

import page.liam.pine.*;
import redis.clients.jedis.Jedis;
import redis.clients.jedis.JedisPool;
import redis.clients.jedis.JedisPoolConfig;
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
 */
public class TransformRedisSet extends AbstractOperator implements ConcurrentSafe {
    private JedisPool pool;
    private String keyPrefix;
    private String dataType = "string";
    private int ttlSeconds;
    private boolean failOnError;

    @Override
    public void init(Map<String, Object> params) {
        String addr = (String) params.getOrDefault("redis_addr", "");
        String password = (String) params.getOrDefault("redis_password", "");
        int db = toInt(params.getOrDefault("redis_db", 0));
        keyPrefix = (String) params.getOrDefault("key_prefix", "");
        Object dt = params.get("data_type");
        if (dt instanceof String && !((String) dt).isEmpty()) {
            dataType = (String) dt;
        }
        ttlSeconds = toInt(params.getOrDefault("ttl", 0));
        Object foe = params.get("fail_on_error");
        if (foe instanceof Boolean) failOnError = (Boolean) foe;

        if (!addr.isEmpty()) {
            String host = addr.contains(":") ? addr.substring(0, addr.indexOf(':')) : addr;
            int port = addr.contains(":") ? Integer.parseInt(addr.substring(addr.indexOf(':') + 1)) : 6379;
            JedisPoolConfig cfg = new JedisPoolConfig();
            if (password.isEmpty()) {
                pool = new JedisPool(cfg, host, port, 2000, null, db);
            } else {
                pool = new JedisPool(cfg, host, port, 2000, password, db);
            }
        }
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) throws PineErrors.OperatorException {
        if (pool == null) return;

        int n = commonInput.size();
        if (n < 2) {
            throw new IllegalArgumentException("transform_redis_set: common_input must have at least 2 fields (key fields + value field)");
        }

        String key = keyPrefix + TransformRedisGet.buildKeySuffix(input, commonInput.subList(0, n - 1));
        Object value = input.common(commonInput.get(n - 1));

        try (Jedis jedis = pool.getResource()) {
            switch (dataType) {
                case "set": {
                    List<String> members = toStringList(value);
                    if (members == null || members.isEmpty()) return;
                    Pipeline pipe = jedis.pipelined();
                    pipe.del(key);
                    pipe.sadd(key, members.toArray(new String[0]));
                    if (ttlSeconds > 0) pipe.expire(key, ttlSeconds);
                    pipe.sync();
                    break;
                }
                case "list": {
                    List<String> members = toStringList(value);
                    if (members == null || members.isEmpty()) return;
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

    private static int toInt(Object v) {
        if (v instanceof Number) return ((Number) v).intValue();
        return 0;
    }
}
