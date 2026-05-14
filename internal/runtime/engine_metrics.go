package runtime

import "github.com/Liam0205/pineapple/pkg/metrics"

// EngineMetrics holds pre-created metrics for the scheduler and per-operator
// recording. Created once at NewEngine time from a metrics.Provider.
type EngineMetrics struct {
	SchedulerRuns  metrics.Counter
	ActiveOps      metrics.Gauge
	OpExecTotal    metrics.Counter
	OpExecDuration metrics.Histogram
	OpSkipTotal    metrics.Counter
	OpErrorTotal   metrics.Counter

	DAGExecTotal    metrics.Counter
	DAGExecDuration metrics.Histogram
	DAGOpsExecuted  metrics.Histogram
}

// NewEngineMetrics creates all engine-level metrics from the given provider.
func NewEngineMetrics(p metrics.Provider) *EngineMetrics {
	opLabels := []string{"operator"}
	return &EngineMetrics{
		SchedulerRuns: p.NewCounter(metrics.MetricOpts{
			Name: "pine_scheduler_runs_total",
			Help: "Total number of DAG scheduler runs.",
		}),
		ActiveOps: p.NewGauge(metrics.MetricOpts{
			Name: "pine_operator_active",
			Help: "Number of operators currently executing.",
		}),
		OpExecTotal: p.NewCounter(metrics.MetricOpts{
			Name:       "pine_operator_exec_total",
			Help:       "Total successful operator executions.",
			LabelNames: opLabels,
		}),
		OpExecDuration: p.NewHistogram(metrics.HistogramOpts{
			MetricOpts: metrics.MetricOpts{
				Name:       "pine_operator_exec_duration_seconds",
				Help:       "Operator execution duration in seconds.",
				LabelNames: opLabels,
			},
			Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 5.0},
		}),
		OpSkipTotal: p.NewCounter(metrics.MetricOpts{
			Name:       "pine_operator_skip_total",
			Help:       "Total skipped operator executions.",
			LabelNames: opLabels,
		}),
		OpErrorTotal: p.NewCounter(metrics.MetricOpts{
			Name:       "pine_operator_error_total",
			Help:       "Total failed operator executions.",
			LabelNames: opLabels,
		}),
		DAGExecTotal: p.NewCounter(metrics.MetricOpts{
			Name:       "pine_dag_executions_total",
			Help:       "Total DAG executions.",
			LabelNames: []string{"status"},
		}),
		DAGExecDuration: p.NewHistogram(metrics.HistogramOpts{
			MetricOpts: metrics.MetricOpts{
				Name: "pine_dag_execution_duration_seconds",
				Help: "DAG execution duration in seconds.",
			},
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
		}),
		DAGOpsExecuted: p.NewHistogram(metrics.HistogramOpts{
			MetricOpts: metrics.MetricOpts{
				Name: "pine_dag_operators_executed",
				Help: "Number of operators executed (not skipped) per DAG run.",
			},
		}),
	}
}
