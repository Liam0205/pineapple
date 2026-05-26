"""Pine Engine -- DAG-based operator execution runtime.

Mirrors the Pine-Java Engine architecture: compile-once, execute-many.
"""
from __future__ import annotations

import json
import os
import sys
import threading
import time
import traceback
from concurrent.futures import Future, InvalidStateError, ThreadPoolExecutor
from dataclasses import dataclass
from typing import Any, Self

from pine.cancellation import CancellationToken
from pine.config import Config, InputFieldSpec, OperatorConfig
from pine.dag import DAG
from pine.errors import (
    ConfigError,
    ExecutionError,
    OperatorException,
    PanicError,
    RegistryError,
    ValidationError,
)
from pine.frame import Frame
from pine.operator import (
    AdditiveWritesRowSet,
    ConcurrentSafe,
    ConsumesRowSet,
    DebugAware,
    MetadataAware,
    MetricsAware,
    MutatesRowSet,
    Operator,
    OperatorInput,
    OperatorOutput,
    OperatorType,
    ResourceAware,
    StatsProvider,
)
from pine.parallel import _parallel_execute
from pine.registry import Registry
from pine.result import OpTrace, Result, Warning
from pine.stats import _Stats

# ---------------------------------------------------------------------------
# CompiledOperator
# ---------------------------------------------------------------------------


@dataclass
class CompiledOperator:
    """Holds an operator instance together with its compile-time configuration."""
    name: str
    instance: Operator
    config: OperatorConfig
    debug: bool
    recall: bool
    data_parallel: int
    operator_type: str


# ---------------------------------------------------------------------------
# Engine
# ---------------------------------------------------------------------------

def _ensure_operators_registered():
    """Import operators module to trigger registration (if it exists)."""
    try:
        from pine import operators  # noqa: F401
        if hasattr(operators, "ensure_registered"):
            operators.ensure_registered()
    except ImportError:
        pass


