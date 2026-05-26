from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any

from pine.errors import ConfigError

RESERVED_KEYS = frozenset({
    "type_name", "$metadata", "$code_info", "skip", "recall", "sources",
    "debug", "consumes_row_set", "mutates_row_set",
    "additive_writes_row_set", "common_defaults", "item_defaults",
    "strict_common", "strict_item",
    "for_branch_control", "data_parallel",
})


@dataclass
class Metadata:
    common_input: list[str] = field(default_factory=list)
    common_output: list[str] = field(default_factory=list)
    item_input: list[str] = field(default_factory=list)
    item_output: list[str] = field(default_factory=list)


@dataclass
class OperatorConfig:
    """Per-operator configuration within a pipeline."""

    type_name: str = ""
    metadata: Metadata = field(default_factory=Metadata)
    skip: list[str] = field(default_factory=list)
    recall: bool = False
    sources: list[str] = field(default_factory=list)
    debug: bool | None = None
    consumes_row_set: bool = False
    mutates_row_set: bool = False
    additive_writes_row_set: bool = False
    for_branch_control: bool = False
    data_parallel: int = 1
    common_defaults: dict[str, Any] = field(default_factory=dict)
    item_defaults: dict[str, Any] = field(default_factory=dict)
    strict_common: list[str] = field(default_factory=list)
    strict_item: list[str] = field(default_factory=list)
    raw_params: dict[str, Any] = field(default_factory=dict)
    operator_type: str = ""
    input_spec: InputFieldSpec | None = None


class DefaultedField:
    __slots__ = ("name", "default")

    def __init__(self, name: str, default: Any):
        self.name = name
        self.default = default


class InputFieldSpec:
    """Resolved input field specification with defaults, strict, and nullable fields."""

    __slots__ = (
        "strict_common", "defaulted_common", "nullable_common",
        "strict_item", "defaulted_item", "nullable_item",
    )

    def __init__(
        self,
        strict_common: list[str],
        defaulted_common: list[DefaultedField],
        nullable_common: list[str],
        strict_item: list[str],
        defaulted_item: list[DefaultedField],
        nullable_item: list[str],
    ):
        self.strict_common = strict_common
        self.defaulted_common = defaulted_common
        self.nullable_common = nullable_common
        self.strict_item = strict_item
        self.defaulted_item = defaulted_item
        self.nullable_item = nullable_item

    @staticmethod
    def compute(
        metadata: "Metadata",
        common_defaults: dict[str, Any],
        item_defaults: dict[str, Any],
        strict_common: list[str],
        strict_item: list[str],
        skip: list[str],
    ) -> "InputFieldSpec":
        skip_set = set(skip)
        strict_common_set = set(strict_common)
        strict_item_set = set(strict_item)

        s_common: list[str] = []
        d_common: list[DefaultedField] = []
        n_common: list[str] = []
        for f in metadata.common_input:
            if f in skip_set:
                continue
            if f in common_defaults:
                d_common.append(DefaultedField(f, common_defaults[f]))
            elif f in strict_common_set:
                s_common.append(f)
            else:
                n_common.append(f)

        s_item: list[str] = []
        d_item: list[DefaultedField] = []
        n_item: list[str] = []
        for f in metadata.item_input:
            if f in item_defaults:
                d_item.append(DefaultedField(f, item_defaults[f]))
            elif f in strict_item_set:
                s_item.append(f)
            else:
                n_item.append(f)

        return InputFieldSpec(s_common, d_common, n_common, s_item, d_item, n_item)


class SubFlowRef:
    def __init__(self, pipeline: list[str] | None = None):
        self.pipeline: list[str] = pipeline or []


class FlowContract:
    """Declares the pipeline's required inputs and guaranteed outputs."""

    def __init__(self):
        self.common_input: list[str] = []
        self.item_input: list[str] = []
        self.common_output: list[str] = []
        self.item_output: list[str] = []


class PipelineConfig:
    """Holds operator configs and sub-flow pipeline map."""

    def __init__(self):
        self.operators: dict[str, OperatorConfig] = {}
        self.pipeline_map: dict[str, SubFlowRef] = {}


class ExpandResult:
    def __init__(self, sequence: list[str], op_to_sub_flow: dict[str, str]):
        self.sequence = sequence
        self.op_to_sub_flow = op_to_sub_flow


