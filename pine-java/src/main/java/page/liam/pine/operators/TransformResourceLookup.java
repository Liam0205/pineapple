package page.liam.pine.operators;

import page.liam.pine.*;
import page.liam.pine.GoFormat;

import java.util.Map;

/**
 * Operator: transform_resource_lookup
 * Metadata contract
 *   ItemInput:  [<lookup_key>]
 *   ItemOutput: [<output_field>]
 */
public class TransformResourceLookup extends AbstractOperator implements ConcurrentSafe, ResourceAware {
    private String resourceName;
    private String lookupKey;
    private String outputField;
    private Object defaultValue;
    private boolean hasDefault;
    private ResourceProvider resourceProvider;

    @Override
    public void init(Map<String, Object> params) {
        resourceName = (String) params.get("resource_name");
        lookupKey = (String) params.get("lookup_key");
        outputField = (String) params.get("output_field");
        if (params.containsKey("default_value")) {
            defaultValue = params.get("default_value");
            hasDefault = true;
        }
    }

    @Override
    public void setResourceProvider(ResourceProvider provider) {
        this.resourceProvider = provider;
    }

    @Override
    @SuppressWarnings("unchecked")
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) throws PineErrors.OperatorException {
        if (resourceProvider == null) {
            throw new PineErrors.OperatorException("transform_resource_lookup: no resource provider in context");
        }
        ResourceProvider.GetResult result = resourceProvider.get(resourceName);
        if (!result.exists()) {
            throw new PineErrors.OperatorException("transform_resource_lookup: resource \"" + resourceName + "\" not found");
        }
        Object raw = result.value();
        if (!(raw instanceof Map)) {
            throw new PineErrors.OperatorException("transform_resource_lookup: resource \"" + resourceName + "\" is " +
                    (raw == null ? "null" : raw.getClass().getSimpleName()) + ", want map[string]any");
        }
        Map<String, Object> table = (Map<String, Object>) raw;

        for (int i = 0; i < input.itemCount(); i++) {
            Object keyRaw = input.item(i, lookupKey);
            if (keyRaw == null) {
                if (hasDefault) {
                    output.setItem(i, outputField, defaultValue);
                }
                continue;
            }
            String key = toKeyString(keyRaw);
            if (table.containsKey(key)) {
                output.setItem(i, outputField, table.get(key));
            } else if (hasDefault) {
                output.setItem(i, outputField, defaultValue);
            }
        }
    }

    private static String toKeyString(Object v) {
        if (v instanceof String) return (String) v;
        if (v instanceof Number) {
            double d = ((Number) v).doubleValue();
            if (d == (long) d) {
                return Long.toString((long) d);
            }
            return GoFormat.formatFloatF(d);
        }
        return String.valueOf(v);
    }
}
