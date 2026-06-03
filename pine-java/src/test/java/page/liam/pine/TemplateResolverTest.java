package page.liam.pine;

import org.junit.jupiter.api.Test;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Mirrors {@code pine-go internal/runtime/template_test.go} so byte-exact
 * error wording stays in lockstep with the Go runtime (issue #74).
 */
public class TemplateResolverTest {

    private static OperatorSchema schemaWith(String paramName, String type, boolean templatable) {
        Map<String, ParamSpec> params = new LinkedHashMap<>();
        params.put(paramName, new ParamSpec(type, false, null, "x", templatable));
        return new OperatorSchema("op_a", OperatorType.TRANSFORM, "test", params);
    }

    private static Frame frameWithCommon(Map<String, Object> common) {
        return new DataFrame(common, List.of());
    }

    @Test
    void isTemplatedString_classifies() {
        assertFalse(TemplateResolver.isTemplatedString("plain"));
        assertTrue(TemplateResolver.isTemplatedString("{{x}}"));
        assertTrue(TemplateResolver.isTemplatedString("prefix-{{x}}-suffix"));
        assertFalse(TemplateResolver.isTemplatedString("{{}}"));
        assertFalse(TemplateResolver.isTemplatedString(null));
        assertFalse(TemplateResolver.isTemplatedString(42));
        assertFalse(TemplateResolver.isTemplatedString(true));
    }

    @Test
    void buildPlan_skipsNonTemplated() throws Exception {
        List<TemplateResolver.TemplatedParam> plan = TemplateResolver.buildPlan(
                "name", schemaWith("k", "string", true),
                Map.of("k", "no markers"));
        assertTrue(plan.isEmpty());
    }

    @Test
    void buildPlan_happy() throws Exception {
        List<TemplateResolver.TemplatedParam> plan = TemplateResolver.buildPlan(
                "name", schemaWith("k", "int64", true),
                Map.of("k", "{{user_id}}"));
        assertEquals(1, plan.size());
        assertEquals("k", plan.get(0).name);
        assertEquals("int64", plan.get(0).scalarType);
        assertEquals("user_id", plan.get(0).field);
    }

    @Test
    void extractBareField_classifies() {
        assertEquals("user_id", TemplateResolver.extractBareField("{{user_id}}"));
        assertNull(TemplateResolver.extractBareField("prefix-{{x}}"));
        assertNull(TemplateResolver.extractBareField("{{x}}-suffix"));
        assertNull(TemplateResolver.extractBareField("tenant:{{tenant_id}}:"));
        assertNull(TemplateResolver.extractBareField("{{a}}{{b}}"));
        assertNull(TemplateResolver.extractBareField("{{}}"));
        assertNull(TemplateResolver.extractBareField("plain"));
    }

    @Test
    void buildPlan_rejectsNonBareMarker() {
        // L0 contract: literal text around the marker is rejected at engine
        // build time. Apple validator catches this earlier, but the runtime
        // re-checks in case of hand-edited JSON.
        for (String bad : List.of(
                "prefix-{{x}}",
                "{{x}}-suffix",
                "tenant:{{tenant_id}}:",
                "{{a}}{{b}}")) {
            PineErrors.ConfigError e = assertThrows(PineErrors.ConfigError.class,
                    () -> TemplateResolver.buildPlan("name",
                            schemaWith("k", "string", true),
                            Map.of("k", bad)));
            assertTrue(e.getMessage().contains("must be a bare {{field}} marker"),
                    "value " + bad + ": " + e.getMessage());
        }
    }

    @Test
    void buildPlan_rejectsNonTemplatable() {
        PineErrors.ConfigError e = assertThrows(PineErrors.ConfigError.class,
                () -> TemplateResolver.buildPlan("name",
                        schemaWith("k", "string", false),
                        Map.of("k", "{{x}}")));
        assertTrue(e.getMessage().contains("param \"k\" is not declared templatable"), e.getMessage());
    }

    @Test
    void buildPlan_rejectsUnknownParam() {
        PineErrors.ConfigError e = assertThrows(PineErrors.ConfigError.class,
                () -> TemplateResolver.buildPlan("name",
                        schemaWith("k", "string", true),
                        Map.of("missing", "{{x}}")));
        assertTrue(e.getMessage().contains("param \"missing\" is not declared in schema"), e.getMessage());
    }

    @Test
    void buildPlan_rejectsNonScalarType() {
        PineErrors.ConfigError e = assertThrows(PineErrors.ConfigError.class,
                () -> TemplateResolver.buildPlan("name",
                        schemaWith("k", "string_list", true),
                        Map.of("k", "{{x}}")));
        assertTrue(e.getMessage().contains("does not support templating"), e.getMessage());
    }

    @Test
    void buildPlan_allScalarTypes() throws Exception {
        for (String typ : List.of("string", "int", "int64", "float", "float64", "bool")) {
            TemplateResolver.buildPlan("name",
                    schemaWith("k", typ, true),
                    Map.of("k", "{{x}}"));
        }
    }

    @Test
    void resolve_stringBindsField() throws Exception {
        List<TemplateResolver.TemplatedParam> plan = TemplateResolver.buildPlan(
                "op", schemaWith("k", "string", true), Map.of("k", "{{id}}"));
        Map<String, Object> got = TemplateResolver.resolve("op", plan,
                frameWithCommon(Map.of("id", "42")));
        assertEquals("42", got.get("k"));
    }

    /**
     * Pins {@code stringifyForTemplate} on the GoFormat.sprint pathway:
     * a double-valued source field bound to a string-typed templatable
     * param must serialize as Go's {@code fmt.Sprint(5.0) == "5"},
     * not Java's {@code String.valueOf(5.0) == "5.0"}. Without this
     * pin the Redis key would diverge across runtimes whenever the
     * template source is float-typed.
     */
    @Test
    void resolve_floatSourceStringTargetMatchesGoFormat() throws Exception {
        List<TemplateResolver.TemplatedParam> plan = TemplateResolver.buildPlan(
                "op", schemaWith("k", "string", true), Map.of("k", "{{x}}"));
        Map<String, Object> got = TemplateResolver.resolve("op", plan,
                frameWithCommon(Map.of("x", Double.valueOf(5.0))));
        assertEquals("5", got.get("k"));
    }

    @Test
    void resolve_int() throws Exception {
        List<TemplateResolver.TemplatedParam> plan = TemplateResolver.buildPlan(
                "op", schemaWith("k", "int64", true), Map.of("k", "{{n}}"));
        Map<String, Object> got = TemplateResolver.resolve("op", plan,
                frameWithCommon(Map.of("n", 7L)));
        assertEquals(7L, got.get("k"));
    }

    @Test
    void resolve_bool() throws Exception {
        List<TemplateResolver.TemplatedParam> plan = TemplateResolver.buildPlan(
                "op", schemaWith("k", "bool", true), Map.of("k", "{{b}}"));
        Map<String, Object> got = TemplateResolver.resolve("op", plan,
                frameWithCommon(Map.of("b", true)));
        assertEquals(Boolean.TRUE, got.get("k"));
    }

    @Test
    void resolve_missingField() throws Exception {
        List<TemplateResolver.TemplatedParam> plan = TemplateResolver.buildPlan(
                "op", schemaWith("k", "string", true), Map.of("k", "{{absent}}"));
        PineErrors.OperatorException e = assertThrows(PineErrors.OperatorException.class,
                () -> TemplateResolver.resolve("op", plan, frameWithCommon(Map.of())));
        assertEquals(
                "templated param \"k\" references common field \"absent\" which is missing",
                e.getMessage());
    }

    @Test
    void resolve_coerceFailure() throws Exception {
        List<TemplateResolver.TemplatedParam> plan = TemplateResolver.buildPlan(
                "op", schemaWith("k", "int64", true), Map.of("k", "{{x}}"));
        PineErrors.OperatorException e = assertThrows(PineErrors.OperatorException.class,
                () -> TemplateResolver.resolve("op", plan,
                        frameWithCommon(Map.of("x", "not-a-number"))));
        assertEquals(
                "templated param \"k\" cannot coerce \"not-a-number\" to int64",
                e.getMessage());
    }

    @Test
    void resolve_emptyPlanReturnsNull() throws Exception {
        assertNull(TemplateResolver.resolve("op", List.of(), frameWithCommon(Map.of())));
        assertNull(TemplateResolver.resolve("op", null, frameWithCommon(Map.of())));
    }
}
