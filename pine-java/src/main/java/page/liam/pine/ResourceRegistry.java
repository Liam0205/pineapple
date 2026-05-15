package page.liam.pine;

import java.util.*;
import java.util.concurrent.ConcurrentHashMap;

/**
 * Static registry for ResourceSchema entries, mirroring Go's pkg/resource.Register/All.
 * Business code registers schemas (alongside FetcherFactory in ResourceManager) so that
 * codegen can enumerate resource types without external JSON.
 */
public final class ResourceRegistry {
    private ResourceRegistry() {}

    private static final Map<String, Codegen.ResourceSchema> registry = new ConcurrentHashMap<>();

    /**
     * Registers a ResourceSchema. Typically called from static initializers alongside
     * ResourceManager.registerFactory(). Panics on duplicate name.
     */
    public static void register(Codegen.ResourceSchema schema) {
        if (registry.putIfAbsent(schema.name, schema) != null) {
            throw new IllegalStateException("resource: duplicate resource schema \"" + schema.name + "\"");
        }
    }

    /**
     * Returns all registered ResourceSchemas, sorted by name.
     * Used by Codegen in --schema-from-registry mode.
     */
    public static List<Codegen.ResourceSchema> all() {
        List<Codegen.ResourceSchema> result = new ArrayList<>(registry.values());
        result.sort(Comparator.comparing(s -> s.name));
        return result;
    }

    /** Clears the registry. For testing only. */
    public static void reset() {
        registry.clear();
    }
}
