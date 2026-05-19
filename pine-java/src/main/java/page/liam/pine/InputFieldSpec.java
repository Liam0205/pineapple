package page.liam.pine;

import java.util.*;

/**
 * Separates input fields into strict (error on nil/missing) and
 * defaulted (substitute default on nil/missing). Computed once at engine build time.
 */
public class InputFieldSpec {
    public final List<String> strictCommon;
    public final List<DefaultedField> defaultedCommon;
    public final List<String> strictItem;
    public final List<DefaultedField> defaultedItem;

    public InputFieldSpec(List<String> strictCommon, List<DefaultedField> defaultedCommon,
                          List<String> strictItem, List<DefaultedField> defaultedItem) {
        this.strictCommon = strictCommon;
        this.defaultedCommon = defaultedCommon;
        this.strictItem = strictItem;
        this.defaultedItem = defaultedItem;
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
     * Creates the InputFieldSpec from metadata, defaults, and skip fields.
     * Fields in the defaults map become "defaulted" (substitute default on nil/missing).
     * Fields NOT in defaults become "strict" (error on nil/missing).
     */
    public static InputFieldSpec compute(Config.Metadata meta,
                                         Map<String, Object> commonDefaults,
                                         Map<String, Object> itemDefaults,
                                         List<String> skip) {
        Set<String> skipSet = new HashSet<>(skip);

        List<String> strictCommon = new ArrayList<>();
        List<DefaultedField> defaultedCommon = new ArrayList<>();
        for (String field : meta.commonInput) {
            if (skipSet.contains(field)) {
                continue;
            }
            if (commonDefaults.containsKey(field)) {
                defaultedCommon.add(new DefaultedField(field, commonDefaults.get(field)));
            } else {
                strictCommon.add(field);
            }
        }

        List<String> strictItem = new ArrayList<>();
        List<DefaultedField> defaultedItem = new ArrayList<>();
        for (String field : meta.itemInput) {
            if (itemDefaults.containsKey(field)) {
                defaultedItem.add(new DefaultedField(field, itemDefaults.get(field)));
            } else {
                strictItem.add(field);
            }
        }

        return new InputFieldSpec(
                Collections.unmodifiableList(strictCommon),
                Collections.unmodifiableList(defaultedCommon),
                Collections.unmodifiableList(strictItem),
                Collections.unmodifiableList(defaultedItem)
        );
    }
}
