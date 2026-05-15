package page.liam.pine;

import java.util.List;

public abstract class AbstractOperator implements Operator, MetadataAware {
    protected List<String> commonInput;
    protected List<String> commonOutput;
    protected List<String> itemInput;
    protected List<String> itemOutput;

    @Override
    public void setMetadata(List<String> commonInput, List<String> commonOutput,
                            List<String> itemInput, List<String> itemOutput) {
        this.commonInput = commonInput;
        this.commonOutput = commonOutput;
        this.itemInput = itemInput;
        this.itemOutput = itemOutput;
    }
}
