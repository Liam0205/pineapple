package page.liam.pine;

import java.util.List;
import java.util.Map;

public interface Frame {
    Object common(String field);
    int itemCount();
    OperatorInput buildInput(List<String> commonFields, List<String> itemFields,
                             Map<String, Object> commonDefaults, Map<String, Object> itemDefaults);
    void applyOutput(OperatorOutput out, String opName, boolean recall) throws Exception;
    Map<String, Object> toResultCommon(List<String> commonOut);
    List<Map<String, Object>> toResultItems(List<String> itemOut);

    static Frame create(String storageMode, Map<String, Object> common, List<Map<String, Object>> items) {
        if ("column".equalsIgnoreCase(storageMode)) {
            return new ColumnFrame(common, items);
        }
        return new DataFrame(common, items);
    }
}
