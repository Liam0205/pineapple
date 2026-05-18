package page.liam.pine;

import java.util.*;

public class OperatorOutput {
    private Map<String, Object> commonWrites;
    private Map<Integer, Map<String, Object>> itemWrites;
    private List<Map<String, Object>> addedItems;
    private Set<Integer> removedItems;
    private List<Integer> itemOrder;
    private Exception warning;

    public void setWarning(Exception w) {
        this.warning = w;
    }

    public Exception getWarning() {
        return warning;
    }

    public void setCommon(String field, Object value) {
        if (commonWrites == null) {
            commonWrites = new LinkedHashMap<>();
        }
        commonWrites.put(field, value);
    }

    public void setItem(int index, String field, Object value) {
        if (itemWrites == null) {
            itemWrites = new HashMap<>();
        }
        itemWrites.computeIfAbsent(index, k -> new LinkedHashMap<>()).put(field, value);
    }

    public void addItem(Map<String, Object> fields) {
        if (addedItems == null) {
            addedItems = new ArrayList<>();
        }
        addedItems.add(fields);
    }

    public void removeItem(int index) {
        if (removedItems == null) {
            removedItems = new HashSet<>();
        }
        removedItems.add(index);
    }

    public void setItemOrder(List<Integer> order) {
        this.itemOrder = order;
    }

    public Map<String, Object> getCommonWrites() {
        return commonWrites != null ? commonWrites : Collections.emptyMap();
    }

    public Map<Integer, Map<String, Object>> getItemWrites() {
        return itemWrites != null ? itemWrites : Collections.emptyMap();
    }

    public List<Map<String, Object>> getAddedItems() {
        return addedItems != null ? addedItems : Collections.emptyList();
    }

    public Set<Integer> getRemovedItems() {
        return removedItems != null ? removedItems : Collections.emptySet();
    }

    public List<Integer> getItemOrder() {
        return itemOrder;
    }
}
