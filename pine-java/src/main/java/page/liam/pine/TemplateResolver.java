package page.liam.pine;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Shared {{field}} template-syntax helpers for operator-param
 * interpolation (issue #74). Mirrors {@code pine-go internal/runtime/template.go}
 * — error wording matches byte-for-byte for cross-runtime parity.
 */
public final class TemplateResolver {

    private static final Pattern MARKER = Pattern.compile("\\{\\{(\\w+)\\}\\}");
    private static final Pattern BARE_MARKER = Pattern.compile("^\\{\\{(\\w+)\\}\\}$");

    private static final Set<String> TEMPLATABLE_SCALAR_TYPES = Set.of(
            "string", "int", "int64", "float", "float64", "bool"
    );

    private TemplateResolver() {}

    /** Returns true iff v is a string carrying at least one {{field}} marker. */
    public static boolean isTemplatedString(Object v) {
        if (!(v instanceof String s)) {
            return false;
        }
        return MARKER.matcher(s).find();
    }

    /**
     * Returns true iff s matches the L0 bare-marker contract
     * {@code "^{{field}}$"} exactly. Operators that accept
     * int/float/bool typed templatable params use this from their
     * init path to distinguish a legitimate templatable marker
     * (fallback to zero, resolved per-request) from a hand-edited
     * garbage string (which must error out instead of silently
     * coercing to zero).
     */
    public static boolean isBareMarker(String s) {
        return BARE_MARKER.matcher(s).matches();
    }

    /**
     * Returns the single field name from a bare {@code "{{field}}"} value,
     * or {@code null} if the value contains literal text or multiple
     * markers. Enforces the L0 contract at engine build time.
     */
    static String extractBareField(String s) {
        Matcher m = BARE_MARKER.matcher(s);
        return m.matches() ? m.group(1) : null;
    }

    private static String normalizeScalarType(String t) {
        return switch (t) {
            case "int", "int64" -> "int64";
            case "float", "float64" -> "float64";
            default -> t;
        };
    }

    /**
     * Pre-compiled plan entry for one templated operator param.
     */
    public static final class TemplatedParam {
        public final String name;
        public final String scalarType; // canonical: string | int64 | float64 | bool
        public final String field;      // single common-field name (L0 contract)

        TemplatedParam(String name, String scalarType, String field) {
            this.name = name;
            this.scalarType = scalarType;
            this.field = field;
        }
    }

    /**
     * Scans an operator's rawParams against its schema and returns the
     * per-param interpolation plan. Returns an empty list when no
     * templated params are present. Throws {@link PineErrors.ConfigError}
     * if a templated value targets a non-templatable or non-scalar param.
     */
    public static List<TemplatedParam> buildPlan(
            String opName,
            OperatorSchema schema,
            Map<String, Object> rawParams) throws PineErrors.ConfigError {
        List<TemplatedParam> plan = new ArrayList<>();
        for (Map.Entry<String, Object> e : rawParams.entrySet()) {
            Object raw = e.getValue();
            if (!isTemplatedString(raw)) {
                continue;
            }
            String paramName = e.getKey();
            ParamSpec spec = schema.params.get(paramName);
            if (spec == null) {
                throw new PineErrors.ConfigError(
                        "operator \"" + opName + "\": param \"" + paramName +
                                "\" is not declared in schema");
            }
            if (!spec.templatable) {
                throw new PineErrors.ConfigError(
                        "operator \"" + opName + "\": param \"" + paramName +
                                "\" is not declared templatable in schema");
            }
            if (!TEMPLATABLE_SCALAR_TYPES.contains(spec.type)) {
                throw new PineErrors.ConfigError(
                        "operator \"" + opName + "\": param \"" + paramName +
                                "\" has declared type \"" + spec.type +
                                "\" which does not support templating");
            }
            String tmpl = (String) raw;
            String field = extractBareField(tmpl);
            if (field == null) {
                // L0 contract violation. Apple validator already rejects
                // this at compile time; we re-check at engine init in
                // case of hand-edited JSON.
                throw new PineErrors.ConfigError(
                        "operator \"" + opName + "\": param \"" + paramName +
                                "\" value \"" + tmpl +
                                "\" must be a bare {{field}} marker");
            }
            plan.add(new TemplatedParam(
                    paramName,
                    normalizeScalarType(spec.type),
                    field));
        }
        return plan;
    }

    /**
     * Expands a pre-built plan against the current request's common
     * frame and returns the {paramName -> coercedValue} map. The
     * scheduler attaches it to the per-request {@link OperatorInput}
     * via {@code setTemplatedParams}; operators read it via
     * {@code input.templatedParam(name)}.
     *
     * <p>The lookup consults the raw request {@link Frame} rather than
     * the operator's filtered {@link OperatorInput}: template source
     * fields live in {@code meta.common_input_template} (#74), which is
     * excluded from the operator view so that operators cannot observe
     * them via {@code input.common}. The DAG still tracks them as read
     * dependencies via {@link Config.Metadata#commonReadFields},
     * guaranteeing the producing operator (or the request itself) has
     * populated the frame before this call.
     *
     * <p>The thrown {@link PineErrors.OperatorException} carries only
     * the core message; the engine wraps every fatal in {@link
     * PineErrors.ExecutionError} which already prefixes the operator
     * name. Duplicating it here would diverge from cross-runtime parity.
     */
    public static Map<String, Object> resolve(
            String opName,
            List<TemplatedParam> plan,
            Frame frame) throws PineErrors.OperatorException {
        if (plan == null || plan.isEmpty()) {
            return null;
        }
        Map<String, Object> out = new LinkedHashMap<>(plan.size());
        for (TemplatedParam p : plan) {
            Object val = frame.common(p.field);
            if (val == null) {
                throw new PineErrors.OperatorException(
                        "templated param \"" + p.name +
                                "\" references common field \"" + p.field +
                                "\" which is missing");
            }
            // Stringify-then-coerce keeps cross-runtime parity with
            // pine-go for both happy and coerce-failure paths.
            out.put(p.name, coerce(p.name, p.scalarType, stringifyForTemplate(val)));
        }
        return out;
    }

    private static String stringifyForTemplate(Object v) {
        // GoFormat.sprint matches fmt.Sprint(any) byte-for-byte — must/conventions.md
        // anchors Go formatting as the cross-runtime contract. String.valueOf
        // diverges on floats (5.0 → "5.0" vs Go's "5"), which silently corrupts
        // the Redis key cross-runtime when the source field is float-typed.
        return GoFormat.sprint(v);
    }

    private static Object coerce(String paramName, String scalarType, String s) throws PineErrors.OperatorException {
        try {
            return switch (scalarType) {
                case "string" -> s;
                case "int64" -> Long.parseLong(s);
                case "float64" -> Double.parseDouble(s);
                case "bool" -> parseStrictBool(s, paramName);
                default -> throw new PineErrors.OperatorException(
                        "templated param \"" + paramName +
                                "\" has unsupported scalar type \"" + scalarType + "\"");
            };
        } catch (NumberFormatException nfe) {
            throw new PineErrors.OperatorException(
                    "templated param \"" + paramName + "\" cannot coerce \"" +
                            s + "\" to " + scalarType);
        }
    }

    /**
     * Match Go's {@code strconv.ParseBool}: accepts 1/0/t/f/T/F/true/false
     * /TRUE/FALSE/True/False. Any other value is a coercion failure.
     */
    private static boolean parseStrictBool(String s, String paramName) throws PineErrors.OperatorException {
        switch (s) {
            case "1": case "t": case "T": case "true": case "TRUE": case "True":
                return true;
            case "0": case "f": case "F": case "false": case "FALSE": case "False":
                return false;
            default:
                throw new PineErrors.OperatorException(
                        "templated param \"" + paramName + "\" cannot coerce \"" +
                                s + "\" to bool");
        }
    }
}
