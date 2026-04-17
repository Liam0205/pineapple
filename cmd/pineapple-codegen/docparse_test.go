package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseDocComment(t *testing.T) {
	text := `Operator: filter_condition
Category: Filter
Description: Removes items where a specified field equals a given value.

Params:
  - field (string, required): Item field to check.
  - value (any, required): Items where field == value are removed.

Metadata contract (typical usage):
  CommonInput:  []
  CommonOutput: []
  ItemInput:    [<field>]
  ItemOutput:   []
`
	doc, ok, err := parseDocComment(text)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if doc.Name != "filter_condition" {
		t.Errorf("Name = %q", doc.Name)
	}
	if doc.Category != "Filter" {
		t.Errorf("Category = %q", doc.Category)
	}
	if doc.Description != "Removes items where a specified field equals a given value." {
		t.Errorf("Description = %q", doc.Description)
	}
	if len(doc.ParamDocs) != 2 {
		t.Fatalf("ParamDocs count = %d, want 2", len(doc.ParamDocs))
	}

	p0 := doc.ParamDocs[0]
	if p0.Name != "field" || p0.Type != "string" || !p0.Required {
		t.Errorf("param 0 = %+v", p0)
	}
	if p0.Description != "Item field to check." {
		t.Errorf("param 0 desc = %q", p0.Description)
	}

	p1 := doc.ParamDocs[1]
	if p1.Name != "value" || p1.Type != "any" || !p1.Required {
		t.Errorf("param 1 = %+v", p1)
	}

	if doc.Metadata.CommonInput != "[]" {
		t.Errorf("CommonInput = %q", doc.Metadata.CommonInput)
	}
	if doc.Metadata.ItemInput != "[<field>]" {
		t.Errorf("ItemInput = %q", doc.Metadata.ItemInput)
	}
}

func TestParseDocCommentOptionalWithDefault(t *testing.T) {
	text := `Operator: feature_normalize
Category: Feature
Description: Normalizes a numeric item field using min-max scaling to [0, 1].

Params:
  - field (string, required): Item field to normalize.
  - output_field (string, optional, default=<field>+"_norm"): Target field for normalized values.
  - method (string, optional, default="min_max"): Normalization method.

Metadata contract (typical usage):
  CommonInput:  []
  CommonOutput: []
  ItemInput:    [<field>]
  ItemOutput:   [<output_field>]
`
	doc, ok, err := parseDocComment(text)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok")
	}
	if len(doc.ParamDocs) != 3 {
		t.Fatalf("ParamDocs count = %d, want 3", len(doc.ParamDocs))
	}

	p1 := doc.ParamDocs[1]
	if p1.Name != "output_field" {
		t.Errorf("name = %q", p1.Name)
	}
	if p1.Required {
		t.Error("should be optional")
	}
	if p1.Default != `<field>+"_norm"` {
		t.Errorf("default = %q", p1.Default)
	}

	p2 := doc.ParamDocs[2]
	if p2.Default != `"min_max"` {
		t.Errorf("method default = %q", p2.Default)
	}
}

func TestParseDocCommentMultiLineDescription(t *testing.T) {
	text := `Operator: lua
Category: Feature / Control
Description: Executes a Lua script for per-item or per-common computation.

Exactly one of function_for_item or function_for_common must be provided.

Params:
  - lua_script (string, required): Lua source code defining the function to call.
  - function_for_item (string, optional): Function name to call per item.
  - function_for_common (string, optional): Function name to call once for all items.

Metadata contract (typical usage):
  CommonInput:  [<common fields read as scalar globals>]
  CommonOutput: [<return values from function_for_common>]
  ItemInput:    [<item fields — scalars in item mode, lists in common mode>]
  ItemOutput:   [<return values from function_for_item>]
`
	doc, ok, err := parseDocComment(text)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok")
	}
	if doc.Category != "Feature / Control" {
		t.Errorf("Category = %q", doc.Category)
	}
	if doc.Description != "Executes a Lua script for per-item or per-common computation." {
		t.Errorf("Description = %q", doc.Description)
	}
	if len(doc.ParamDocs) != 3 {
		t.Fatalf("ParamDocs count = %d", len(doc.ParamDocs))
	}
}

func TestParseDocCommentNoOperator(t *testing.T) {
	text := `Package operators aggregates all built-in operator packages.
`
	_, ok, err := parseDocComment(text)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected ok=false for non-operator doc")
	}
}

func TestParseOperatorDocs(t *testing.T) {
	// Create a temp directory with a mock operator file
	dir := t.TempDir()
	subDir := filepath.Join(dir, "myop")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := `// Operator: test_op
// Category: Test
// Description: A test operator.
//
// Params:
//   - name (string, required): The name.
//
// Metadata contract (typical usage):
//   CommonInput:  []
//   CommonOutput: []
//   ItemInput:    []
//   ItemOutput:   []
package myop
`
	if err := os.WriteFile(filepath.Join(subDir, "op.go"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// Also write a test file that should be skipped
	if err := os.WriteFile(filepath.Join(subDir, "op_test.go"), []byte("package myop\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	docs, err := ParseOperatorDocs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("docs count = %d, want 1", len(docs))
	}
	if docs[0].Name != "test_op" {
		t.Errorf("Name = %q", docs[0].Name)
	}
	if docs[0].Category != "Test" {
		t.Errorf("Category = %q", docs[0].Category)
	}
}

func TestParseRealOperators(t *testing.T) {
	// Parse the actual operators/ directory in the project
	docs, err := ParseOperatorDocs("../../operators")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) < 8 {
		t.Errorf("expected at least 8 operators, got %d", len(docs))
	}

	// Build name map
	m := opDocByName(docs)

	// Spot-check a few operators
	if _, ok := m["filter_condition"]; !ok {
		t.Error("missing filter_condition")
	}
	if _, ok := m["lua"]; !ok {
		t.Error("missing lua")
	}
	if _, ok := m["observe_log"]; !ok {
		t.Error("missing observe_log")
	}

	// Check that all docs have required fields
	for _, d := range docs {
		if d.Name == "" {
			t.Error("empty name")
		}
		if d.Category == "" {
			t.Errorf("%s: empty category", d.Name)
		}
		if d.Description == "" {
			t.Errorf("%s: empty description", d.Name)
		}
	}
}

func TestOpDocByName(t *testing.T) {
	docs := []OpDoc{
		{Name: "a"},
		{Name: "b"},
	}
	m := opDocByName(docs)
	if len(m) != 2 {
		t.Errorf("len = %d", len(m))
	}
	if _, ok := m["a"]; !ok {
		t.Error("missing a")
	}
}
