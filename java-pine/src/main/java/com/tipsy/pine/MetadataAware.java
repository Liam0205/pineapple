package com.tipsy.pine;

import java.util.List;

public interface MetadataAware {
    void setMetadata(List<String> commonInput, List<String> commonOutput,
                     List<String> itemInput, List<String> itemOutput);
}
