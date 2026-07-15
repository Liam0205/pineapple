package page.liam.pine;

import java.util.ArrayList;
import java.util.List;

public enum OperatorType {
    RECALL,
    TRANSFORM,
    FILTER,
    MERGE,
    REORDER,
    OBSERVE;

    public String validateOutput(OperatorOutput out) {
        List<String> violations = new ArrayList<>();

        boolean hasCommonWrites = !out.getCommonWrites().isEmpty();
        // Whole-column writes are item writes for the method-restriction
        // contract: setItemColumnDouble counts as SetItem.
        boolean hasItemWrites = !out.getItemWrites().isEmpty() || !out.getColumnWrites().isEmpty();
        boolean hasAddedItems = !out.getAddedItems().isEmpty();
        boolean hasRemovedItems = !out.getRemovedItems().isEmpty();
        boolean hasItemOrder = out.getItemOrder() != null;

        switch (this) {
            case RECALL:
                // Recall may write common (e.g. a recall-generated request id
                // that downstream operators consume): a common write is a
                // normal mutating hazard participant and the DAG builds correct
                // edges from common_output regardless of operator type. It
                // still must not mutate/remove/reorder existing items.
                if (hasItemWrites) violations.add("SetItem");
                if (hasRemovedItems) violations.add("RemoveItem");
                if (hasItemOrder) violations.add("SetItemOrder");
                break;
            case TRANSFORM:
                if (hasAddedItems) violations.add("AddItem");
                if (hasRemovedItems) violations.add("RemoveItem");
                if (hasItemOrder) violations.add("SetItemOrder");
                break;
            case FILTER:
                if (hasCommonWrites) violations.add("SetCommon");
                if (hasItemWrites) violations.add("SetItem");
                if (hasAddedItems) violations.add("AddItem");
                if (hasItemOrder) violations.add("SetItemOrder");
                break;
            case MERGE:
                if (hasCommonWrites) violations.add("SetCommon");
                if (hasAddedItems) violations.add("AddItem");
                if (hasItemOrder) violations.add("SetItemOrder");
                break;
            case REORDER:
                if (hasCommonWrites) violations.add("SetCommon");
                if (hasItemWrites) violations.add("SetItem");
                if (hasAddedItems) violations.add("AddItem");
                if (hasRemovedItems) violations.add("RemoveItem");
                break;
            case OBSERVE:
                if (hasCommonWrites) violations.add("SetCommon");
                if (hasItemWrites) violations.add("SetItem");
                if (hasAddedItems) violations.add("AddItem");
                if (hasRemovedItems) violations.add("RemoveItem");
                if (hasItemOrder) violations.add("SetItemOrder");
                break;
        }

        if (violations.isEmpty()) {
            return null;
        }
        String typeName = name().charAt(0) + name().substring(1).toLowerCase();
        StringBuilder sb = new StringBuilder("[");
        for (int i = 0; i < violations.size(); i++) {
            if (i > 0) sb.append(" ");
            sb.append(violations.get(i));
        }
        sb.append("]");
        return "operator type " + typeName + " must not call " + sb;
    }
}
