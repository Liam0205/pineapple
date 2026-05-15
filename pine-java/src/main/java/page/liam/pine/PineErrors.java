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
            super("operator \"" + operator + "\": " + message);
            this.operator = operator;
        }

        public String getOperator() { return operator; }
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
        public OperatorException(String message) {
            super(message);
        }
        public OperatorException(String message, Throwable cause) {
            super(message, cause);
        }
    }

    public static class ExecutionError extends RuntimeException {
        private final String operator;

        public ExecutionError(String operator, Throwable cause) {
            super(cause.getMessage(), cause);
            this.operator = operator;
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
            return "pine: panic in operator \"" + operator + "\": " + getCause().getClass().getSimpleName();
        }
    }
}
