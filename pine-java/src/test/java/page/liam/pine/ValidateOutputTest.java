package page.liam.pine;

import org.junit.jupiter.api.Test;

import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Tests for {@link OperatorType#validateOutput}, the operator-type
 * method-restriction contract. Mirrors pine-go
 * {@code internal/types/validate_output_test.go} and the pine-cpp
 * {@code test_engine.cpp} cases so the three runtimes enforce the same
 * matrix. Pins the Recall-may-write-common relaxation and the
 * still-forbidden item mutations.
 */
class ValidateOutputTest {

    @Test
    void recallMayWriteCommon() {
        OperatorOutput out = new OperatorOutput();
        out.setCommon("request_id", "req-123");
        out.addItem(Map.of("id", "a"));
        assertNull(OperatorType.RECALL.validateOutput(out),
                "Recall writing common + addItem should be allowed");
    }

    @Test
    void recallStillForbidsSetItem() {
        OperatorOutput out = new OperatorOutput();
        out.setItem(0, "score", 1.0);
        String err = OperatorType.RECALL.validateOutput(out);
        assertNotNull(err, "Recall.setItem should be forbidden");
        assertTrue(err.contains("SetItem"), () -> "error should mention SetItem: " + err);
    }

    @Test
    void recallStillForbidsRemoveAndReorder() {
        OperatorOutput removeOut = new OperatorOutput();
        removeOut.removeItem(0);
        String removeErr = OperatorType.RECALL.validateOutput(removeOut);
        assertNotNull(removeErr);
        assertTrue(removeErr.contains("RemoveItem"), () -> removeErr);

        OperatorOutput reorderOut = new OperatorOutput();
        reorderOut.setItemOrder(java.util.List.of(0));
        String reorderErr = OperatorType.RECALL.validateOutput(reorderOut);
        assertNotNull(reorderErr);
        assertTrue(reorderErr.contains("SetItemOrder"), () -> reorderErr);
    }

    @Test
    void transformMayWriteCommonAndItem() {
        OperatorOutput out = new OperatorOutput();
        out.setCommon("x", 1);
        out.setItem(0, "y", 2);
        assertNull(OperatorType.TRANSFORM.validateOutput(out),
                "Transform writing common + item should be allowed");
    }

    @Test
    void observeIsReadOnly() {
        OperatorOutput out = new OperatorOutput();
        out.setCommon("x", 1);
        String err = OperatorType.OBSERVE.validateOutput(out);
        assertNotNull(err, "Observe.setCommon should be forbidden");
        assertTrue(err.contains("SetCommon"), () -> err);
    }

    @Test
    void emptyOutputAlwaysClean() {
        for (OperatorType ty : OperatorType.values()) {
            assertNull(ty.validateOutput(new OperatorOutput()),
                    () -> "empty output should be clean for " + ty);
        }
    }
}
