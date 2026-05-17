package page.liam.pine;

import java.util.*;

public class OperatorOutput {
    private final Map<String, Object> commonWrites = new LinkedHashMap<>();
    private final Map<Integer, Map<String, Object>> itemWrites = new HashMap<>();
    private final List<Map<String, Object>> addedItems = new ArrayList<>();
    private final Set<Integer> removedItems = new HashSet<>();
    private List<Integer> itemOrder;
    private Exception warning;

    public void setWarning(Exception w) {
        if (this.warning == null) {
            this.warning = w;
        }
    }

    public Exception getWarning() {
        return warning;
    }

    public void setCommon(String field, Object value) {
        commonWrites.put(field, value);
    }

    public void setItem(int index, String field, Object value) {
        itemWrites.computeIfAbsent(index, k -> new LinkedHashMap<>()).put(field, value);
    }

    public void addItem(Map<String, Object> fields) {
        addedItems.add(fields);
    }

    public void removeItem(int index) {
        removedItems.add(index);
    }

    public void setItemOrder(List<Integer> order) {
        this.itemOrder = order;
    }

    public Map<String, Object> getCommonWrites() {
        return commonWrites;
    }

    public Map<Integer, Map<String, Object>> getItemWrites() {
        return itemWrites;
    }

    public List<Map<String, Object>> getAddedItems() {
        return addedItems;
    }

    public Set<Integer> getRemovedItems() {
        return removedItems;
    }

    public List<Integer> getItemOrder() {
        return itemOrder;
    }
}
