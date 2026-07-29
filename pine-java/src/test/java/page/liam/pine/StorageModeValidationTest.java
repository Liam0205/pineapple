package page.liam.pine;

import static org.junit.jupiter.api.Assertions.assertDoesNotThrow;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.nio.charset.StandardCharsets;
import org.junit.jupiter.api.Test;

/**
 * Pins the fail-fast and the root-string type rule added in issue #187.
 *
 * <p>Before it, an invalid {@code storage_mode} was silently accepted here and
 * fell back to row storage, so a typo produced a working engine whose memory and
 * performance profile was the opposite of what the author asked for. The value is
 * now rejected at config load in all three runtimes, with a byte-identical
 * message.
 *
 * <p>Rejecting at load rather than in {@code Frame.create} is deliberate: the
 * dispatch rule is "only the exact literal column selects column storage,
 * everything else is row", mirroring pine-go's {@code NewFrame} default branch
 * (issue #179). Validation makes the invalid value unreachable instead of
 * changing what dispatch does with it, so both rules stay simple.
 *
 * <p>The type half mattered more than it looked. It was never specific to
 * {@code storage_mode}: pine-go rejects a non-string for EVERY root string field
 * because they are all declared {@code string} and {@code encoding/json} fails
 * the whole unmarshal, while this runtime coerced everything through
 * {@code asText()} — {@code 123} became {@code "123"} and a container became the
 * empty string. So a config pine-go refused ran here with an invented value.
 */
class StorageModeValidationTest {

    private static byte[] config(String storageModeJson) {
        String sm = storageModeJson == null ? "" : "\"storage_mode\": " + storageModeJson + ",";
        return ("{" + sm
                + "\"pipeline_config\": {"
                + "  \"operators\": {\"op\": {\"type_name\": \"transform_copy\","
                + "    \"direction\": \"common_to_item\","
                + "    \"$metadata\": {\"common_input\": [\"v\"], \"common_output\": [],"
                + "      \"item_input\": [], \"item_output\": [\"v\"]}}},"
                + "  \"pipeline_map\": {\"s\": {\"pipeline\": [\"op\"]}}},"
                + "\"pipeline_group\": {\"main\": {\"pipeline\": [\"s\"]}},"
                + "\"flow_contract\": {\"common_input\": [\"v\"], \"item_input\": [\"id\"],"
                + "  \"common_output\": [], \"item_output\": [\"id\", \"v\"]}}")
                .getBytes(StandardCharsets.UTF_8);
    }

    @Test
    void validStorageModesAreAccepted() throws Exception {
        // Empty and null are accepted because they are pine-go's zero value: an
        // omitted key and a JSON null both arrive there as "".
        for (String sm : new String[] {"\"row\"", "\"column\"", "\"\"", "null"}) {
            final String value = sm;
            assertDoesNotThrow(() -> Config.load(config(value)),
                    "storage_mode " + value + " must be accepted");
        }
        assertDoesNotThrow(() -> Config.load(config(null)),
                "absent storage_mode must be accepted");
    }

    @Test
    void invalidStorageModesAreRejected() {
        // Includes the two values that diverged across runtimes before #179:
        // "colunm" (a typo) and "Column" (wrong case).
        for (String sm : new String[] {"\"colunm\"", "\"Column\"", "\"COLUMN\"", "\"col\"",
                                       "\"columns\"", "\"column \"", "\" column\"", "\"unknown\""}) {
            final String value = sm;
            Exception e = assertThrows(Exception.class, () -> Config.load(config(value)),
                    "storage_mode " + value + " must be rejected");
            assertTrue(e.getMessage().contains("is invalid, must be"),
                    "unexpected error text for " + value + ": " + e.getMessage());
        }
    }

    @Test
    void rootStringFieldsRejectNonStrings() {
        String[] fields = {"storage_mode", "log_prefix", "_PINEAPPLE_VERSION",
                           "_PINEAPPLE_CREATE_TIME"};
        String[] nonStrings = {"123", "1.5", "true", "false", "[1,2]", "{\"a\":1}"};
        for (String field : fields) {
            for (String val : nonStrings) {
                byte[] body = withField(field, val);
                Exception e = assertThrows(Exception.class, () -> Config.load(body),
                        field + " = " + val + " must be rejected");
                assertTrue(e.getMessage().contains("must be a string"),
                        "unexpected error text for " + field + "=" + val + ": " + e.getMessage());
            }
            // null leaves the default rather than erroring, matching Go.
            byte[] nullBody = withField(field, "null");
            assertDoesNotThrow(() -> Config.load(nullBody),
                    field + " = null must be accepted");
        }
    }

    @Test
    void multipleWrongTypedRootFieldsNameTheFirstInCheckOrder() throws Exception {
        // With more than one root field wrong-typed, WHICH field the error names is
        // externally observable, so pine-java and pine-cpp must agree. Both check in
        // the order _PINEAPPLE_VERSION, _PINEAPPLE_CREATE_TIME, storage_mode,
        // log_prefix, so both name _PINEAPPLE_VERSION here regardless of the JSON's
        // key order.
        //
        // pine-go deliberately is NOT part of this contract: encoding/json names
        // whichever wrong-typed field appears first IN THE JSON DOCUMENT, so its
        // answer moves with the input and no fixed check order can reproduce it.
        // Measured; recorded in llmdoc/memory/doc-gaps.md. That asymmetry is also why
        // this cannot be a cross-validate error fixture — section 05 requires all
        // three engines to match the same substring.
        String base = new String(config(null), StandardCharsets.UTF_8);
        byte[] body = ("{\"log_prefix\": 1, \"storage_mode\": 2, \"_PINEAPPLE_VERSION\": 3,"
                + base.substring(1)).getBytes(StandardCharsets.UTF_8);
        Exception e = assertThrows(Exception.class, () -> Config.load(body));
        assertTrue(e.getMessage().contains("_PINEAPPLE_VERSION"),
                "expected the first field in check order to be named, got: " + e.getMessage());
    }

    /** Rebuilds the minimal config with one root field forced to a raw JSON value. */
    private static byte[] withField(String field, String rawJson) {
        String base = new String(config(null), StandardCharsets.UTF_8);
        return ("{\"" + field + "\": " + rawJson + "," + base.substring(1))
                .getBytes(StandardCharsets.UTF_8);
    }
}