class Engine:
    """Pipeline execution engine: builds DAG, schedules operators, returns results."""

    def __init__(
        self,
        compiled_operators: list[CompiledOperator],
        dag: DAG,
        contract: Any,
        resource_provider: Any | None,
        metrics_provider: Any | None,
        storage_mode: str,
        stats: _Stats,
    ):
        self._operators = compiled_operators
        self._dag = dag
        self._contract = contract
        self._resource_provider = resource_provider
        self._metrics_provider = metrics_provider
        self._storage_mode = storage_mode
        self._stats = stats
        self._pool_size = max((os.cpu_count() or 1) * 2, 4)

    # ------------------------------------------------------------------
    # Factory
    # ------------------------------------------------------------------

    @classmethod
    def create(
        cls,
        json_config: bytes,
        resource_provider: Any | None = None,
        metrics_provider: Any | None = None,
    ) -> Self:
        """Compile a Pine engine from JSON config bytes.

        Args:
            json_config: Raw JSON configuration as bytes.
            resource_provider: Optional resource provider for ResourceAware operators.
            metrics_provider: Optional metrics provider for MetricsAware operators.

        Returns:
            An immutable Engine instance ready for concurrent execution.

        Raises:
            ConfigError: If the configuration is invalid.
            ValidationError: If operator constraints are violated.
        """
        _ensure_operators_registered()

        cfg = Config.load(json_config)
        expanded = cfg.expand_operator_sequence_with_sub_flows()
        sequence = expanded.sequence
        op_to_sub_flow = expanded.op_to_sub_flow

        _validate_sources_order(sequence, cfg.pipeline_config.operators)

        # Resolve global debug
        global_debug = cfg.debug

        registry = Registry.global_instance()
        compiled_ops: list[CompiledOperator] = []

        for name in sequence:
            op_cfg = cfg.pipeline_config.operators[name]

            # Build operator instance
            op = registry.build_operator(op_cfg.type_name, op_cfg.raw_params)

            # Resolve operator type from registry schema
            schema = registry.get_schema(op_cfg.type_name)
            if schema is not None:
                op_type = schema.type
                effective_operator_type = op_type.value
            else:
                op_type = OperatorType.TRANSFORM
                effective_operator_type = "transform"
            op_cfg.operator_type = effective_operator_type

            # Infer row-set semantics from operator interfaces
            if isinstance(op, ConsumesRowSet):
                op_cfg.consumes_row_set = True
            if isinstance(op, MutatesRowSet):
                op_cfg.mutates_row_set = True
            if isinstance(op, AdditiveWritesRowSet):
                op_cfg.additive_writes_row_set = True
            # Validate row-set marker constraints
            if op_cfg.additive_writes_row_set and op_cfg.mutates_row_set:
                raise RegistryError(
                    f'operator "{name}": AdditiveWritesRowSet and '
                    f'MutatesRowSet are mutually exclusive'
                )
            if op_type == OperatorType.RECALL and not op_cfg.additive_writes_row_set:
                raise RegistryError(
                    f'operator "{name}": Recall type must implement AdditiveWritesRowSet'
                )

            effective_debug = global_debug or op_cfg.debug
            effective_recall = op_cfg.recall or op_type == OperatorType.RECALL

            # Injection: MetadataAware -> DebugAware -> MetricsAware -> ResourceAware
            if isinstance(op, MetadataAware):
                common_in = [f for f in op_cfg.metadata.common_input
                             if f not in op_cfg.skip]
                op.set_metadata(
                    common_in,
                    op_cfg.metadata.common_output,
                    op_cfg.metadata.item_input,
                    op_cfg.metadata.item_output,
                )

            if isinstance(op, DebugAware):
                op.set_debug(name, effective_debug)

            if isinstance(op, MetricsAware):
                op.set_metrics_provider(metrics_provider)

            if isinstance(op, ResourceAware):
                # Align with pine-{go,cpp}: don't fail at init when no provider is
                # supplied. Operators check for the provider at execute time so
                # pipelines without resource-dependent operators can be constructed
                # even when no provider exists.
                if resource_provider is not None:
                    op.set_resource_provider(resource_provider)

            # Validate data_parallel constraints
            effective_data_parallel = op_cfg.data_parallel
            if effective_data_parallel < 0:
                raise ValidationError(
                    f'operator "{name}": data_parallel must be >= 1, '
                    f"got {effective_data_parallel}"
                )
            if effective_data_parallel == 0:
                effective_data_parallel = 1
            if effective_data_parallel > 1:
                if op_type != OperatorType.TRANSFORM:
                    raise ValidationError(
                        f'operator "{name}": data_parallel={effective_data_parallel} '
                        f"is only supported for Transform operators, "
                        f"got {op_type.value}"
                    )
                if op_cfg.metadata.common_output:
                    raise ValidationError(
                        f'operator "{name}": data_parallel={effective_data_parallel} '
                        f"requires empty $metadata.common_output for Transform operators"
                    )
                if not isinstance(op, ConcurrentSafe):
                    raise ValidationError(
                        f'operator "{name}": data_parallel={effective_data_parallel} '
                        f"requires the operator to implement ConcurrentSafe interface "
                        f'(type "{op_cfg.type_name}" does not)'
                    )

            # Pre-compute InputFieldSpec
            op_cfg.input_spec = InputFieldSpec.compute(
                op_cfg.metadata, op_cfg.common_defaults, op_cfg.item_defaults, op_cfg.skip
            )

            compiled_ops.append(CompiledOperator(
                name=name,
                instance=op,
                config=op_cfg,
                debug=effective_debug,
                recall=effective_recall,
                data_parallel=effective_data_parallel,
                operator_type=effective_operator_type,
            ))

        dag = DAG.build(sequence, cfg.pipeline_config.operators, op_to_sub_flow)
        engine_stats = _Stats()
        engine_stats.pre_init_operators([cop.name for cop in compiled_ops])

        return cls(
            compiled_operators=compiled_ops,
            dag=dag,
            contract=cfg.flow_contract,
            resource_provider=resource_provider,
            metrics_provider=metrics_provider,
            storage_mode=cfg.storage_mode,
            stats=engine_stats,
        )

    # ------------------------------------------------------------------
    # Execution
    # ------------------------------------------------------------------

    def execute(
        self,
        common: dict[str, Any] | None,
        items: list[dict[str, Any]] | None = None,
    ) -> Result:
        """Execute the pipeline on the given request data.

        Args:
            common: Common fields (must not be None).
            items: List of item dicts (may be None or empty).

        Returns:
            Result with common, items, warnings, trace, and error.

        Raises:
            ValidationError: If the flow contract is violated.
        """
        if common is None:
            raise ValidationError("request.Common must not be nil")

        # Validate flow contract
        for field_name in self._contract.common_input:
            if field_name not in common:
                raise ValidationError(
                    f'missing required common input field "{field_name}"'
                )

        if items and self._contract.item_input:
            for i, item in enumerate(items):
                for field_name in self._contract.item_input:
                    if field_name not in item:
                        raise ValidationError(
                            f'item[{i}] missing required item input field "{field_name}"'
                        )

        frame = Frame.create(self._storage_mode, common, items)
        n = len(self._operators)

        self._stats.record_run()

        # Per-operator futures for DAG-based scheduling
        futures: list[Future[None]] = [Future() for _ in range(n)]
        traces: list[OpTrace | None] = [None] * n
        warnings: list[Warning] = []
        warnings_lock = threading.Lock()
        fatal_error: list[Exception | None] = [None]
        fatal_lock = threading.Lock()
        cancellation_token = CancellationToken()
        active_ops = [0]
        active_ops_lock = threading.Lock()

        def _set_fatal(err: Exception):
            with fatal_lock:
                if fatal_error[0] is not None:
                    return False
                fatal_error[0] = err
            cancellation_token.cancel()
            for f in futures:
                if not f.done():
                    try:
                        f.set_result(None)
                    except InvalidStateError:
                        pass
            return True

        def _run_operator(idx: int):
            cop = self._operators[idx]
            node = self._dag.nodes[idx]
            op_name = cop.name

            try:
                # Wait for all predecessors
                for pred_idx in node.preds:
                    pred_future = futures[pred_idx]
                    try:
                        pred_future.result()
                    except Exception:
                        pass
                    if fatal_error[0] is not None:
                        return
                    if cancellation_token.is_cancelled():
                        return

                if fatal_error[0] is not None:
                    return

                _execute_operator(idx, cop, frame, traces, warnings, warnings_lock,
                                  cancellation_token, active_ops, active_ops_lock,
                                  _set_fatal)

            except Exception:
                err = PanicError(
                    f"pine: operator \"{op_name}\": unexpected panic",
                    detail=traceback.format_exc(),
                )
                _set_fatal(err)
            finally:
                f = futures[idx]
                if not f.done():
                    try:
                        f.set_result(None)
                    except Exception:
                        pass

        def _execute_operator(
            idx: int,
            cop: CompiledOperator,
            frame: Frame,
            traces: list[OpTrace | None],
            warnings_list: list[Warning],
            warnings_lk: threading.Lock,
            token: CancellationToken,
            active: list[int],
            active_lk: threading.Lock,
            set_fatal,
        ):
            op_cfg = cop.config
            start_ns = time.perf_counter_ns()

            # Evaluate skip: any truthy skip field causes the operator to be skipped.
            skipped = False
            if op_cfg.skip:
                skipped = frame.check_skip(op_cfg.skip)

            if skipped:
                duration_ns = time.perf_counter_ns() - start_ns
                traces[idx] = OpTrace(
                    name=cop.name,
                    duration_ns=duration_ns,
                    skipped=True,
                )
                self._stats.record_skip(cop.name)
                return

            # Build input (uses precomputed InputFieldSpec)
            try:
                op_input = frame.build_input(cop.name, op_cfg.input_spec)
            except ExecutionError as e:
                set_fatal(e)
                return

            # Debug: capture input snapshot
            input_snapshot = None
            if cop.debug:
                input_snapshot = _snapshot_input(op_input)

            # Track concurrency
            with active_lk:
                active[0] += 1
                current = active[0]
            self._stats.record_concurrency(current)

            # Execute
            output: OperatorOutput | None = None
            exec_err: Exception | None = None
            try:
                if cop.data_parallel > 1:
                    output = _parallel_execute(
                        token, cop.instance, op_input, cop.data_parallel, cop.name
                    )
                else:
                    output = OperatorOutput()
                    cop.instance.execute(token, op_input, output)
            except OperatorException as e:
                exec_err = e
            except Exception as e:
                exec_err = e

            # Decrement active
            with active_lk:
                active[0] -= 1

            # Validate output type constraints
            if exec_err is None and output is not None:
                schema = Registry.global_instance()._schemas.get(op_cfg.type_name)
                if schema is not None:
                    violation = schema.type.validate_output(output)
                    if violation is not None:
                        exec_err = OperatorException(f"type violation: {violation}")

            duration_ns = time.perf_counter_ns() - start_ns

            if exec_err is not None:
                traces[idx] = OpTrace(
                    name=cop.name,
                    duration_ns=duration_ns,
                    skipped=False,
                    input_snapshot=input_snapshot,
                )
                self._stats.record_error(cop.name, duration_ns)
                # Classify error. Preserve the original exception object as
                # __cause__ so downstream code can walk the chain via
                # ``err.__cause__`` (mirrors Go errors.As / pine-cpp
                # std::nested_exception). The user-facing message is
                # unchanged so cross-validate Section 5 stays byte-exact.
                if isinstance(exec_err, PanicError):
                    wrapped = exec_err
                elif isinstance(exec_err, OperatorException):
                    wrapped = PanicError(
                        f'pine: execution error in operator "{cop.name}": {exec_err}',
                        detail="",
                        cause=exec_err,
                    )
                else:
                    wrapped = PanicError(
                        f'pine: panic in operator "{cop.name}": unexpected panic',
                        detail=traceback.format_exc(),
                        cause=exec_err,
                    )
                set_fatal(wrapped)
                return

            # Collect warning
            if output.warning is not None:
                with warnings_lk:
                    warnings_list.append(Warning(operator=cop.name, err=output.warning))

            # Debug: capture output snapshot and log
            output_snapshot = None
            if cop.debug:
                output_snapshot = _snapshot_output(output)
                input_size = op_input.item_count()
                output_size = (input_size
                               + len(output.added_items)
                               - len(output.removed_items))
                input_json = json.dumps(input_snapshot) if input_snapshot else "{}"
                output_json = json.dumps(output_snapshot) if output_snapshot else "{}"
                print(
                    f'[pine-debug] operator="{cop.name}" '
                    f"duration={_format_duration(duration_ns)} "
                    f"input_size={input_size} output_size={output_size} "
                    f"input={input_json} output={output_json}",
                    file=sys.stderr,
                )

            # Apply output
            try:
                frame.apply_output(output, cop.name, cop.recall)
            except Exception as apply_err:
                traces[idx] = OpTrace(
                    name=cop.name,
                    duration_ns=duration_ns,
                    skipped=False,
                    input_snapshot=input_snapshot,
                    output_snapshot=output_snapshot,
                )
                self._stats.record_error(cop.name, duration_ns)
                # ExecutionError thrown from apply_output (NaN/Inf validation,
                # SetItemOrder permutation check) already carries the operator
                # name and a structured `operator "X": <segment>: <msg>` body
                # — wrap it once with the standard execution-error prefix to
                # match Go/Java/C++ byte-for-byte. Other exceptions get the
                # original `apply output: ` segment for legacy reasons. R3-H2.
                if isinstance(apply_err, ExecutionError):
                    # str(ExecutionError) already starts with `operator "X": `;
                    # strip the redundant `operator "X": ` so the final wrap
                    # reads `pine: execution error in operator "X": <segment>: <msg>`.
                    inner = str(apply_err)
                    prefix = f'operator "{cop.name}": '
                    if inner.startswith(prefix):
                        inner = inner[len(prefix):]
                    wrapped = PanicError(
                        f'pine: execution error in operator "{cop.name}": {inner}',
                        detail=traceback.format_exc(),
                        cause=apply_err,
                    )
                else:
                    wrapped = PanicError(
                        f'pine: execution error in operator "{cop.name}": apply output: {apply_err}',
                        detail=traceback.format_exc(),
                        cause=apply_err,
                    )
                set_fatal(wrapped)
                return

            traces[idx] = OpTrace(
                name=cop.name,
                duration_ns=duration_ns,
                skipped=False,
                input_snapshot=input_snapshot,
                output_snapshot=output_snapshot,
            )
            self._stats.record_exec(cop.name, duration_ns)

        # Submit all operators to thread pool
        with ThreadPoolExecutor(max_workers=self._pool_size) as pool:
            submitted = [pool.submit(_run_operator, i) for i in range(n)]
            # Wait for all to complete
            for f in submitted:
                try:
                    f.result()
                except Exception:
                    pass

        # Collect non-null traces
        trace_list = [t for t in traces if t is not None]

        # Project result
        result_common = frame.to_result_common(self._contract.common_output)
        result_items = frame.to_result_items(self._contract.item_output)

        return Result(
            common=result_common,
            items=result_items,
            warnings=warnings,
            trace=trace_list,
            error=fatal_error[0],
        )

    # ------------------------------------------------------------------
    # Public stats API
    # ------------------------------------------------------------------

    def stats(self) -> dict[str, dict[str, Any]]:
        """Return per-operator execution stats (sorted alphabetically)."""
        return self._stats.snapshot()

    def scheduler_stats(self) -> dict[str, Any]:
        """Return scheduler-level stats."""
        return self._stats.scheduler_snapshot()

    def operator_custom_stats(self) -> dict[str, dict[str, int]] | None:
        """Collect custom stats from operators implementing StatsProvider."""
        result: dict[str, dict[str, int]] = {}
        for cop in self._operators:
            if isinstance(cop.instance, StatsProvider):
                custom = cop.instance.operator_stats()
                if custom:
                    result[cop.name] = custom
        return result if result else None

    def render_dag(self, fmt: str, collapse: int = 0) -> str:
        """Render the DAG in the given format.

        Args:
            fmt: "dot" or "mermaid".
            collapse: SubFlow collapse level (0 = full).

        Returns:
            Rendered DAG string.

        Raises:
            ValidationError: If format is unknown.
        """
        if fmt not in ("dot", "mermaid"):
            raise ValidationError(
                f'unsupported DAG format "{fmt}" (use "dot" or "mermaid")'
            )

        from pine.visualize import (
            render_collapsed_dot,
            render_collapsed_mermaid,
            render_dot,
            render_mermaid,
        )

        if collapse > 0:
            if fmt == "dot":
                return render_collapsed_dot(self._dag, collapse)
            else:
                return render_collapsed_mermaid(self._dag, collapse)
        else:
            if fmt == "dot":
                return render_dot(self._dag)
            else:
                return render_mermaid(self._dag)


