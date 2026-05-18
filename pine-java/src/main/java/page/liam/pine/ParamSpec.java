package page.liam.pine;

public class ParamSpec {
    public final String type;
    public final boolean required;
    public final Object defaultValue;
    public final String description;

    public ParamSpec(String type, boolean required, Object defaultValue, String description) {
        this.type = type;
        this.required = required;
        this.defaultValue = defaultValue;
        this.description = description;
    }

    public static ParamSpec required(String type, String description) {
        return new ParamSpec(type, true, null, description);
    }

    public static ParamSpec optional(String type, Object defaultValue, String description) {
        return new ParamSpec(type, false, defaultValue, description);
    }
}
