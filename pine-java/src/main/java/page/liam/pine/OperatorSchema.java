package page.liam.pine;

import java.util.Collections;
import java.util.Map;

public class OperatorSchema {
    public final String name;
    public final OperatorType type;
    public final String description;
    public final Map<String, ParamSpec> params;

    public OperatorSchema(String name, OperatorType type, String description, Map<String, ParamSpec> params) {
        this.name = name;
        this.type = type;
        this.description = description;
        this.params = params != null ? Collections.unmodifiableMap(params) : Collections.emptyMap();
    }
}