# ---------------------------------------------------------------------------
# Module-level helpers
# ---------------------------------------------------------------------------


def _validate_sources_order(
    sequence: list[str],
    operators: dict[str, OperatorConfig],
):
    """Validate that all source references point to earlier operators in sequence."""
    seen: set[str] = set()
    for name in sequence:
        op_cfg = operators.get(name)
        if op_cfg is not None:
            for src in op_cfg.sources:
                if src not in seen:
                    raise ValidationError(
                        f'operator "{name}": sources references "{src}" which is '
                        f"declared after the current operator (forward reference)"
                    )
        seen.add(name)

def _snapshot_input(input_: OperatorInput) -> dict[str, Any]:
    """Capture a debug snapshot of operator input."""
    snap: dict[str, Any] = {}
    common = input_.raw_common()
    if common:
        snap["common"] = dict(common)
    items = input_.raw_items()
    if items:
        has_data = any(bool(row) for row in items)
        if has_data:
            snap["items"] = [dict(row) for row in items]
    return snap


def _snapshot_output(output: OperatorOutput) -> dict[str, Any]:
    """Capture a debug snapshot of operator output."""
    snap: dict[str, Any] = {}
    if output.common_writes:
        snap["common_writes"] = dict(output.common_writes)
    if output.item_writes:
        snap["item_writes"] = dict(output.item_writes)
    if output.added_items:
        snap["added_items"] = list(output.added_items)
    if output.removed_items:
        snap["removed_items"] = list(output.removed_items)
    return snap


class ResourceResult:
    def __init__(self, value: Any, found: bool):
        self._value = value
        self._found = found

    def exists(self) -> bool:
        return self._found

    def value(self) -> Any:
        return self._value


class StaticResourceProvider:
    def __init__(self, resources: dict[str, Any] | None):
        self._resources = resources or {}

    def get(self, name: str) -> ResourceResult:
        if name in self._resources:
            return ResourceResult(self._resources[name], True)
        return ResourceResult(None, False)


def _format_duration(nanos: int) -> str:
    """Format duration for debug logging."""
    if nanos < 1_000_000:
        return f"{nanos / 1000.0}µs"
    return f"{nanos / 1_000_000.0}ms"
