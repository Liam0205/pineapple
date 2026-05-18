package page.liam.pine;

import java.util.Map;

public interface StatsProvider {
    Map<String, Long> operatorStats();
}
