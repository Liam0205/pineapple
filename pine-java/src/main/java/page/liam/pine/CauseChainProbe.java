package page.liam.pine;

/**
 * Smoke test for ExecutionError cause-chain unwrap.
 *
 * Constructs a FakeRedisError, wraps it via {@code new ExecutionError(op,
 * cause)} into pine ExecutionError, then uses {@code getCause() instanceof
 * FakeRedisError} to recover the inner cause. Prints either:
 *
 * <pre>
 *   PASS:&lt;recovered_inner_msg&gt;
 * </pre>
 *
 * or:
 *
 * <pre>
 *   FAIL:&lt;reason&gt;
 * </pre>
 *
 * on stdout. cross-validate Section 15 asserts byte-identical stdout across
 * pine-go / pine-java / pine-python / pine-cpp probes.
 */
public final class CauseChainProbe {
    static final class FakeRedisError extends RuntimeException {
        FakeRedisError(String key) {
            super("key=" + key + " not found");
        }
    }

    public static void main(String[] args) {
        try {
            try {
                throw new FakeRedisError("user:42");
            } catch (FakeRedisError inner) {
                throw new PineErrors.ExecutionError("redis_getter", inner);
            }
        } catch (PineErrors.ExecutionError outer) {
            Throwable cause = outer.getCause();
            if (cause instanceof FakeRedisError) {
                System.out.println("PASS:" + cause.getMessage());
                return;
            }
            System.out.println("FAIL:getCause() did not recover FakeRedisError from ExecutionError chain");
            System.exit(1);
        }
    }
}
