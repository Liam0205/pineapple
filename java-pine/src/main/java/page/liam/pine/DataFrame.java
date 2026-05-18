package page.liam.pine;

import java.util.*;

public class DataFrame {
    private Map<String, Object> common;
    private List<Map<String, Object>> items;

    public DataFrame(Map<String, Object> common, List<Map<String, Object>> items) {
        this.common = new LinkedHashMap<>(common != null ? common : Collections.emptyMap());
        this.items = new ArrayList<>();
        if (items != null) {
            for (Map<String, Object> item : items) {
                this.items.add(new LinkedHashMap<>(item));
            }
        }
    }

    public Object common(String field) {
        return common.get(field);
    }

    public void setCommon(String field, Object value) {
        common.put(field, value);
    }

    public int itemCount() {
        return items.size();
    }

    public Object item(int index, String field) {
        if (index < 0 || index >= items.size()) return null;
        return items.get(index).get(field);
    }

    public OperatorInput buildInput(List<String> commonFields, List<String> itemFields,
                                    Map<String, Object> commonDefaults, Map<String, Object> itemDefaults) {
        Map<String, Object> cs = new LinkedHashMap<>();
        for (String field : commonFields) {
            if (common.containsKey(field)) {
                Object v = common.get(field);
                if (v == null && commonDefaults.containsKey(field)) {
                    v = commonDefaults.get(field);
                }
                cs.put(field, v);
            } else if (commonDefaults.containsKey(field)) {
                cs.put(field, commonDefaults.get(field));
            }
        }

        List<Map<String, Object>> its = new ArrayList<>(items.size());
        for (Map<String, Object> item : items) {
            Map<String, Object> row = new LinkedHashMap<>();
            for (String field : itemFields) {
                if (item.containsKey(field)) {
                    Object v = item.get(field);
                    if (v == null && itemDefaults.containsKey(field)) {
                        v = itemDefaults.get(field);
                    }
                    row.put(field, v);
                } else if (itemDefaults.containsKey(field)) {
                    row.put(field, itemDefaults.get(field));
                }
            }
            its.add(row);
        }

        return new OperatorInput(cs, its);
    }

    public void applyOutput(OperatorOutput out, String opName, boolean recall) throws Exception {
        // 1. Common writes
        for (Map.Entry<String, Object> entry : out.getCommonWrites().entrySet()) {
            common.put(entry.getKey(), entry.getValue());
        }

        // 2. Item writes
        for (Map.Entry<Integer, Map<String, Object>> entry : out.getItemWrites().entrySet()) {
            int idx = entry.getKey();
            if (idx < 0 || idx >= items.size()) {
                throw new RuntimeException("SetItem index " + idx + " out of range [0, " + items.size() + ")");
            }
            items.get(idx).putAll(entry.getValue());
        }

        // 3. Removals
        Set<Integer> removed = out.getRemovedItems();
        if (!removed.isEmpty()) {
            for (int idx : removed) {
                if (idx < 0 || idx >= items.size()) {
                    throw new RuntimeException("RemoveItem index " + idx + " out of range [0, " + items.size() + ")");
                }
            }
            List<Map<String, Object>> surviving = new ArrayList<>();
            for (int i = 0; i < items.size(); i++) {
                if (!removed.contains(i)) {
                    surviving.add(items.get(i));
                }
            }
            items = surviving;
        }

        // 4. Reorder
        List<Integer> order = out.getItemOrder();
        if (order != null) {
            if (order.size() != items.size()) {
                throw new RuntimeException("SetItemOrder length " + order.size() + " does not match item count " + items.size());
            }
            List<Map<String, Object>> reordered = new ArrayList<>(order.size());
            for (int origIdx : order) {
                if (origIdx < 0 || origIdx >= items.size()) {
                    throw new RuntimeException("SetItemOrder index " + origIdx + " out of range [0, " + items.size() + ")");
                }
                reordered.add(items.get(origIdx));
            }
            items = reordered;
        }

        // 5. Additions
        for (Map<String, Object> added : out.getAddedItems()) {
            Map<String, Object> row = new LinkedHashMap<>(added);
            if (recall) {
                row.put("_source", opName);
            }
            items.add(row);
        }
    }

    public Map<String, Object> toResultCommon(List<String> commonOut) {
        Map<String, Object> result = new LinkedHashMap<>();
        for (String k : commonOut) {
            if (common.containsKey(k)) {
                result.put(k, common.get(k));
            }
        }
        return result;
    }

    public List<Map<String, Object>> toResultItems(List<String> itemOut) {
        List<Map<String, Object>> result = new ArrayList<>(items.size());
        for (Map<String, Object> item : items) {
            Map<String, Object> row = new LinkedHashMap<>();
            for (String k : itemOut) {
                if (item.containsKey(k)) {
                    row.put(k, item.get(k));
                }
            }
            result.add(row);
        }
        return result;
    }
}
