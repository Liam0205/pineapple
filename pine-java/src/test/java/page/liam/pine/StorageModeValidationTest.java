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
    void rootFieldCheckOrderIsFullyPinned() throws Exception {
        // Asserts every ADJACENT PAIR, locking all four positions. An earlier version
        // used one input with three bad fields and asserted only that
        // _PINEAPPLE_VERSION was named, which pins position 1 and leaves the other
        // three free — swapping storage_mode with log_prefix in pine-cpp kept every
        // gate green while making the two runtimes blame different fields.
        //
        // pine-cpp's test_storage_mode_validation.cpp has the mirror. pine-go is
        // outside this contract: encoding/json names whichever wrong-typed field
        // appears first IN THE JSON DOCUMENT, so no fixed order reproduces it.
        String[][] pairs = {
            {"_PINEAPPLE_VERSION", "_PINEAPPLE_CREATE_TIME"},
            {"_PINEAPPLE_CREATE_TIME", "storage_mode"},
            {"storage_mode", "log_prefix"},
        };
        String base = new String(config(null), StandardCharsets.UTF_8);
        for (String[] pair : pairs) {
            String first = pair[0];
            String second = pair[1];
            byte[] body = ("{\"" + second + "\": 1, \"" + first + "\": 2,"
                    + base.substring(1)).getBytes(StandardCharsets.UTF_8);
            Exception e = assertThrows(Exception.class, () -> Config.load(body),
                    first + " + " + second + " must be rejected");
            assertTrue(e.getMessage().contains(first),
                    "expected \"" + first + "\" to outrank \"" + second
                            + "\", got: " + e.getMessage());
        }
    }

    /** Rebuilds the minimal config with one root field forced to a raw JSON value. */
    private static byte[] withField(String field, String rawJson) {
        String base = new String(config(null), StandardCharsets.UTF_8);
        return ("{\"" + field + "\": " + rawJson + "," + base.substring(1))
                .getBytes(StandardCharsets.UTF_8);
    }
}
