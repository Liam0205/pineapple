package page.liam.pine;

import java.util.List;
import java.util.Map;

/**
 * Go-compatible type name formatting for error messages.
 * Mirrors Go's fmt.Sprintf("%T", v) for JSON-decoded values.
 */
public final class GoTypeNames {
    private GoTypeNames() {}

    public static String of(Object v) {
        if (v == null) return "<nil>";
        if (v instanceof Boolean) return "bool";
        if (v instanceof String) return "string";
        if (v instanceof Number) return "float64";
        if (v instanceof List) return "[]interface {}";
        if (v instanceof Map) return "map[string]interface {}";
        return v.getClass().getSimpleName();
    }
}
