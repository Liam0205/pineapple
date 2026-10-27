package page.liam.pine.debug;

/**
 * Receives application-supplied debug data together with the framework-supplied
 * {@link NodeMeta}. Mirrors pine-go {@code pkg/debug.Emitter}; the implementer's
 * contract is the package doc of {@code pine-go/pkg/debug/debug.go}, summarised:
 *
 * <ul>
 *   <li>{@code emit} may be called concurrently (independent DAG nodes and
 *       {@code data_parallel} shards emit from different threads).</li>
 *   <li>It must consume or copy {@code data} before returning: input views,
 *       nested containers and operator outputs may change afterwards.</li>
 *   <li>It must return promptly, without waiting on network I/O, and must
 *       handle its own failures. {@link Recorder} swallows anything it throws
 *       so the business result is unaffected.</li>
 *   <li>Capacity limits, supported data types, serialization, delivery and
 *       session ownership belong to the implementation.</li>
 * </ul>
 */
@FunctionalInterface
public interface Emitter {
    void emit(NodeMeta meta, String kind, Object data);
}
