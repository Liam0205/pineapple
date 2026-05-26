"""Engine-level metrics mirroring pine-go/internal/runtime/engine_metrics.go.

Created once at Engine.create() time from a metrics.Provider.  All metric
handles are pre-created so the request hot-path never allocates new handles.
"""
from __future__ import annotations

from pine.metrics import (
    Counter,
    Gauge,
    Histogram,
    HistogramOpts,
    MetricOpts,
    Provider,
)


class EngineMetrics:
    """Pre-created metric handles for the scheduler and per-operator recording."""

    __slots__ = (
        "scheduler_runs",
        "active_ops",
        "op_exec_total",
        "op_exec_duration",
        "op_skip_total",
        "op_error_total",
        "dag_exec_total",
        "dag_exec_duration",
        "dag_ops_executed",
    )

    def __init__(self, provider: Provider) -> None:
        op_labels = ("operator",)

        self.scheduler_runs: Counter = provider.new_counter(MetricOpts(
            name="pine_scheduler_runs_total",
            help="Total number of DAG scheduler runs.",
        ))
        self.active_ops: Gauge = provider.new_gauge(MetricOpts(
            name="pine_operator_active",
            help="Number of operators currently executing.",
        ))
        self.op_exec_total: Counter = provider.new_counter(MetricOpts(
            name="pine_operator_exec_total",
            help="Total successful operator executions.",
            label_names=op_labels,
        ))
        self.op_exec_duration: Histogram = provider.new_histogram(HistogramOpts(
            name="pine_operator_exec_duration_seconds",
            help="Operator execution duration in seconds.",
            buckets=(0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 5.0),
            label_names=op_labels,
        ))
        self.op_skip_total: Counter = provider.new_counter(MetricOpts(
            name="pine_operator_skip_total",
            help="Total skipped operator executions.",
            label_names=op_labels,
        ))
        self.op_error_total: Counter = provider.new_counter(MetricOpts(
            name="pine_operator_error_total",
            help="Total failed operator executions.",
            label_names=op_labels,
        ))
        self.dag_exec_total: Counter = provider.new_counter(MetricOpts(
            name="pine_dag_executions_total",
            help="Total DAG executions.",
            label_names=("status",),
        ))
        self.dag_exec_duration: Histogram = provider.new_histogram(HistogramOpts(
            name="pine_dag_execution_duration_seconds",
            help="DAG execution duration in seconds.",
            buckets=(0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0),
        ))
        self.dag_ops_executed: Histogram = provider.new_histogram(HistogramOpts(
            name="pine_dag_operators_executed",
            help="Number of operators executed (not skipped or cancelled) per DAG run.",
            buckets=(1, 2, 5, 10, 15, 20, 25, 30, 40, 50, 75, 100, 150, 200, 300, 450),
        ))

    def pre_init_operators(self, op_names: list[str]) -> None:
        """Pre-initialize labeled time series for all known operators.

        Ensures metrics backends (e.g. Prometheus) expose them from startup
        with zero values, rather than only after the first observation.
        """
        for name in op_names:
            self.op_exec_total.with_(name)
            self.op_exec_duration.with_(name)
            self.op_skip_total.with_(name)
            self.op_error_total.with_(name)
        self.dag_exec_total.with_("success")
        self.dag_exec_total.with_("error")
