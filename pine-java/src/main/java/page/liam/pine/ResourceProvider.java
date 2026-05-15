package page.liam.pine;

/**
 * Read-only interface for accessing named resources.
 * Operators obtain resource values through this interface.
 */
public interface ResourceProvider {

    class GetResult {
        private final Object value;
        private final boolean exists;

        public GetResult(Object value, boolean exists) {
            this.value = value;
            this.exists = exists;
        }

        public Object value() { return value; }
        public boolean exists() { return exists; }
    }

    /**
     * Returns the current value for a named resource.
     * Returns GetResult(null, false) if the resource does not exist or is not yet loaded.
     * Returns GetResult(null, true) if the resource exists but its value is null.
     */
    GetResult get(String name);
}
