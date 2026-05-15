package page.liam.pine;

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
        this.schedulerRuns = provider.newCounter(new MetricOpts("pine_scheduler_runs_total", "Total scheduler runs"));
        this.activeOps = provider.newGauge(new MetricOpts("pine_operator_active", "Currently executing operators"));
        this.opExecTotal = provider.newCounter(new MetricOpts("pine_operator_exec_total", "Operator executions", "operator"));
        this.opExecDuration = provider.newHistogram(new HistogramOpts("pine_operator_exec_duration_seconds", "Operator execution duration",
                new double[]{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 5.0}, "operator"));
        this.opSkipTotal = provider.newCounter(new MetricOpts("pine_operator_skip_total", "Operator skips", "operator"));
        this.opErrorTotal = provider.newCounter(new MetricOpts("pine_operator_error_total", "Operator errors", "operator"));
        this.dagExecTotal = provider.newCounter(new MetricOpts("pine_dag_executions_total", "DAG executions", "status"));
        this.dagExecDuration = provider.newHistogram(new HistogramOpts("pine_dag_execution_duration_seconds", "DAG execution duration",
                new double[]{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}));
        this.dagOpsExecuted = provider.newHistogram(new HistogramOpts("pine_dag_operators_executed", "Operators executed per DAG run",
                new double[]{1, 5, 10, 20, 50, 100, 200}));
    }
}
