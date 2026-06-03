package codegen

import (
	"fmt"
	"text/template"

	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

// pythonType maps ParamSpec.Type to a Python type hint.
func pythonType(goType string) string {
	switch goType {
	case "string":
		return "str"
	case "int", "int64":
		return "int"
	case "float64":
		return "float"
	case "bool":
		return "bool"
	default:
		return "Any"
	}
}

// pythonDefault maps ParamSpec.Type to a Python default value string.
func pythonDefault(goType string) string {
	switch goType {
	case "string":
		return `""`
	case "int", "int64":
		return "0"
	case "float64":
		return "0.0"
	case "bool":
		return "False"
	default:
		return "None"
	}
}

func pythonParamDefault(spec types.ParamSpec) string {
	if spec.Default != nil {
		return pythonLiteral(spec.Default)
	}
	return pythonDefault(spec.Type)
}

func hasDefault(v any) bool {
	return v != nil
}

// pythonLiteral converts a Go value to a Python literal string.
func pythonLiteral(v any) string {
	if v == nil {
		return "None"
	}
	switch x := v.(type) {
	case string:
		return fmt.Sprintf("%q", x)
	case bool:
		if x {
			return "True"
		}
		return "False"
	case float64:
		return fmt.Sprintf("%g", x)
	case int64:
		return fmt.Sprintf("%d", x)
	case int:
		return fmt.Sprintf("%d", x)
	default:
		return fmt.Sprintf("%q", fmt.Sprint(v))
	}
}

// sortedParams returns param names in sorted order for deterministic output.
func sortedParams(params map[string]types.ParamSpec) []string {
	names := make([]string, 0, len(params))
	for k := range params {
		names = append(names, k)
	}
	sortStringsSlice(names)
	return names
}

// alwaysParams returns sorted names of params that are always included in
// the params dict: required params, or optional params that have a Default.
func alwaysParams(params map[string]types.ParamSpec) []string {
	var names []string
	for k, spec := range params {
		if spec.Required || spec.Default != nil {
			names = append(names, k)
		}
	}
	sortStringsSlice(names)
	return names
}

// conditionalParams returns sorted names of optional params with no Default.
// These are only included in the params dict when the caller passes a non-None value.
func conditionalParams(params map[string]types.ParamSpec) []string {
	var names []string
	for k, spec := range params {
		if !spec.Required && spec.Default == nil {
			names = append(names, k)
		}
	}
	sortStringsSlice(names)
	return names
}

func sortStringsSlice(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// toCamelCase converts snake_case to CamelCase.
func toCamelCase(s string) string {
	result := make([]byte, 0, len(s))
	upper := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' {
			upper = true
			continue
		}
		if upper && c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
			upper = false
		} else {
			upper = false
		}
		result = append(result, c)
	}
	return string(result)
}

// isRecall returns true if the operator type is Recall.
func isRecall(t types.OperatorType) bool {
	return t == types.OpTypeRecall
}

var funcMap = template.FuncMap{
	"pythonType":         pythonType,
	"pythonDefault":      pythonDefault,
	"pythonParamDefault": pythonParamDefault,
	"pythonLiteral":      pythonLiteral,
	"hasDefault":         hasDefault,
	"camelCase":          toCamelCase,
	"sortedParams":       sortedParams,
	"alwaysParams":       alwaysParams,
	"conditionalParams":  conditionalParams,
	"isRecall":           isRecall,
}

