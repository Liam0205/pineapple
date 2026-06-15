package page.liam.pine;

public class ParamSpec {
    public final String type;
    public final boolean required;
    public final Object defaultValue;
    public final String description;
    /**
     * Templatable opts this param into per-request {@code {{field}}} interpolation (issue #74).
     * When true the Apple compiler accepts a templated string value for this param and
     * auto-injects the referenced common fields into the operator's common_input; the engine
     * resolves and coerces the value, attaches the map to OperatorInput, and operators read
     * it via {@code input.templatedParam(name)}. Only string / int64 / float64 / bool
     * scalars are supported.
     */
    public final boolean templatable;

    public ParamSpec(String type, boolean required, Object defaultValue, String description) {
        this(type, required, defaultValue, description, false);
    }

    public ParamSpec(String type, boolean required, Object defaultValue, String description,
                     boolean templatable) {
        this.type = type;
        this.required = required;
        this.defaultValue = defaultValue;
        this.description = description;
        this.templatable = templatable;
    }

    public static ParamSpec required(String type, String description) {
        return new ParamSpec(type, true, null, description, false);
    }

    public static ParamSpec optional(String type, Object defaultValue, String description) {
        return new ParamSpec(type, false, defaultValue, description, false);
    }

    public static ParamSpec requiredTemplatable(String type, String description) {
        return new ParamSpec(type, true, null, description, true);
    }

    public static ParamSpec optionalTemplatable(String type, String description) {
        return new ParamSpec(type, false, null, description, true);
    }

    public static ParamSpec optionalTemplatable(String type, Object defaultValue, String description) {
        return new ParamSpec(type, false, defaultValue, description, true);
    }
}
