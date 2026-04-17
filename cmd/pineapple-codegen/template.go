package main

import (
	"fmt"
	"text/template"

	"github.com/Liam0205/pineapple/internal/types"
)

// pythonType maps ParamSpec.Type to a Python type hint.
func pythonType(goType string) string {
	switch goType {
	case "string":
		return "str"
	case "int64":
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
	case "int64":
		return "0"
	case "float64":
		return "0.0"
	case "bool":
		return "False"
	default:
		return "None"
	}
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

var funcMap = template.FuncMap{
	"pythonType":    pythonType,
	"pythonDefault": pythonDefault,
	"pythonLiteral": pythonLiteral,
	"camelCase":     toCamelCase,
	"sortedParams":  sortedParams,
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
        "{{$k}}": {"type": "{{$v.Type}}", "required": {{if $v.Required}}True{{else}}False{{end}}{{if $v.Default}}, "default": {{pythonLiteral $v.Default}}{{end}}},
    {{- end}}{{end}}
    }

    def __call__(
        self,
        *,{{range $k := sortedParams $schema.Params}}{{with $v := index $schema.Params $k}}
        {{$k}}: {{pythonType $v.Type}} = {{if $v.Required}}...{{else}}{{pythonDefault $v.Type}}{{end}},{{end}}{{end}}
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        debug: bool = False,
    ) -> "{{camelCase $schema.Name}}Op":
        return self._apply(
            params={
            {{- range $k := sortedParams $schema.Params}}
                "{{$k}}": {{$k}},
            {{- end}}
            },
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            debug=debug,
        )
{{end}}`

const initTemplate = `# auto-generated from pine operator schema — DO NOT EDIT
{{range .}}from apple.generated.operators import {{camelCase .Name}}Op
{{end}}
__all__ = [{{range .}}"{{camelCase .Name}}Op", {{end}}]
`

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
