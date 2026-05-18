package codegen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseDocCommentMetadata(t *testing.T) {
	text := `Operator: filter_condition
Type: Filter
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
	if doc.Metadata.CommonInput != "[]" {
		t.Errorf("CommonInput = %q", doc.Metadata.CommonInput)
	}
	if doc.Metadata.CommonOutput != "[]" {
		t.Errorf("CommonOutput = %q", doc.Metadata.CommonOutput)
	}
	if doc.Metadata.ItemInput != "[<field>]" {
		t.Errorf("ItemInput = %q", doc.Metadata.ItemInput)
	}
	if doc.Metadata.ItemOutput != "[]" {
		t.Errorf("ItemOutput = %q", doc.Metadata.ItemOutput)
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

func TestParseDocCommentNoMetadata(t *testing.T) {
	text := `Operator: simple_op
Type: Transform
Description: A simple operator with no metadata section.
`
	doc, ok, err := parseDocComment(text)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if doc.Name != "simple_op" {
		t.Errorf("Name = %q", doc.Name)
	}
	// Metadata should be zero-valued
	if doc.Metadata.CommonInput != "" {
		t.Errorf("CommonInput should be empty, got %q", doc.Metadata.CommonInput)
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
// Type: Transform
// Description: A test operator.
//
// Params:
//   - name (string, required): The name.
//
// Metadata contract (typical usage):
//   CommonInput:  []
//   CommonOutput: []
//   ItemInput:    [<name>]
//   ItemOutput:   [result]
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
	if docs[0].Metadata.ItemInput != "[<name>]" {
		t.Errorf("ItemInput = %q", docs[0].Metadata.ItemInput)
	}
	if docs[0].Metadata.ItemOutput != "[result]" {
		t.Errorf("ItemOutput = %q", docs[0].Metadata.ItemOutput)
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

	// Spot-check metadata is parsed
	if fc, ok := m["filter_condition"]; ok {
		if fc.Metadata.ItemInput != "[<field>]" {
			t.Errorf("filter_condition ItemInput = %q", fc.Metadata.ItemInput)
		}
	} else {
		t.Error("missing filter_condition")
	}

	if _, ok := m["transform_by_lua"]; !ok {
		t.Error("missing transform_by_lua")
	}
	if _, ok := m["observe_log"]; !ok {
		t.Error("missing observe_log")
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
