package page.liam.pine.operators;

import page.liam.pine.*;
import redis.clients.jedis.Jedis;
import redis.clients.jedis.JedisPool;
import redis.clients.jedis.JedisPoolConfig;

import java.util.*;

public class TransformRedisGet extends AbstractOperator implements ConcurrentSafe {
    private JedisPool pool;
    private String keyPrefix;
    private String dataType = "string";
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
        Object foe = params.get("fail_on_error");
        if (foe instanceof Boolean) {
            failOnError = (Boolean) foe;
        }

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
    public void execute(OperatorInput input, OperatorOutput output) throws Exception {
        String resultField = commonOutput.get(0);
        String cacheHitField = commonOutput.get(1);

        if (pool == null) {
            output.setCommon(cacheHitField, false);
            return;
        }

        String key = keyPrefix + buildKeySuffix(input, commonInput);

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
            throw e;
        } catch (Exception e) {
            output.setWarning(e);
            if (failOnError) {
                throw new RuntimeException("transform_redis_get: " + key + ": " + e.getMessage(), e);
            }
            output.setCommon(cacheHitField, false);
        }
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
        if (v == null) return "<nil>";
        if (v instanceof Number) {
            double d = ((Number) v).doubleValue();
            if (d == (long) d && !Double.isInfinite(d)) return Long.toString((long) d);
            return Double.toString(d);
        }
        return v.toString();
    }

    private static int toInt(Object v) {
        if (v instanceof Number) return ((Number) v).intValue();
        return 0;
    }
}