const operatorClassTemplate = `# auto-generated from pine operator schema — DO NOT EDIT
from __future__ import annotations
from typing import Any
from apple.base import BaseOp

{{range $schema := .}}
class {{camelCase $schema.Name}}Op(BaseOp):
    """Operator: {{$schema.Name}}"""
    _name = "{{$schema.Name}}"
    _params_schema = { {{- range $k := sortedParams $schema.Params}}{{with $v := index $schema.Params $k}}
        "{{$k}}": {"type": "{{$v.Type}}", "required": {{if $v.Required}}True{{else}}False{{end}}{{if hasDefault $v.Default}}, "default": {{pythonLiteral $v.Default}}{{end}}{{if $v.Templatable}}, "templatable": True{{end}}},
    {{- end}}{{end}}
    }

    def __call__(
        self,
        *,{{range $k := sortedParams $schema.Params}}{{with $v := index $schema.Params $k}}
        {{$k}}: {{pythonType $v.Type}} = {{if $v.Required}}...{{else}}{{pythonParamDefault $v}}{{end}},{{end}}{{end}}
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        consumes_row_set: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "{{camelCase $schema.Name}}Op":
        _params = {
        {{- range $k := alwaysParams $schema.Params}}
            "{{$k}}": {{$k}},
        {{- end}}
        }
        {{- range $k := conditionalParams $schema.Params}}
        if {{$k}} is not None:
            _params["{{$k}}"] = {{$k}}
        {{- end}}
        return self._apply(
            params=_params,
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,{{if isRecall $schema.Type}}
            recall=True,{{end}}
            consumes_row_set=consumes_row_set,
            debug=debug,
            name=name or "",
        )
{{end}}`

const initTemplate = `# auto-generated from pine operator schema — DO NOT EDIT
{{range .}}from .operators import {{camelCase .Name}}Op
{{end}}
__all__ = [{{range .}}"{{camelCase .Name}}Op", {{end}}]
`

// markersTemplate renders apple_generated/markers.py — a static lookup table
// keyed by operator type_name, exposing the row-set marker bools (probed
// from the Go factory at codegen time). Apple OpCall consults this table to
// auto-populate additive_writes_row_set / consumes_row_set / mutates_row_set,
// keeping the Go marker contract as the single source of truth.
const markersTemplate = `# auto-generated from pine operator schema — DO NOT EDIT
"""Row-set marker bools per operator, probed from Go factories at codegen time.

The Go side declares row-set semantics via marker interfaces
(AdditiveWritesRowSet, ConsumesRowSet, MutatesRowSet). This file mirrors
those flags so Apple OpCall and the validator can judge row-set behavior
directly instead of inferring from operator name prefix.
"""
from __future__ import annotations

OPERATOR_MARKERS: dict[str, dict[str, bool]] = {
{{- range .}}
    "{{.Name}}": {
        "additive_writes_row_set": {{if .AdditiveWritesRowSet}}True{{else}}False{{end}},
        "consumes_row_set": {{if .ConsumesRowSet}}True{{else}}False{{end}},
        "mutates_row_set": {{if .MutatesRowSet}}True{{else}}False{{end}},
    },
{{- end}}
}


def get_markers(type_name: str) -> dict[str, bool]:
    """Return the marker dict for type_name, or all-False defaults if unknown.

    Unknown operators (e.g., custom ops registered after codegen) are
    treated as having no row-set semantics; the Go side remains authoritative.
    """
    return OPERATOR_MARKERS.get(type_name, {
        "additive_writes_row_set": False,
        "consumes_row_set": False,
        "mutates_row_set": False,
    })
`

// --- Markdown documentation templates ---

// DocData combines registry schema and parsed doc comments for template rendering.
type DocData struct {
	Name        string
	Type        string
	Description string
	Params      []DocParam
	Metadata    MetadataDoc
}

// DocParam holds a single parameter's documentation data for template rendering.
// All fields come from OperatorSchema.ParamSpec.
type DocParam struct {
	Name        string
	Type        string
	Required    bool
	Default     string
	Description string
}

const operatorDocTemplate = `# {{.Name}}

**Type**: {{.Type}}

{{.Description}}

## Parameters

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
{{- range .Params}}
| {{.Name}} | {{.Type}} | {{if .Required}}Yes{{else}}No{{end}} | {{if .Default}}{{$.BacktickWrap .Default}}{{else}}-{{end}} | {{.Description}} |
{{- end}}

## Metadata Contract

| Field | Typical Usage |
|-------|---------------|
| CommonInput | {{$.BacktickWrap .Metadata.CommonInput}} |
| CommonOutput | {{$.BacktickWrap .Metadata.CommonOutput}} |
| ItemInput | {{$.BacktickWrap .Metadata.ItemInput}} |
| ItemOutput | {{$.BacktickWrap .Metadata.ItemOutput}} |

## DSL Usage

` + "```python" + `
flow.{{.Name}}(
{{- range .Params}}
    {{.Name}}=...,
{{- end}}
    common_input=[...],
    item_input=[...],
    item_output=[...],
)
` + "```" + `
`

