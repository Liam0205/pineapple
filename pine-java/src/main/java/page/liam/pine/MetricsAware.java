package page.liam.pine;

import page.liam.pine.metrics.Provider;

public interface MetricsAware {
    void setMetricsProvider(Provider provider);
}
