package page.liam.pine;

import org.junit.jupiter.api.Test;

import java.util.concurrent.atomic.AtomicInteger;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Covers the resource teardown contract that mirrors Go's pkg/resource:
 * stop() closes resource values implementing AutoCloseable exactly once.
 */
public class ResourceManagerTest {

    @Test
    void stopClosesAutoCloseableResourceOnce() throws Exception {
        AtomicInteger closes = new AtomicInteger();
        AutoCloseable value = closes::incrementAndGet;

        ResourceManager rm = new ResourceManager();
        rm.register("handle", () -> value, -1);
        rm.start();
        assertSame(value, rm.get("handle").value());

        rm.stop();
        assertEquals(1, closes.get(), "stop() must close an AutoCloseable resource value");

        // Idempotent: a second stop() must not close again.
        rm.stop();
        assertEquals(1, closes.get(), "stop() must be idempotent");
    }
}
