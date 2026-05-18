package page.liam.pine;

import java.util.concurrent.atomic.AtomicBoolean;

public interface CancellationToken {
    boolean isCancelled();
    void cancel();

    static CancellationToken create() {
        return new SimpleCancellationToken();
    }

    static CancellationToken childOf(CancellationToken parent) {
        return new ChildCancellationToken(parent);
    }
}

class SimpleCancellationToken implements CancellationToken {
    private final AtomicBoolean cancelled = new AtomicBoolean();

    @Override
    public boolean isCancelled() { return cancelled.get(); }

    @Override
    public void cancel() { cancelled.set(true); }
}

class ChildCancellationToken implements CancellationToken {
    private final AtomicBoolean cancelled = new AtomicBoolean();
    private final CancellationToken parent;

    ChildCancellationToken(CancellationToken parent) {
        this.parent = parent;
    }

    @Override
    public boolean isCancelled() { return cancelled.get() || parent.isCancelled(); }

    @Override
    public void cancel() { cancelled.set(true); }
}
