package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Liam0205/pineapple/internal/registry"
	"github.com/Liam0205/pineapple/internal/types"

	// Trigger operator registrations for integration test
	_ "github.com/Liam0205/pineapple/operators"
)

func TestToCamelCase(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"filter_condition", "FilterCondition"},
		{"recall_static", "RecallStatic"},
		{"lua", "Lua"},
		{"feature_normalize", "FeatureNormalize"},
		{"a_b_c", "ABC"},
		{"", ""},
	}
	for _, tt := range tests {
		got := toCamelCase(tt.in)
		if got != tt.want {
			t.Errorf("toCamelCase(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPythonType(t *testing.T) {
	tests := map[string]string{
		"string":  "str",
		"int64":   "int",
		"float64": "float",
		"bool":    "bool",
		"any":     "Any",
		"unknown": "Any",
	}
	for in, want := range tests {
		got := pythonType(in)
		if got != want {
			t.Errorf("pythonType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPythonDefault(t *testing.T) {
	tests := map[string]string{
		"string":  `""`,
		"int64":   "0",
		"float64": "0.0",
		"bool":    "False",
		"any":     "None",
	}
	for in, want := range tests {
		got := pythonDefault(in)
		if got != want {
			t.Errorf("pythonDefault(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPythonLiteral(t *testing.T) {
	tests := []struct {
		in   any
		want string
	}{
		{nil, "None"},
		{"hello", `"hello"`},
		{true, "True"},
		{false, "False"},
		{3.14, "3.14"},
		{int64(42), "42"},
		{0.0, "0"},
	}
	for _, tt := range tests {
		got := pythonLiteral(tt.in)
		if got != tt.want {
			t.Errorf("pythonLiteral(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSortedParams(t *testing.T) {
	params := map[string]types.ParamSpec{
		"zebra": {Type: "string"},
		"alpha": {Type: "int64"},
		"mid":   {Type: "bool"},
	}
	got := sortedParams(params)
	want := []string{"alpha", "mid", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("len mismatch: %v vs %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sortedParams[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTemplateRendering(t *testing.T) {
	schemas := []types.OperatorSchema{
		{
			Name: "test_op",
			Params: map[string]types.ParamSpec{
				"name":  {Type: "string", Required: true},
				"count": {Type: "int64", Required: false, Default: int64(10)},
			},
		},
	}

	opTmpl, initTmpl, err := parseTemplates()
	if err != nil {
		t.Fatal(err)
	}

	var opBuf bytes.Buffer
	if err := opTmpl.Execute(&opBuf, schemas); err != nil {
		t.Fatal(err)
	}
	opOut := opBuf.String()

	// Check key elements
	checks := []string{
		`class TestOpOp(BaseOp):`,
		`_name = "test_op"`,
		`"name": {"type": "string", "required": True}`,
		`"count": {"type": "int64", "required": False, "default": 10}`,
		`name: str = ...`,
		`count: int = 0`,
		`-> "TestOpOp"`,
	}
	for _, check := range checks {
		if !bytes.Contains([]byte(opOut), []byte(check)) {
			t.Errorf("operators.py missing: %s\n\nGot:\n%s", check, opOut)
		}
	}

	var initBuf bytes.Buffer
	if err := initTmpl.Execute(&initBuf, schemas); err != nil {
		t.Fatal(err)
	}
	initOut := initBuf.String()
	if !bytes.Contains([]byte(initOut), []byte("TestOpOp")) {
		t.Errorf("__init__.py missing TestOpOp")
	}
}

func TestRegistryAllIntegration(t *testing.T) {
	// With all operators imported, All() should return non-empty sorted list
	schemas := registry.All()
	if len(schemas) == 0 {
		t.Fatal("expected registered operators")
	}
	// Check sorted order
	for i := 1; i < len(schemas); i++ {
		if schemas[i].Name < schemas[i-1].Name {
			t.Errorf("not sorted: %q < %q", schemas[i].Name, schemas[i-1].Name)
		}
	}
}

func TestRunIntegration(t *testing.T) {
	dir := t.TempDir()
	if err := run(dir, "", ""); err != nil {
		t.Fatal(err)
	}

	// Check operators.py exists and is non-empty
	opData, err := os.ReadFile(filepath.Join(dir, "operators.py"))
	if err != nil {
		t.Fatal(err)
	}
	if len(opData) == 0 {
		t.Error("operators.py is empty")
	}
	if !bytes.Contains(opData, []byte("class")) {
		t.Error("operators.py has no class definitions")
	}

	// Check __init__.py exists
	initData, err := os.ReadFile(filepath.Join(dir, "__init__.py"))
	if err != nil {
		t.Fatal(err)
	}
	if len(initData) == 0 {
		t.Error("__init__.py is empty")
	}
}

func TestRunWithDocGeneration(t *testing.T) {
	pyDir := t.TempDir()
	docDir := t.TempDir()

	if err := run(pyDir, docDir, "../../operators"); err != nil {
		t.Fatal(err)
	}

	// Check that per-operator docs were generated
	entries, err := os.ReadDir(docDir)
	if err != nil {
		t.Fatal(err)
	}

	mdCount := 0
	hasReadme := false
	for _, e := range entries {
		if e.Name() == "README.md" {
			hasReadme = true
		}
		if filepath.Ext(e.Name()) == ".md" {
			mdCount++
		}
	}

	if !hasReadme {
		t.Error("missing README.md index")
	}
	// At least 9 operators + 1 README = 10
	if mdCount < 10 {
		t.Errorf("expected at least 10 .md files, got %d", mdCount)
	}

	// Spot-check content of one doc
	data, err := os.ReadFile(filepath.Join(docDir, "filter_condition.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	checks := []string{
		"# filter_condition",
		"**Category**: Filter",
		"## Parameters",
		"## Metadata Contract",
		"## DSL Usage",
		"flow.filter_condition(",
	}
	for _, check := range checks {
		if !bytes.Contains([]byte(content), []byte(check)) {
			t.Errorf("filter_condition.md missing: %s", check)
		}
	}

	// Check README has category grouping
	readme, err := os.ReadFile(filepath.Join(docDir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readmeContent := string(readme)
	if !bytes.Contains([]byte(readmeContent), []byte("## Filter")) {
		t.Error("README missing Filter category")
	}
	if !bytes.Contains([]byte(readmeContent), []byte("[filter_condition]")) {
		t.Error("README missing link to filter_condition")
	}
}
