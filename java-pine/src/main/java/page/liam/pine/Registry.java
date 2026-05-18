package page.liam.pine;

import java.util.HashMap;
import java.util.Map;
import java.util.function.Supplier;

public class Registry {
    private static final Map<String, OperatorEntry> operators = new HashMap<>();

    public static void register(String name, OperatorType type, Supplier<Operator> factory) {
        if (operators.containsKey(name)) {
            throw new IllegalStateException("operator already registered: " + name);
        }
        operators.put(name, new OperatorEntry(type, factory));
    }

    public static Operator buildOperator(String typeName, Map<String, Object> params) throws Exception {
        OperatorEntry entry = operators.get(typeName);
        if (entry == null) {
            throw new IllegalArgumentException("unknown operator: " + typeName);
        }
        Operator op = entry.factory.get();
        op.init(params);
        return op;
    }

    public static OperatorType getType(String typeName) {
        OperatorEntry entry = operators.get(typeName);
        return entry != null ? entry.type : null;
    }

    private static class OperatorEntry {
        final OperatorType type;
        final Supplier<Operator> factory;

        OperatorEntry(OperatorType type, Supplier<Operator> factory) {
            this.type = type;
            this.factory = factory;
        }
    }
}
