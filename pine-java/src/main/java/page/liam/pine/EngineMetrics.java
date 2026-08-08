package page.liam.pine;

import java.util.List;

import page.liam.pine.metrics.*;

public class EngineMetrics {
    public final Counter schedulerRuns;
    public final Gauge activeOps;
    public final Counter opExecTotal;
    public final Histogram opExecDuration;
    public final Counter opSkipTotal;
    public final Counter opErrorTotal;
    public final Counter dagExecTotal;
    public final Histogram dagExecDuration;
    public final Histogram dagOpsExecuted;

    public EngineMetrics(Provider provider) {
        this.schedulerRuns = provider.newCounter(new MetricOpts("pine_scheduler_runs_total", "Total number of DAG scheduler runs."));
        this.activeOps = provider.newGauge(new MetricOpts("pine_operator_active", "Number of operators currently executing."));
        this.opExecTotal = provider.newCounter(new MetricOpts("pine_operator_exec_total", "Total successful operator executions.", "operator"));
        // Buckets here (and on the other histograms) are a SUGGESTION to an external
        // backend, not a working configuration: no bundled Provider reads them —
        // MetricsCollector aggregates count and sum only. Authoritative contract:
        // pine-go/pkg/metrics/metrics.go.
        this.opExecDuration = provider.newHistogram(new HistogramOpts("pine_operator_exec_duration_seconds", "Operator execution duration in seconds.",
                new double[]{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 5.0}, "operator"));
        this.opSkipTotal = provider.newCounter(new MetricOpts("pine_operator_skip_total", "Total skipped operator executions.", "operator"));
        this.opErrorTotal = provider.newCounter(new MetricOpts("pine_operator_error_total", "Total failed operator executions.", "operator"));
        this.dagExecTotal = provider.newCounter(new MetricOpts("pine_dag_executions_total", "Total DAG executions.", "status"));
        this.dagExecDuration = provider.newHistogram(new HistogramOpts("pine_dag_execution_duration_seconds", "DAG execution duration in seconds.",
                new double[]{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}));
        this.dagOpsExecuted = provider.newHistogram(new HistogramOpts("pine_dag_operators_executed", "Number of operators executed (not skipped or cancelled) per DAG run.",
                new double[]{1, 2, 5, 10, 15, 20, 25, 30, 40, 50, 75, 100, 150, 200, 300, 450}));
    }

    public void preInitOperators(List<String> opNames) {
        for (String name : opNames) {
            opExecTotal.with(name);
            opExecDuration.with(name);
            opSkipTotal.with(name);
            opErrorTotal.with(name);
        }
        dagExecTotal.with("success");
        dagExecTotal.with("error");
    }
}
