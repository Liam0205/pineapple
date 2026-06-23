package page.liam.pine;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;

/**
 * Locks the formatting choices that pine-java's Codegen must share with
 * pine-go's pkg/codegen so per-operator markdown stays byte-comparable
 * across engines (modulo legitimate schema-content differences).
 *
 * <p>Backstory: a 2026-06-22 PR that round-tripped through {@code
 * scripts/codegen.sh --backend java} surfaced three doc-rendering drifts
 * vs the Go reference output:
 * <ul>
 *   <li>{@code **Type**: TRANSFORM} (Java enum {@code name()}) vs
 *       {@code **Type**: Transform} (Go's PascalCase string).</li>
 *   <li>String defaults rendered as {@code `string`} vs Go's
 *       {@code `"string"`}.</li>
 *   <li>Boolean defaults rendered as {@code `false`} vs Go's
 *       {@code `False`} (Python literal form).</li>
 * </ul>
 * The Go side has always been the contract — Apple Python is the consumer
 * and Go is the original source-of-truth backend. This test pins both
 * helpers at their intended outputs so a future tweak doesn't reopen the
 * drift.
 */
public class CodegenDocParityTest {

    @Test
    void pascalCaseEnumMatchesGoTypeStrings() {
        assertEquals("Transform", Codegen.pascalCaseEnum("TRANSFORM"));
        assertEquals("Recall", Codegen.pascalCaseEnum("RECALL"));
        assertEquals("Filter", Codegen.pascalCaseEnum("FILTER"));
        assertEquals("Merge", Codegen.pascalCaseEnum("MERGE"));
        assertEquals("Reorder", Codegen.pascalCaseEnum("REORDER"));
        assertEquals("Observe", Codegen.pascalCaseEnum("OBSERVE"));
    }

    @Test
    void pascalCaseEnumHandlesMultiWordNames() {
        // Defensive: future multi-word enum names should render cleanly
        // (Go uses PascalCase for these in its OpType constants).
        assertEquals("MergeDedupUnion", Codegen.pascalCaseEnum("MERGE_DEDUP_UNION"));
    }

    @Test
    void pascalCaseEnumHandlesEdgeCases() {
        assertEquals("", Codegen.pascalCaseEnum(""));
        assertNull(Codegen.pascalCaseEnum(null));
    }

    @Test
    void toPythonLiteralStringsAreQuoted() {
        // Bare toString() drifted to "string" without quotes; Go's
        // pythonLiteral uses fmt.Sprintf("%q", ...) which produces
        // "string" with embracing quotes.
        assertEquals("\"string\"", Codegen.toPythonLiteral("string"));
        assertEquals("\"hello\"", Codegen.toPythonLiteral("hello"));
        assertEquals("\"\"", Codegen.toPythonLiteral(""));
    }

    @Test
    void toPythonLiteralBoolsAreCapitalised() {
        // Bare Boolean.toString() yields "false"/"true"; Go writes
        // Python literals "False"/"True" so the rendered table cell
        // matches the Apple Python code that consumes the schema.
        assertEquals("True", Codegen.toPythonLiteral(Boolean.TRUE));
        assertEquals("False", Codegen.toPythonLiteral(Boolean.FALSE));
    }

    @Test
    void toPythonLiteralIntegersDropDecimal() {
        // Go's pythonLiteral renders int64 as %d (no decimal). Our
        // path goes through Number.doubleValue() and detects whole
        // numbers; verify both representations.
        assertEquals("0", Codegen.toPythonLiteral(0L));
        assertEquals("0", Codegen.toPythonLiteral(0));
        assertEquals("2000", Codegen.toPythonLiteral(2000L));
    }

    @Test
    void toPythonLiteralNullIsNone() {
        assertEquals("None", Codegen.toPythonLiteral(null));
    }
}
