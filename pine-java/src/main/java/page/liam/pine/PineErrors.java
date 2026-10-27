package page.liam.pine;

import java.io.PrintWriter;
import java.io.StringWriter;

public final class PineErrors {
    private PineErrors() {}

    public static class ConfigError extends Exception {
        public ConfigError(String message) {
            super(message);
        }
        public ConfigError(String message, Throwable cause) {
            super(message, cause);
        }

        @Override
        public String getMessage() {
            return "pine: config error: " + super.getMessage();
        }
    }

    public static class RegistryError extends RuntimeException {
        private final String operator;

        public RegistryError(String operator, String message) {
            super(message);
            this.operator = operator;
        }

        public String getOperator() { return operator; }

        @Override
        public String getMessage() {
            return "pine: registry error [" + operator + "]: " + super.getMessage();
        }
    }

    public static class ValidationError extends IllegalArgumentException {
        public ValidationError(String message) {
            super(message);
        }

        @Override
        public String getMessage() {
            return "pine: validation error: " + super.getMessage();
        }
    }

    public static class OperatorException extends Exception {
        // Package-private: lets ContextException skip the stack trace, so a
        // context's single shared error instance stays cheap to create.
        OperatorException(String message, boolean writableStackTrace) {
            super(message, null, false, writableStackTrace);
        }
        public OperatorException(String message) {
            super(message);
        }
        public OperatorException(String message, Throwable cause) {
            super(message, cause);
        }
    }

    /**
     * The error a {@link RequestContext} reports once it is done: "context
     * canceled" or "context deadline exceeded", the same text as Go's
     * context.Canceled / context.DeadlineExceeded. An operator that stops
     * early because its context is done throws this (see
     * {@link RequestContext#throwIfCancelled()}); the engine then reports an
     * ExecutionError naming that operator, exactly as pine-go does when an
     * operator returns ctx.Err().
     */
    public static class ContextException extends OperatorException {
        private final boolean deadlineExceeded;

        ContextException(boolean deadlineExceeded) {
            // No stack trace: the one instance is created on whichever thread
            // cancels (often the deadline timer), so its trace says nothing
            // about the operator that later observes it.
            super(deadlineExceeded ? "context deadline exceeded" : "context canceled", false);
            this.deadlineExceeded = deadlineExceeded;
        }

        /** True for a passed deadline, false for an explicit cancel. */
        public boolean isDeadlineExceeded() { return deadlineExceeded; }
    }

    /**
     * The caller's context was cancelled (or its deadline passed) before the
     * DAG finished: at least one operator never ran because of it, while no
     * operator failed on its own. The cause is the caller context's
     * {@link ContextException}. The Result carrying this error still holds
     * the writes of the operators that did complete.
     */
    public static class CancelledError extends RuntimeException {
        public CancelledError(ContextException cause) {
            super(cause.getMessage(), cause);
        }

        @Override
        public ContextException getCause() {
            return (ContextException) super.getCause();
        }

        @Override
        public String getMessage() {
            return "pine: execution cancelled: " + getCause().getMessage();
        }
    }

    public static class ExecutionError extends RuntimeException {
        private final String operator;

        public ExecutionError(String operator, Throwable cause) {
            super(cause.getMessage(), cause);
            this.operator = operator;
        }

        // String-cause convenience constructor: lets call sites raise with
        // a bare message and keeps PanicError chain semantics consistent
        // with pine-cpp's two-arg ExecutionError(op_name, value). The
        // underlying RuntimeException is a private detail used only for
        // getCause().getMessage() chaining.
        public ExecutionError(String operator, String message) {
            this(operator, new RuntimeException(message));
        }

        public String getOperator() { return operator; }

        @Override
        public String getMessage() {
            return "pine: execution error in operator \"" + operator + "\": " + getCause().getMessage();
        }
    }

    public static class PanicError extends RuntimeException {
        private final String operator;
        private final String stack;

        public PanicError(String operator, Throwable cause) {
            super(cause.getMessage(), cause);
            this.operator = operator;
            StringWriter sw = new StringWriter();
            cause.printStackTrace(new PrintWriter(sw));
            this.stack = sw.toString();
        }

        public String getOperator() { return operator; }

        public String detailedError() {
            return getMessage() + "\n" + stack;
        }

        @Override
        public String getMessage() {
            String causeMsg = getCause().getMessage();
            if (causeMsg == null) causeMsg = getCause().toString();
            return "pine: panic in operator \"" + operator + "\": " + causeMsg;
        }
    }
}
