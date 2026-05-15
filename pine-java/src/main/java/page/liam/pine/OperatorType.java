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
        boolean hasItemWrites = !out.getItemWrites().isEmpty();
        boolean hasAddedItems = !out.getAddedItems().isEmpty();
        boolean hasRemovedItems = !out.getRemovedItems().isEmpty();
        boolean hasItemOrder = out.getItemOrder() != null;

        switch (this) {
            case RECALL:
                if (hasCommonWrites) violations.add("SetCommon");
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
        return "operator type " + name().toLowerCase() + " must not call " + violations;
    }
}