// BacktickWrap wraps a string in backticks for markdown. Called as a method on DocData.
func (d DocData) BacktickWrap(s string) string {
	if s == "" {
		return "-"
	}
	return "`" + s + "`"
}

const operatorIndexTemplate = `# Operator Reference

> Auto-generated from Go operator source code. Do not edit manually.

{{range .Categories}}
## {{.Name}}

| Operator | Description |
|----------|-------------|
{{- range .Ops}}
| [{{.Name}}]({{.Name}}.md) | {{.Description}} |
{{- end}}
{{end}}
`

// --- Resource class template ---

const resourceClassTemplate = `# auto-generated from pine resource schema — DO NOT EDIT
from __future__ import annotations
from typing import Any
from apple.resource import BaseResource

{{range $schema := .}}
class {{camelCase $schema.Name}}Resource(BaseResource):
    """Resource: {{$schema.Name}}{{if $schema.Description}} — {{$schema.Description}}{{end}}"""
    _name = "{{$schema.Name}}"
    _default_interval = {{$schema.DefaultInterval}}
    _params_schema = { {{- range $k := sortedParams $schema.Params}}{{with $v := index $schema.Params $k}}
        "{{$k}}": {"type": "{{$v.Type}}", "required": {{if $v.Required}}True{{else}}False{{end}}{{if hasDefault $v.Default}}, "default": {{pythonLiteral $v.Default}}{{end}}},
    {{- end}}{{end}}
    }

    def __init__(
        self,
        *,{{range $k := sortedParams $schema.Params}}{{with $v := index $schema.Params $k}}
        {{$k}}: {{pythonType $v.Type}} = {{if $v.Required}}...{{else}}{{pythonParamDefault $v}}{{end}},{{end}}{{end}}
        interval: int = {{$schema.DefaultInterval}},
    ):
        super().__init__(
            interval=interval,
        {{- range $k := sortedParams $schema.Params}}
            {{$k}}={{$k}},
        {{- end}}
        )
{{end}}`

const resourceInitTemplate = `# auto-generated from pine resource schema — DO NOT EDIT
{{range .}}from .resources import {{camelCase .Name}}Resource
{{end}}
__all__ = [{{range .}}"{{camelCase .Name}}Resource", {{end}}]
`

type indexCategory struct {
	Name string
	Ops  []indexOp
}

type indexOp struct {
	Name        string
	Description string
}

type indexData struct {
	Categories []indexCategory
}

func parseDocTemplates() (*template.Template, *template.Template, error) {
	docTmpl, err := template.New("doc").Parse(operatorDocTemplate)
	if err != nil {
		return nil, nil, err
	}
	idxTmpl, err := template.New("index").Parse(operatorIndexTemplate)
	if err != nil {
		return nil, nil, err
	}
	return docTmpl, idxTmpl, nil
}

func parseTemplates() (*template.Template, *template.Template, error) {
	opTmpl, err := template.New("operators").Funcs(funcMap).Parse(operatorClassTemplate)
	if err != nil {
		return nil, nil, err
	}
	initTmpl, err := template.New("init").Funcs(funcMap).Parse(initTemplate)
	if err != nil {
		return nil, nil, err
	}
	return opTmpl, initTmpl, nil
}

func parseMarkersTemplate() (*template.Template, error) {
	return template.New("markers").Funcs(funcMap).Parse(markersTemplate)
}

func parseResourceTemplates() (*template.Template, *template.Template, error) {
	resTmpl, err := template.New("resources").Funcs(funcMap).Parse(resourceClassTemplate)
	if err != nil {
		return nil, nil, err
	}
	initTmpl, err := template.New("resinit").Funcs(funcMap).Parse(resourceInitTemplate)
	if err != nil {
		return nil, nil, err
	}
	return resTmpl, initTmpl, nil
}
