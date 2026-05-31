package page.liam.pine;

/**
 * Optional interface for operators that hold resources needing explicit
 * teardown (e.g., a pool of interpreter states). The engine calls {@link #close()}
 * on every operator that implements it when the engine is retired — during a
 * config hot-reload, or on shutdown — so a swapped-out engine does not leak its
 * operators' resources. Operators without external resources simply omit it.
 * close must be safe to call once; the engine does not call it twice on the
 * same instance.
 */
public interface Closer {
    void close();
}
