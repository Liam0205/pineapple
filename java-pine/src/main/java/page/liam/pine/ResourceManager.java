package page.liam.pine;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;

import java.util.*;
import java.util.concurrent.*;
import java.util.logging.Level;
import java.util.logging.Logger;

/**
 * Manages a set of named resources with background refresh.
 * Implements ResourceProvider for direct injection into operators.
 *
 * <p>Usage:
 * <pre>
 *   ResourceManager mgr = new ResourceManager();
 *   mgr.register("myData", () -> fetchFromRemote(), 60);
 *   mgr.start();
 *   // ... use mgr as ResourceProvider ...
 *   mgr.stop();
 * </pre>
 */
public class ResourceManager implements ResourceProvider {

    private static final Logger LOG = Logger.getLogger(ResourceManager.class.getName());
    private static final long DEFAULT_INTERVAL_SECONDS = 600; // 10 minutes

    // --- Functional interfaces ---

    /** Fetches a resource value. Called by the background refresh loop. */
    @FunctionalInterface
    public interface Fetcher {
        Object fetch() throws Exception;
    }

    /** Creates a Fetcher from configuration params. Registered globally by plugin code. */
    @FunctionalInterface
    public interface FetcherFactory {
        Fetcher create(Map<String, Object> params) throws Exception;
    }

    // --- Global factory registry ---

    private static final Map<String, FetcherFactory> factories = new ConcurrentHashMap<>();

    /**
     * Registers a FetcherFactory for a given resource type name.
     * Typically called from static initializers. Panics on duplicate.
     */
    public static void registerFactory(String type, FetcherFactory factory) {
        if (factories.putIfAbsent(type, factory) != null) {
            throw new IllegalStateException("resource: duplicate factory type \"" + type + "\"");
        }
    }

    /**
     * Clears the global factory registry. For testing only.
     */
    public static void resetFactories() {
        factories.clear();
    }

    // --- Managed resource entry ---

    private static class ManagedResource {
        final String name;
        final Fetcher fetcher;
        final long intervalSeconds;
        volatile Object value;
        volatile boolean loaded;

        ManagedResource(String name, Fetcher fetcher, long intervalSeconds) {
            this.name = name;
            this.fetcher = fetcher;
            this.intervalSeconds = intervalSeconds;
        }
    }

    // --- Instance state ---

    private final Map<String, ManagedResource> resources = new LinkedHashMap<>();
    private ScheduledExecutorService executor;
    private boolean started;

    /**
     * Registers a named resource with its fetcher and refresh interval.
     * Must be called before start(). Throws on duplicate name.
     */
    public synchronized void register(String name, Fetcher fetcher, long intervalSeconds) {
        if (started) {
            throw new IllegalStateException("resource: register called after start");
        }
        if (resources.containsKey(name)) {
            throw new IllegalStateException("resource: duplicate resource name \"" + name + "\"");
        }
        if (intervalSeconds <= 0) {
            intervalSeconds = DEFAULT_INTERVAL_SECONDS;
        }
        resources.put(name, new ManagedResource(name, fetcher, intervalSeconds));
    }

    /**
     * Parses "resource_config" from the given JSON config and registers each resource
     * using the globally registered FetcherFactory. No-op if resource_config is absent or empty.
     * Must be called before start().
     */
    public void loadFromConfig(byte[] jsonConfig) throws Exception {
        ObjectMapper mapper = new ObjectMapper();
        JsonNode root = mapper.readTree(jsonConfig);
        JsonNode rcNode = root.get("resource_config");
        if (rcNode == null || rcNode.isNull() || rcNode.isEmpty()) {
            return;
        }

        Iterator<Map.Entry<String, JsonNode>> fields = rcNode.fields();
        while (fields.hasNext()) {
            Map.Entry<String, JsonNode> entry = fields.next();
            String name = entry.getKey();
            JsonNode cfg = entry.getValue();

            String type = cfg.has("type") ? cfg.get("type").asText() : "";
            long interval = cfg.has("interval") ? cfg.get("interval").asLong() : 0;

            Map<String, Object> params = new LinkedHashMap<>();
            JsonNode paramsNode = cfg.get("params");
            if (paramsNode != null && paramsNode.isObject()) {
                @SuppressWarnings("unchecked")
                Map<String, Object> parsed = mapper.convertValue(paramsNode, Map.class);
                params.putAll(parsed);
            }

            FetcherFactory factory = factories.get(type);
            if (factory == null) {
                throw new IllegalArgumentException(
                        "resource: unknown fetcher type \"" + type + "\" for resource \"" + name + "\"");
            }

            Fetcher fetcher = factory.create(params);
            register(name, fetcher, interval);
        }
    }

    /**
     * Performs a synchronous initial load for all resources, then launches
     * background refresh via ScheduledExecutorService. Throws if any initial load fails.
     */
    public synchronized void start() throws Exception {
        if (started) {
            throw new IllegalStateException("resource: already started");
        }

        // Synchronous initial load
        for (ManagedResource r : resources.values()) {
            Object val = r.fetcher.fetch();
            r.value = val;
            r.loaded = true;
        }

        // Launch background refresh
        started = true;
        if (!resources.isEmpty()) {
            executor = Executors.newScheduledThreadPool(1, runnable -> {
                Thread t = new Thread(runnable, "resource-refresh");
                t.setDaemon(true);
                return t;
            });

            for (ManagedResource r : resources.values()) {
                executor.scheduleAtFixedRate(() -> refresh(r),
                        r.intervalSeconds, r.intervalSeconds, TimeUnit.SECONDS);
            }
        }
    }

    /**
     * Shuts down the background refresh executor.
     */
    public synchronized void stop() {
        if (!started) {
            return;
        }
        if (executor != null) {
            executor.shutdownNow();
            try {
                executor.awaitTermination(5, TimeUnit.SECONDS);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }
            executor = null;
        }
        started = false;
    }

    /**
     * Returns the current value for a named resource.
     * Lock-free read via volatile field.
     * Returns null if the resource does not exist or is not yet loaded.
     */
    @Override
    public Object get(String name) {
        ManagedResource r = resources.get(name);
        if (r == null || !r.loaded) {
            return null;
        }
        return r.value;
    }

    /**
     * Returns the names of all registered resources, sorted alphabetically.
     */
    public List<String> names() {
        List<String> result = new ArrayList<>(resources.keySet());
        Collections.sort(result);
        return result;
    }

    private void refresh(ManagedResource r) {
        try {
            Object val = r.fetcher.fetch();
            r.value = val;
        } catch (Exception e) {
            LOG.log(Level.WARNING,
                    "resource: refresh \"" + r.name + "\" failed (keeping old value): " + e.getMessage());
        }
    }
}
