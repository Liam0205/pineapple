package page.liam.pine;

import java.time.Duration;
import java.time.Instant;
import java.util.HashSet;
import java.util.Optional;
import java.util.Set;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ScheduledFuture;
import java.util.concurrent.ScheduledThreadPoolExecutor;
import java.util.concurrent.TimeUnit;

/**
 * Request-scoped context passed to every {@link Operator#execute}: the Java
 * counterpart of Go's {@code context.Context}. It carries cancellation, an
 * optional deadline, request-scoped values and the request's
 * {@link ResourceProvider}.
 *
 * <p>Contexts are immutable and form a chain: each {@code with*} factory
 * derives a child from a parent. Cancelling a parent cancels every context
 * derived from it; cancelling a child never affects its parent. A context
 * derived with {@link #withCancel}, {@link #withDeadline} or
 * {@link #withTimeout} is a {@link Cancelable} and should be closed when the
 * work it scopes is done, which releases its link to the parent and its
 * deadline timer:
 *
 * <pre>{@code
 * try (RequestContext.Cancelable ctx =
 *         RequestContext.withTimeout(RequestContext.background(), Duration.ofMillis(200))) {
 *     Engine.Result r = engine.execute(ctx, common, items);
 * }
 * }</pre>
 *
 * <p>Once done, {@link #err()} reports "context canceled" or "context
 * deadline exceeded" — the same text as Go's context errors.
 */
public class RequestContext {
    private static final RequestContext BACKGROUND = new RequestContext(null, Signal.NEVER, null, null, null);

    private final RequestContext parent;
    final Signal signal;
    // Exactly one of (key, resources) is set on a value / resource node;
    // both are null on a cancel / deadline node and on background().
    private final Object key;
    private final Object value;
    private final ResourceProvider resources;

    RequestContext(RequestContext parent, Signal signal, Object key, Object value, ResourceProvider resources) {
        this.parent = parent;
        this.signal = signal;
        this.key = key;
        this.value = value;
        this.resources = resources;
    }

    /** The root context: never cancelled, no deadline, no values, no resources. */
    public static RequestContext background() {
        return BACKGROUND;
    }

    /** A child that is cancelled by {@link Cancelable#cancel()} or when the parent is. */
    public static Cancelable withCancel(RequestContext parent) {
        return new Cancelable(parent, Signal.childOf(parent.signal, parent.signal.deadline));
    }

    /**
     * A child that is also cancelled, with "context deadline exceeded", once
     * {@code deadline} passes. A parent deadline that is earlier still wins.
     */
    public static Cancelable withDeadline(RequestContext parent, Instant deadline) {
        Instant inherited = parent.signal.deadline;
        if (inherited != null && !inherited.isAfter(deadline)) {
            return withCancel(parent);
        }
        Signal s = Signal.childOf(parent.signal, deadline);
        s.armDeadline();
        return new Cancelable(parent, s);
    }

    /** {@code withDeadline(parent, now + timeout)}. */
    public static Cancelable withTimeout(RequestContext parent, Duration timeout) {
        return withDeadline(parent, Instant.now().plus(timeout));
    }

    /**
     * A child carrying {@code value} under {@code key}. Keys compare with
     * {@code equals}; use a private key object (not a String) to avoid
     * collisions between libraries, as with Go's context keys.
     */
    public static RequestContext withValue(RequestContext parent, Object key, Object value) {
        if (key == null) {
            throw new IllegalArgumentException("RequestContext.withValue: key must not be null");
        }
        return new RequestContext(parent, parent.signal, key, value, null);
    }

    /** A child whose {@link #resources()} is {@code provider}. */
    public static RequestContext withResources(RequestContext parent, ResourceProvider provider) {
        return new RequestContext(parent, parent.signal, null, null, provider);
    }

    /** True once this context is cancelled or its deadline has passed. */
    public boolean isCancelled() {
        return signal.err != null;
    }

    /** The reason this context is done, or null while it is not. */
    public PineErrors.ContextException err() {
        return signal.err;
    }

    /** Throws {@link #err()} if this context is done; operators use it at safe stopping points. */
    public void throwIfCancelled() throws PineErrors.ContextException {
        PineErrors.ContextException e = signal.err;
        if (e != null) {
            throw e;
        }
    }

    /** The deadline after which this context is cancelled, if any. */
    public Optional<Instant> deadline() {
        return Optional.ofNullable(signal.deadline);
    }

    /** The value bound to {@code key} by the nearest {@link #withValue}, or null. */
    public Object value(Object key) {
        for (RequestContext c = this; c != null; c = c.parent) {
            if (c.key != null && c.key.equals(key)) {
                return c.value;
            }
        }
        return null;
    }

    /** The provider set by the nearest {@link #withResources}, or null. */
    public ResourceProvider resources() {
        for (RequestContext c = this; c != null; c = c.parent) {
            if (c.resources != null) {
                return c.resources;
            }
        }
        return null;
    }

