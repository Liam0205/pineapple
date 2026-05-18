package page.liam.pine;

import java.util.List;

public abstract class AbstractOperator implements Operator, MetadataAware {
    private List<String> commonInput = List.of();
    private List<String> commonOutput = List.of();
    private List<String> itemInput = List.of();
    private List<String> itemOutput = List.of();

    @Override
    public final void setMetadata(List<String> commonInput, List<String> commonOutput,
                            List<String> itemInput, List<String> itemOutput) {
        this.commonInput = List.copyOf(commonInput);
        this.commonOutput = List.copyOf(commonOutput);
        this.itemInput = List.copyOf(itemInput);
        this.itemOutput = List.copyOf(itemOutput);
    }

    protected List<String> commonInput() { return commonInput; }
    protected List<String> commonOutput() { return commonOutput; }
    protected List<String> itemInput() { return itemInput; }
    protected List<String> itemOutput() { return itemOutput; }
}
