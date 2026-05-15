package page.liam.pine;

/**
 * Interface for operators that need access to managed resources.
 * The engine injects the provider before each execution.
 */
public interface ResourceAware {
    void setResourceProvider(ResourceProvider provider);
}
