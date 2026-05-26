#include "pine/metrics.hpp"

namespace pine {
namespace metrics {

namespace {

class NopCounter : public Counter {
public:
    Counter* with(const std::vector<std::string>&) override { return this; }
    void inc() override {}
};

class NopGauge : public Gauge {
public:
    Gauge* with(const std::vector<std::string>&) override { return this; }
    void set(double) override {}
    void add(double) override {}
};

class NopHistogram : public Histogram {
public:
    Histogram* with(const std::vector<std::string>&) override { return this; }
    void observe(double) override {}
};

class NopProvider : public Provider {
public:
    Counter* new_counter(const MetricOpts&) override { return &counter_; }
    Gauge* new_gauge(const MetricOpts&) override { return &gauge_; }
    Histogram* new_histogram(const HistogramOpts&) override { return &histogram_; }

private:
    NopCounter counter_;
    NopGauge gauge_;
    NopHistogram histogram_;
};

}  // namespace

Provider* nop_provider() {
    static NopProvider instance;
    return &instance;
}

}  // namespace metrics
}  // namespace pine