    /**
     * Runs {@code callback} once when this context is cancelled — immediately,
     * on the calling thread, if it already is. Otherwise it runs on the thread
     * that cancels (the caller of {@code cancel()}, or the deadline timer), so
     * it must be quick and must not block: cancel a future, close a socket.
     * Closing the returned registration before then removes the callback.
     */
    public Registration onCancel(Runnable callback) {
        return signal.onCancel(callback);
    }

    /**
     * Waits up to {@code timeout} for this context to be cancelled. Returns
     * true if it was (possibly before the call), false if the time ran out.
     * An interruptible replacement for {@code Thread.sleep} in operators.
     */
    public boolean awaitCancellation(Duration timeout) throws InterruptedException {
        if (signal == Signal.NEVER) {
            Thread.sleep(timeout);
            return false;
        }
        return signal.done.await(timeout.toNanos(), TimeUnit.NANOSECONDS);
    }

    /** Handle returned by {@link #onCancel}; {@link #close()} unregisters the callback. */
    public interface Registration extends AutoCloseable {
        @Override
        void close();
    }

    /** A context its creator can cancel; {@link #close()} is {@link #cancel()}. */
    public static final class Cancelable extends RequestContext implements AutoCloseable {
        Cancelable(RequestContext parent, Signal signal) {
            super(parent, signal, null, null, null);
        }

        /** Cancels this context and every context derived from it; idempotent. */
        public void cancel() {
            signal.cancel(new PineErrors.ContextException(false));
        }

        @Override
        public void close() {
            cancel();
        }
    }

    // --- cancellation state ---

    /**
     * Cancellation state shared by a cancelable context and the value /
     * resource contexts derived from it. Child signals register a callback
     * on their parent signal and drop it when they are cancelled themselves,
     * so a long-lived parent (a server's root context) does not accumulate
     * one entry per finished request.
     */
    static final class Signal {
        static final Signal NEVER = new Signal(null);

        private static final ScheduledThreadPoolExecutor TIMER = newTimer();

        final Instant deadline;
        final CountDownLatch done = new CountDownLatch(1);
        volatile PineErrors.ContextException err;
        // Guarded by this; null once cancelled.
        private Set<Entry> callbacks = new HashSet<>();
        private volatile Registration parentLink;
        private volatile ScheduledFuture<?> timer;

        private Signal(Instant deadline) {
            this.deadline = deadline;
        }

        static Signal childOf(Signal parent, Instant deadline) {
            Signal s = new Signal(deadline);
            if (parent != NEVER) {
                s.parentLink = parent.onCancel(() -> s.cancel(parent.err));
            }
            return s;
        }

        void armDeadline() {
            long delayNanos = Duration.between(Instant.now(), deadline).toNanos();
            if (delayNanos <= 0) {
                cancel(new PineErrors.ContextException(true));
                return;
            }
            timer = TIMER.schedule(() -> cancel(new PineErrors.ContextException(true)),
                    delayNanos, TimeUnit.NANOSECONDS);
        }

        void cancel(PineErrors.ContextException cause) {
            if (this == NEVER) {
                return;
            }
            Set<Entry> toRun;
            synchronized (this) {
                if (err != null) {
                    return;
                }
                err = cause;
                toRun = callbacks;
                callbacks = null;
            }
            done.countDown();
            Registration link = parentLink;
            if (link != null) {
                link.close();
            }
            ScheduledFuture<?> t = timer;
            if (t != null) {
                t.cancel(false);
            }
            for (Entry e : toRun) {
                runCallback(e.callback);
            }
        }

        Registration onCancel(Runnable callback) {
            if (this == NEVER) {
                return () -> { };
            }
            Entry entry = new Entry(callback);
            synchronized (this) {
                if (callbacks != null) {
                    callbacks.add(entry);
                    return () -> remove(entry);
                }
            }
            runCallback(callback);
            return () -> { };
        }

        // Test hook: callbacks still registered (0 once cancelled).
        synchronized int pendingCallbacks() {
            return callbacks == null ? 0 : callbacks.size();
        }

        private synchronized void remove(Entry entry) {
            if (callbacks != null) {
                callbacks.remove(entry);
            }
        }

        private static void runCallback(Runnable callback) {
            try {
                callback.run();
            } catch (RuntimeException e) {
                System.err.println("[pine] RequestContext onCancel callback threw: " + e);
            }
        }

        private static ScheduledThreadPoolExecutor newTimer() {
            ScheduledThreadPoolExecutor t = new ScheduledThreadPoolExecutor(1, r -> {
                Thread th = new Thread(r, "pine-context-deadline");
                th.setDaemon(true);
                return th;
            });
            // Cancelled deadlines (the common case: the request finished
            // first) leave the queue at once instead of at their due time.
            t.setRemoveOnCancelPolicy(true);
            return t;
        }

        // Identity-keyed holder so the same Runnable can be registered twice
        // and each registration removes only itself.
        private static final class Entry {
            final Runnable callback;

            Entry(Runnable callback) {
                this.callback = callback;
            }
        }
    }
}
