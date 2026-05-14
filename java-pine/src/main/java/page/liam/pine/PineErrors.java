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
    }

    public static class RegistryError extends RuntimeException {
        private final String operator;

        public RegistryError(String operator, String message) {
            super("operator \"" + operator + "\": " + message);
            this.operator = operator;
        }

        public String getOperator() { return operator; }
    }

    public static class ValidationError extends IllegalArgumentException {
        public ValidationError(String message) {
            super(message);
        }
    }

    public static class ExecutionError extends RuntimeException {
        private final String operator;

        public ExecutionError(String operator, Throwable cause) {
            super("operator \"" + operator + "\" execution failed: " + cause.getMessage(), cause);
            this.operator = operator;
        }

        public String getOperator() { return operator; }
    }

    public static class PanicError extends RuntimeException {
        private final String operator;
        private final String stack;

        public PanicError(String operator, Throwable cause) {
            super("operator \"" + operator + "\" panicked: " + cause.getClass().getSimpleName(), cause);
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
            return "operator \"" + operator + "\" panicked: " + getCause().getClass().getSimpleName();
        }
    }
}