class Config:
    """Pipeline configuration parsed from JSON."""

    def __init__(self):
        self.pineapple_version: str = ""
        self.log_prefix: str = ""
        self.debug: bool = False
        self.storage_mode: str = "row"
        self.pipeline_config: PipelineConfig = PipelineConfig()
        self.pipeline_group: dict[str, SubFlowRef] = {}
        self.flow_contract: FlowContract = FlowContract()

    @classmethod
    def load(cls, json_data: bytes) -> "Config":
        try:
            root = json.loads(json_data)
        except Exception as e:
            raise ConfigError(f"failed to parse config JSON: {e}")

        cfg = Config()
        cfg.pineapple_version = root.get("_PINEAPPLE_VERSION", "")
        cfg.log_prefix = root.get("log_prefix", "")
        cfg.debug = root.get("debug", False)
        cfg.storage_mode = root.get("storage_mode", "row")

        fc = root.get("flow_contract", {})
        cfg.flow_contract.common_input = fc.get("common_input", [])
        cfg.flow_contract.item_input = fc.get("item_input", [])
        cfg.flow_contract.common_output = fc.get("common_output", [])
        cfg.flow_contract.item_output = fc.get("item_output", [])

        pg = root.get("pipeline_group", {})
        for name, val in pg.items():
            cfg.pipeline_group[name] = SubFlowRef(val.get("pipeline", []))

        pc = root.get("pipeline_config", {})
        pm = pc.get("pipeline_map", {})
        for name, val in pm.items():
            cfg.pipeline_config.pipeline_map[name] = SubFlowRef(val.get("pipeline", []))

        ops = pc.get("operators", {})
        for name, op_node in ops.items():
            op_cfg = _parse_operator_config(op_node)
            cfg.pipeline_config.operators[name] = op_cfg

        _validate(cfg)
        return cfg

    def expand_operator_sequence_with_sub_flows(self) -> ExpandResult:
        if "main" in self.pipeline_group:
            group = self.pipeline_group["main"]
        elif len(self.pipeline_group) == 1:
            group = next(iter(self.pipeline_group.values()))
        else:
            raise ConfigError(
                'pipeline_group must contain a "main" entry or exactly one entry'
            )

        for name in self.pipeline_config.operators:
            if name in self.pipeline_config.pipeline_map:
                raise ConfigError(
                    f'name "{name}" exists in both operators and pipeline_map'
                )

        sequence: list[str] = []
        op_to_sub_flow: dict[str, str] = {}
        visiting: set[str] = set()
        seen: set[str] = set()

        self._expand_entries(
            group.pipeline, "", sequence, op_to_sub_flow, visiting, seen
        )
        return ExpandResult(sequence, op_to_sub_flow)

    def _expand_entries(
        self,
        entries: list[str],
        parent_path: str,
        sequence: list[str],
        op_to_sub_flow: dict[str, str],
        visiting: set[str],
        seen: set[str],
    ):
        for entry in entries:
            if entry in self.pipeline_config.operators:
                if entry in seen:
                    raise ConfigError(
                        f'operator "{entry}" referenced more than once in pipeline tree'
                    )
                seen.add(entry)
                sequence.append(entry)
                op_to_sub_flow[entry] = parent_path
            elif entry in self.pipeline_config.pipeline_map:
                if entry in visiting:
                    raise ConfigError(
                        f'cycle detected in sub-flow expansion: "{entry}"'
                    )
                visiting.add(entry)
                self._expand_entries(
                    self.pipeline_config.pipeline_map[entry].pipeline,
                    entry,
                    sequence,
                    op_to_sub_flow,
                    visiting,
                    seen,
                )
                visiting.discard(entry)
            else:
                raise ConfigError(
                    f'pipeline entry "{entry}" is neither an operator nor a sub-flow'
                )


def _parse_operator_config(node: dict[str, Any]) -> OperatorConfig:
    op_cfg = OperatorConfig()
    op_cfg.type_name = node.get("type_name", "")
    op_cfg.recall = node.get("recall", False)
    op_cfg.debug = node.get("debug") if "debug" in node else None
    op_cfg.consumes_row_set = node.get("consumes_row_set", False)
    op_cfg.mutates_row_set = node.get("mutates_row_set", False)
    op_cfg.additive_writes_row_set = node.get("additive_writes_row_set", False)
    op_cfg.for_branch_control = node.get("for_branch_control", False)
    op_cfg.data_parallel = node.get("data_parallel", 1)
    op_cfg.sources = node.get("sources", [])

    skip = node.get("skip", [])
    if isinstance(skip, str):
        op_cfg.skip = [skip] if skip else []
    elif isinstance(skip, list):
        op_cfg.skip = [str(s) for s in skip]
    else:
        op_cfg.skip = []

    meta = node.get("$metadata", {})
    op_cfg.metadata.common_input = meta.get("common_input", [])
    op_cfg.metadata.common_output = meta.get("common_output", [])
    op_cfg.metadata.item_input = meta.get("item_input", [])
    op_cfg.metadata.item_output = meta.get("item_output", [])

    op_cfg.common_defaults = node.get("common_defaults", {})
    op_cfg.item_defaults = node.get("item_defaults", {})
    op_cfg.strict_common = node.get("strict_common", [])
    op_cfg.strict_item = node.get("strict_item", [])

    op_cfg.raw_params = {}
    for key, value in node.items():
        if key not in RESERVED_KEYS:
            op_cfg.raw_params[key] = value

    return op_cfg


def _validate(cfg: Config):
    if not cfg.pipeline_config.operators:
        raise ConfigError("pipeline_config.operators is empty")
    if not cfg.pipeline_group:
        raise ConfigError("pipeline_group is empty")

    for name, op_cfg in cfg.pipeline_config.operators.items():
        if not op_cfg.type_name:
            raise ConfigError(f'operator "{name}": missing type_name')

    for name, op_cfg in cfg.pipeline_config.operators.items():
        for src in op_cfg.sources:
            if src not in cfg.pipeline_config.operators:
                raise ConfigError(
                    f'operator "{name}": sources references undefined operator "{src}"'
                )

    for name, op_cfg in cfg.pipeline_config.operators.items():
        for skip_field in op_cfg.skip:
            if not skip_field.startswith("_"):
                raise ConfigError(
                    f'operator "{name}": skip field "{skip_field}" must start with '
                    "'_' (control fields are engine-internal)"
                )
            if skip_field not in op_cfg.metadata.common_input:
                raise ConfigError(
                    f'operator "{name}": skip field "{skip_field}" must also appear '
                    "in $metadata.common_input to ensure correct DAG ordering"
                )
