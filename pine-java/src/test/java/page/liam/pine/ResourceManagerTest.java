package page.liam.pine;

import org.junit.jupiter.api.Test;

import java.util.concurrent.atomic.AtomicInteger;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Covers the resource lifecycle contracts that mirror Go's pkg/resource:
 * a negative interval means "never refresh" (fetched once, held until stop),
 * and stop() closes resource values implementing AutoCloseable exactly once.
 */
public class ResourceManagerTest {

    @Test
    void negativeIntervalNeverRefreshes() throws Exception {
        AtomicInteger calls = new AtomicInteger();
        ResourceManager rm = new ResourceManager();
        // interval -1 → fetched once at start, no refresh loop scheduled.
        rm.register("conn", () -> {
            calls.incrementAndGet();
            return "pool";
        }, -1);
        rm.start();
        try {
            assertEquals("pool", rm.get("conn").value());
            // Wait well beyond any plausible tick; the fetcher must still have run once.
            Thread.sleep(120);
            assertEquals(1, calls.get(), "never-refresh resource must fetch exactly once");
        } finally {
            rm.stop();
        }
    }

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

    @Test
    void loadFromConfigPreservesNeverRefresh() throws Exception {
        ResourceManager.resetFactories();
        try {
            AtomicInteger calls = new AtomicInteger();
            ResourceManager.registerFactory("test_conn", params -> () -> {
                calls.incrementAndGet();
                return "value";
            });

            ResourceManager rm = new ResourceManager();
            String config = "{\"resource_config\": {"
                    + "\"db\": {\"type\": \"test_conn\", \"interval\": -1, \"params\": {}}"
                    + "}}";
            rm.loadFromConfig(config.getBytes());
            rm.start();
            try {
                assertEquals("value", rm.get("db").value());
                Thread.sleep(120);
                assertEquals(1, calls.get(), "interval -1 from config must not schedule refreshes");
            } finally {
                rm.stop();
            }
        } finally {
            ResourceManager.resetFactories();
        }
    }
}
