package page.liam.pine.debug;

import page.liam.pine.RequestContext;

/**
 * Request-local debug event interface for operators: the Java counterpart of
 * pine-go {@code pkg/debug}. It owns no session, buffer, serializer, worker or
 * external client. Applications attach an {@link Emitter} with
 * {@link #withEmitter}; without one {@link #fromContext} returns null, so build
 * debug-only data inside that null check, outside the candidate loop:
 *
 * <pre>{@code
 * Recorder r = Recorder.fromContext(ctx);
 * if (r != null) {
 *     r.emit("candidates", buildDebugView(input));
 * }
 * }</pre>
 *
 * <p>The engine binds immutable node metadata before calling each operator.
 * An attached recorder preserves normal execution, including
 * {@code data_parallel} shards and independent DAG nodes: shards of a node
 * share its metadata and may emit separate events concurrently; the recorder
 * does not merge them. Intended for trusted debug/test requests. It does not
 * turn on the static debug snapshots ({@code withDebug} / JSON {@code debug})
 * and does not suppress business writes.
 *
 * <p>The authoritative implementer's contract is the package doc of
 * {@code pine-go/pkg/debug/debug.go}; see also {@link Emitter}.
 */
public final class Recorder {
    // Private identity key: no other library can collide with it.
    private static final Object KEY = new Object();

    private final Emitter emitter;
    private final NodeMeta meta;

    private Recorder(Emitter emitter, NodeMeta meta) {
        this.emitter = emitter;
        this.meta = meta;
    }

    /**
     * Attaches {@code emitter} to this request. A null emitter returns
     * {@code ctx} unchanged, including any recorder it already carries.
     * Emitter / session shutdown remains the application's responsibility.
     */
    public static RequestContext withEmitter(RequestContext ctx, Emitter emitter) {
        if (emitter == null) {
            return ctx;
        }
        return RequestContext.withValue(ctx, KEY, new Recorder(emitter, NodeMeta.NONE));
    }

    /** The recorder attached to {@code ctx}, or null when there is none. */
    public static Recorder fromContext(RequestContext ctx) {
        Object r = ctx.value(KEY);
        return r instanceof Recorder ? (Recorder) r : null;
    }

    /**
     * A child of {@code ctx} whose recorder reports {@code meta}. The parent
     * and the shared emitter are not mutated; ctx's values, deadline and
     * cancellation are preserved. The engine calls this before each operator,
     * only when a recorder is attached.
     */
    public RequestContext bindNode(RequestContext ctx, NodeMeta meta) {
        return RequestContext.withValue(ctx, KEY, new Recorder(emitter, meta));
    }

    /**
     * Synchronously forwards {@code data}; Pineapple neither copies nor encodes
     * it. Anything the emitter throws is swallowed so it cannot reach the
     * business operator. Argument evaluation by the caller is not isolated, so
     * guard debug-only payload construction with the {@link #fromContext} null
     * check.
     */
    public void emit(String kind, Object data) {
        try {
            emitter.emit(meta, kind, data);
        } catch (Throwable ignored) {
            // A failing emitter must not affect the business result (Go: recover()).
        }
    }
}
