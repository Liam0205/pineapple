package page.liam.pine;

/**
 * Read-only interface for accessing named resources.
 * Operators obtain resource values through this interface.
 */
public interface ResourceProvider {
    /**
     * Returns the current value for a named resource,
     * or null if the resource does not exist or is not yet loaded.
     */
    Object get(String name);
}
