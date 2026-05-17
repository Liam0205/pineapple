package page.liam.pine.operators;

import page.liam.pine.*;

import java.util.List;
import java.util.Map;

/**
 * Operator: recall_resource
 * Metadata contract
 *   ItemOutput: [<fields present in the resource items>]
 */
public class RecallResource extends AbstractOperator implements ResourceAware {
    private String resourceName;
    private ResourceProvider resourceProvider;

    @Override
    public void init(OperatorParams params) {
        resourceName = (String) params.get("resource_name");
    }

    @Override
    public void setResourceProvider(ResourceProvider provider) {
        this.resourceProvider = provider;
    }

    @Override
    @SuppressWarnings("unchecked")
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) throws PineErrors.OperatorException {
        if (resourceProvider == null) {
            throw new PineErrors.OperatorException("recall_resource: no resource provider in context");
        }
        ResourceProvider.GetResult result = resourceProvider.get(resourceName);
        if (!result.exists()) {
            throw new PineErrors.OperatorException("recall_resource: resource \"" + resourceName + "\" not found");
        }

        Object raw = result.value();
        List<?> items;
        if (raw instanceof List) {
            items = (List<?>) raw;
        } else {
            throw new PineErrors.OperatorException("recall_resource: resource \"" + resourceName + "\" is " +
                    (raw == null ? "null" : raw.getClass().getSimpleName()) + ", want []map[string]any");
        }

        for (int i = 0; i < items.size(); i++) {
            Object item = items.get(i);
            if (item instanceof Map) {
                Map<String, Object> m = (Map<String, Object>) item;
                output.addItem(new java.util.LinkedHashMap<>(m));
            } else {
                throw new PineErrors.OperatorException("recall_resource: items[" + i + "] is " +
                        (item == null ? "null" : item.getClass().getSimpleName()) + ", want Map");
            }
        }
    }
}
