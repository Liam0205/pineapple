package page.liam.pine.operators;

import page.liam.pine.*;
import redis.clients.jedis.Jedis;
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
        ttlSeconds = 0;
        if (params.containsKey("ttl")) {
            // containsKey gates the lookup so an explicit JSON null falls
            // into the else branch and errors out, matching Go's `v, ok :=
            // params["ttl"]` + nil-in-default semantics. params.get alone
            // cannot distinguish "key missing" from "key present, value
            // null" — the latter would silently leave ttlSeconds=0,
            // re-opening the hand-edited-garbage hole this commit closes.
            Object ttlRaw = params.get("ttl");
            if (ttlRaw instanceof Number) {
                ttlSeconds = ((Number) ttlRaw).intValue();
            } else if (ttlRaw instanceof String s) {
                // Only a bare {{field}} marker is accepted here; engine
                // resolves it per-request at execute time. A non-marker
                // string would otherwise be silently coerced to 0 by
                // params.getInt's default-value fallback.
                if (!TemplateResolver.isBareMarker(s)) {
                    throw new IllegalArgumentException("transform_redis_set: ttl must be numeric, got " + GoTypeNames.of(ttlRaw));
                }
            } else {
                throw new IllegalArgumentException("transform_redis_set: ttl must be numeric, got " + GoTypeNames.of(ttlRaw));
            }
        }
        Object foe = params.get("fail_on_error");
        if (foe instanceof Boolean) failOnError = (Boolean) foe;
    }

    @Override
    public void setResourceProvider(ResourceProvider provider) {
        this.resourceProvider = provider;
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) throws PineErrors.OperatorException {
        RedisConnResource resource = TransformRedisGet.borrowResource(resourceProvider, resourceName);
        if (resource == null) return;

        int n = commonInput().size();
        if (n < 2) {
            throw new PineErrors.OperatorException("transform_redis_set: common_input must have at least 2 fields (key fields + value field)");
        }

        // key_prefix and ttl are both templatable (#74). When the DSL
        // configured a {{field}} marker the engine resolved it against
        // this request's common frame before execute; otherwise the
        // init-time value is used. The String / Long type checks below
        // are unreachable: BuildTemplatedParamPlan rejects mismatched
        // declared types and TemplateResolver normalizes through
        // GoFormat.sprint / ParseInt. Kept as defense in depth.
        String prefix = keyPrefix;
        Object resolvedPrefix = input.templatedParam("key_prefix");
        if (resolvedPrefix instanceof String) {
            prefix = (String) resolvedPrefix;
        }
        int ttl = ttlSeconds;
        Object resolvedTtl = input.templatedParam("ttl");
        if (resolvedTtl instanceof Long) {
            ttl = ((Long) resolvedTtl).intValue();
        } else if (resolvedTtl instanceof Integer) {
            ttl = (Integer) resolvedTtl;
        }

        String key = prefix + TransformRedisGet.buildKeySuffix(input, commonInput().subList(0, n - 1));
        Object value = input.common(commonInput().get(n - 1));

        // Each branch records under the dominant Redis verb of the pipeline:
        // SADD / RPUSH / SET. The DEL+EXPIRE that ride along are not
        // separately tracked — granularity beyond the dominant op was not
        // exercised at incident time and would over-fragment the labels.
        final int finalTtl = ttl;
        try {
            switch (dataType) {
                case "set": {
                    List<String> members = toStringList(value);
                    if (members == null || members.isEmpty()) {
                        if (members == null) logf("transform_redis_set: value for key %s is not []string", key);
                        return;
                    }
                    resource.runCommand("SADD", jedis -> {
                        Pipeline pipe = jedis.pipelined();
                        pipe.del(key);
                        pipe.sadd(key, members.toArray(new String[0]));
                        if (finalTtl > 0) pipe.expire(key, finalTtl);
                        pipe.sync();
                        return null;
                    });
                    break;
                }
                case "list": {
                    List<String> members = toStringList(value);
                    if (members == null || members.isEmpty()) {
                        if (members == null) logf("transform_redis_set: value for key %s is not []string", key);
                        return;
                    }
                    resource.runCommand("RPUSH", jedis -> {
                        Pipeline pipe = jedis.pipelined();
                        pipe.del(key);
                        pipe.rpush(key, members.toArray(new String[0]));
                        if (finalTtl > 0) pipe.expire(key, finalTtl);
                        pipe.sync();
                        return null;
                    });
                    break;
                }
                case "string": {
                    if (!(value instanceof String)) {
                        logf("transform_redis_set: value for key %s is not string", key);
                        return;
                    }
                    final String stringValue = (String) value;
                    resource.runCommand("SET", jedis -> {
                        if (finalTtl > 0) {
                            jedis.setex(key, finalTtl, stringValue);
                        } else {
                            jedis.set(key, stringValue);
                        }
                        return null;
                    });
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
            logf("transform_redis_set: write key %s: %s", key, e.getMessage());
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
