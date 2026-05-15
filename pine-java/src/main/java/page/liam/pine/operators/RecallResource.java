package page.liam.pine.operators;

import page.liam.pine.*;

import java.util.List;
import java.util.Map;

public class RecallResource extends AbstractOperator implements ResourceAware {
    private String resourceName;
    private ResourceProvider resourceProvider;

    @Override
    public void init(Map<String, Object> params) throws Exception {
        resourceName = (String) params.get("resource_name");
    }

    @Override
    public void setResourceProvider(ResourceProvider provider) {
        this.resourceProvider = provider;
    }

    @Override
    @SuppressWarnings("unchecked")
    public void execute(OperatorInput input, OperatorOutput output) throws Exception {
        if (resourceProvider == null) {
            throw new IllegalStateException("recall_resource: no resource provider");
        }
        Object raw = resourceProvider.get(resourceName);
        if (raw == null) {
            throw new IllegalStateException("recall_resource: resource \"" + resourceName + "\" not found");
        }

        List<?> items;
        if (raw instanceof List) {
            items = (List<?>) raw;
        } else {
            throw new IllegalStateException("recall_resource: resource \"" + resourceName + "\" is not a List");
        }

        for (Object item : items) {
            if (item instanceof Map) {
                Map<String, Object> m = (Map<String, Object>) item;
                output.addItem(new java.util.LinkedHashMap<>(m));
            } else {
                throw new IllegalStateException("recall_resource: item is not a Map");
            }
        }
    }
}
