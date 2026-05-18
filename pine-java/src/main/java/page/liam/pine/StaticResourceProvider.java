package page.liam.pine;

import java.util.Collections;
import java.util.Map;

/**
 * A trivial ResourceProvider backed by a fixed map. Useful for testing.
 */
public class StaticResourceProvider implements ResourceProvider {

    private final Map<String, Object> data;

    public StaticResourceProvider(Map<String, Object> data) {
        this.data = data != null ? data : Collections.emptyMap();
    }

    @Override
    public Object get(String name) {
        return data.get(name);
    }
}
