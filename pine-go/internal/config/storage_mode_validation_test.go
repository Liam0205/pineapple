package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// minimalConfig is the smallest config that passes validate(), so these tests
// exercise the storage_mode checks rather than tripping over something else.
func minimalConfig(storageModeJSON string) []byte {
	var sm string
	if storageModeJSON != "" {
		sm = `"storage_mode": ` + storageModeJSON + `,`
	}
	return []byte(`{` + sm + `
		"pipeline_config": {
			"operators": {"op": {"type_name": "transform_copy", "direction": "common_to_item",
				"$metadata": {"common_input": ["v"], "common_output": [],
					"item_input": [], "item_output": ["v"]}}},
			"pipeline_map": {"s": {"pipeline": ["op"]}}
		},
		"pipeline_group": {"main": {"pipeline": ["s"]}},
		"flow_contract": {"common_input": ["v"], "item_input": ["id"],
			"common_output": [], "item_output": ["id", "v"]}
	}`)
}

// TestStorageModeValueWhitelist pins the fail-fast added in issue #187.
//
// Before it, an invalid storage_mode was silently accepted and fell back to row
// storage in all three runtimes (that fallback direction was itself aligned in
// #179). Silent fallback means a typo produces a working engine whose memory and
// performance profile is the opposite of what the author asked for, so the value
// is now rejected at config load.
//
// Rejecting here rather than in NewFrame is deliberate: NewFrame's
// `default: newRowFrame` branch is what makes an unrecognised value become row
// storage, and all three runtimes mirror that branch. Validating at load keeps
// the dispatch rule identical while making the invalid value unreachable.
func TestStorageModeValueWhitelist(t *testing.T) {
	accepted := []string{`"row"`, `"column"`, `""`, `null`}
	for _, sm := range accepted {
		t.Run("accepted/"+sm, func(t *testing.T) {
			if _, err := Load(minimalConfig(sm)); err != nil {
				t.Fatalf("storage_mode %s must be accepted, got %v", sm, err)
			}
		})
	}

	// Absent key: the zero value "" reaches validate and must be accepted.
	t.Run("accepted/absent", func(t *testing.T) {
		if _, err := Load(minimalConfig("")); err != nil {
			t.Fatalf("absent storage_mode must be accepted, got %v", err)
		}
	})

	// Every invalid string, including the two that diverged across runtimes
	// before #179: "colunm" (a typo) and "Column" (wrong case).
	rejected := []string{`"colunm"`, `"Column"`, `"COLUMN"`, `"col"`,
		`"columns"`, `"column "`, `" column"`, `"unknown"`}
	for _, sm := range rejected {
		t.Run("rejected/"+sm, func(t *testing.T) {
			_, err := Load(minimalConfig(sm))
			if err == nil {
				t.Fatalf("storage_mode %s must be rejected", sm)
			}
			// The message is byte-identical in all three runtimes; error
			// fixtures match on a substring of it.
			if !strings.Contains(err.Error(), "is invalid, must be") {
				t.Fatalf("storage_mode %s: unexpected error text %q", sm, err)
			}
		})
	}
}

// TestRootStringFieldsRejectNonStrings pins the type-layer half of #187.
//
// This is a property of encoding/json rather than of any one field: every
// RootConfig string field is declared `string`, so a number, boolean, array or
// object fails the whole unmarshal. JSON null is a no-op and leaves the zero
// value, which is why null is accepted here and in the other two runtimes.
//
// It is asserted because the other two runtimes had to be changed to match, and
// they were each wrong in a different way: pine-java coerced everything through
// asText() (123 became "123", a container became ""), and pine-cpp was
// inconsistent with itself — only storage_mode threw, while log_prefix and the
// two _PINEAPPLE_* fields silently ignored a wrong type. Nothing pinned Go's
// behaviour, so nothing would have noticed it changing either.
func TestRootStringFieldsRejectNonStrings(t *testing.T) {
	fields := []string{"storage_mode", "log_prefix", "_PINEAPPLE_VERSION", "_PINEAPPLE_CREATE_TIME"}
	nonStrings := []string{`123`, `1.5`, `true`, `false`, `[1,2]`, `{"a":1}`}

	for _, field := range fields {
		for _, val := range nonStrings {
			t.Run(field+"="+val, func(t *testing.T) {
				var raw map[string]json.RawMessage
				if err := json.Unmarshal(minimalConfig(""), &raw); err != nil {
					t.Fatalf("fixture is not valid JSON: %v", err)
				}
				raw[field] = json.RawMessage(val)
				body, err := json.Marshal(raw)
				if err != nil {
					t.Fatalf("re-marshal failed: %v", err)
				}
				if _, err := Load(body); err == nil {
					t.Fatalf("%s = %s must be rejected", field, val)
				}
			})
		}

		// null leaves the default rather than erroring.
		t.Run(field+"=null", func(t *testing.T) {
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(minimalConfig(""), &raw); err != nil {
				t.Fatalf("fixture is not valid JSON: %v", err)
			}
			raw[field] = json.RawMessage(`null`)
			body, err := json.Marshal(raw)
			if err != nil {
				t.Fatalf("re-marshal failed: %v", err)
			}
			if _, err := Load(body); err != nil {
				t.Fatalf("%s = null must be accepted, got %v", field, err)
			}
		})
	}
}
