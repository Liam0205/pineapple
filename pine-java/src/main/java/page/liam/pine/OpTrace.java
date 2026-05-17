package page.liam.pine;

import java.util.Collections;
import java.util.Map;

public class OpTrace {
    public final String name;
    public final long startTimeNs;
    public final long durationNs;
    public final boolean skipped;
    public final Map<String, Object> inputSnapshot;
    public final Map<String, Object> outputSnapshot;

    public OpTrace(String name, long startTimeNs, long durationNs, boolean skipped,
                   Map<String, Object> inputSnapshot, Map<String, Object> outputSnapshot) {
        this.name = name;
        this.startTimeNs = startTimeNs;
        this.durationNs = durationNs;
        this.skipped = skipped;
        this.inputSnapshot = inputSnapshot != null ? Collections.unmodifiableMap(inputSnapshot) : null;
        this.outputSnapshot = outputSnapshot != null ? Collections.unmodifiableMap(outputSnapshot) : null;
    }
}
