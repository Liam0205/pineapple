package page.liam.pine;

import java.util.*;

/**
 * Separates input fields into strict (error on nil/missing),
 * defaulted (substitute default on nil/missing), and
 * nullable (missing -> error, explicit nil -> pass through).
 * Default mode is Nullable. Strict and Defaulted are opt-in.
 * Computed once at engine build time.
 */
public class InputFieldSpec {
    public final List<String> strictCommon;
    public final List<DefaultedField> defaultedCommon;
    public final List<String> nullableCommon;
    public final List<String> strictItem;
    public final List<DefaultedField> defaultedItem;
    public final List<String> nullableItem;

    public InputFieldSpec(List<String> strictCommon, List<DefaultedField> defaultedCommon,
                          List<String> nullableCommon,
                          List<String> strictItem, List<DefaultedField> defaultedItem,
                          List<String> nullableItem) {
        this.strictCommon = strictCommon;
        this.defaultedCommon = defaultedCommon;
        this.nullableCommon = nullableCommon;
        this.strictItem = strictItem;
        this.defaultedItem = defaultedItem;
        this.nullableItem = nullableItem;
    }

    /**
     * Pairs a field name with its pre-known default value.
     */
    public static class DefaultedField {
        public final String name;
        public final Object defaultValue;

        public DefaultedField(String name, Object defaultValue) {
            this.name = name;
            this.defaultValue = defaultValue;
        }
    }

    /**
     * Creates the InputFieldSpec from metadata, defaults, strict lists, and skip fields.
     * Default mode is Nullable (missing -> error, nil -> pass through).
     * Strict and Defaulted are opt-in.
     * Fields in the defaults map become "defaulted" (substitute default on nil/missing).
     * Fields in the strict list become "strict" (error on nil/missing).
     * Remaining fields become "nullable" (missing -> error, explicit nil -> pass through).
     */
    public static InputFieldSpec compute(Config.Metadata meta,
                                         Map<String, Object> commonDefaults,
                                         Map<String, Object> itemDefaults,
                                         List<String> strictCommonList,
                                         List<String> strictItemList,
                                         List<String> skip) {
        Set<String> skipSet = new HashSet<>(skip);
        Set<String> strictCommonSet = new HashSet<>(strictCommonList);
        Set<String> strictItemSet = new HashSet<>(strictItemList);

        List<String> strictCommon = new ArrayList<>();
        List<DefaultedField> defaultedCommon = new ArrayList<>();
        List<String> nullableCommon = new ArrayList<>();
        for (String field : meta.commonInput) {
            if (skipSet.contains(field)) {
                continue;
            }
            if (commonDefaults.containsKey(field)) {
                defaultedCommon.add(new DefaultedField(field, commonDefaults.get(field)));
            } else if (strictCommonSet.contains(field)) {
                strictCommon.add(field);
            } else {
                nullableCommon.add(field);
            }
        }

        List<String> strictItem = new ArrayList<>();
        List<DefaultedField> defaultedItem = new ArrayList<>();
        List<String> nullableItem = new ArrayList<>();
        for (String field : meta.itemInput) {
            if (itemDefaults.containsKey(field)) {
                defaultedItem.add(new DefaultedField(field, itemDefaults.get(field)));
            } else if (strictItemSet.contains(field)) {
                strictItem.add(field);
            } else {
                nullableItem.add(field);
            }
        }

        return new InputFieldSpec(
                Collections.unmodifiableList(strictCommon),
                Collections.unmodifiableList(defaultedCommon),
                Collections.unmodifiableList(nullableCommon),
                Collections.unmodifiableList(strictItem),
                Collections.unmodifiableList(defaultedItem),
                Collections.unmodifiableList(nullableItem)
        );
    }
}
