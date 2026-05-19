package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
	_ "github.com/Liam0205/pineapple/pine-go/operators"
	"github.com/Liam0205/pineapple/pine-go/pkg/resource"
)

// floatsEqual compares two float64 values using relative epsilon comparison.
// Formula: |a - b| <= ε * max(|a|, |b|, 1.0) where ε = 2^-52.
func floatsEqual(a, b float64) bool {
	eps := math.Pow(2, -52)
	diff := math.Abs(a - b)
	scale := math.Max(math.Max(math.Abs(a), math.Abs(b)), 1.0)
	return diff <= eps*scale
}

// valuesEqual compares two values decoded from JSON, using relative epsilon
// for float comparisons and string-based equality for everything else.
func valuesEqual(got, expected any) bool {
	// Both are numeric (json.Number or float64 from JSON decoding)
	gf, gIsFloat := toFloat64(got)
	ef, eIsFloat := toFloat64(expected)
	if gIsFloat && eIsFloat {
		return floatsEqual(gf, ef)
	}
	return fmt.Sprintf("%v", got) == fmt.Sprintf("%v", expected)
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

type pipelineFixture struct {
	Name            string                    `json:"name"`
	Requires        []string                  `json:"requires"`
	Config          json.RawMessage           `json:"config"`
	StaticResources map[string]any            `json:"static_resources"`
	Cases           []pipelineCase            `json:"cases"`
}

type pipelineCase struct {
	Name        string          `json:"name"`
	Request     pipelineRequest `json:"request"`
	Expected    pipelineResult  `json:"expected"`
	ExpectError string          `json:"expect_error"`
}

type pipelineRequest struct {
	Common map[string]any   `json:"common"`
	Items  []map[string]any `json:"items"`
}

type pipelineResult struct {
	Common map[string]any   `json:"common"`
	Items  []map[string]any `json:"items"`
}

func TestPipelineFixtures(t *testing.T) {
	files, err := filepath.Glob("../../fixtures/pipelines/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("no pipeline fixture files found")
	}

	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var pf pipelineFixture
			if err := json.Unmarshal(data, &pf); err != nil {
				t.Fatal(err)
			}

			if len(pf.Requires) > 0 {
				t.Skipf("skipping: requires %v", pf.Requires)
			}

			for _, tc := range pf.Cases {
				t.Run(tc.Name, func(t *testing.T) {
					engine, err := pine.NewEngine(pf.Config)
					if err != nil {
						t.Fatalf("NewEngine: %v", err)
					}

					req := &pine.Request{
						Common: tc.Request.Common,
						Items:  tc.Request.Items,
					}

					ctx := context.Background()
					if len(pf.StaticResources) > 0 {
						ctx = resource.WithResources(ctx, resource.NewStatic(pf.StaticResources))
					}

					result, err := engine.Execute(ctx, req)

					// Handle expect_error cases
					if tc.ExpectError != "" {
						if err == nil {
							t.Fatalf("expected error containing %q, got nil", tc.ExpectError)
						}
						if !strings.Contains(err.Error(), tc.ExpectError) {
							t.Fatalf("expected error containing %q, got: %v", tc.ExpectError, err)
						}
						return
					}

					if err != nil {
						t.Fatalf("Execute: %v", err)
					}

					// Compare common
					if tc.Expected.Common != nil {
						for key, expectedVal := range tc.Expected.Common {
							got, ok := result.Common[key]
							if !ok {
								t.Errorf("common missing key %q", key)
								continue
							}
							if !valuesEqual(got, expectedVal) {
								t.Errorf("common[%q] = %v, expected %v", key, got, expectedVal)
							}
						}
					}

					// Compare items
					if len(tc.Expected.Items) != len(result.Items) {
						t.Fatalf("items: got %d, expected %d", len(result.Items), len(tc.Expected.Items))
					}
					for i, expectedItem := range tc.Expected.Items {
						for key, expectedVal := range expectedItem {
							got, ok := result.Items[i][key]
							if !ok {
								t.Errorf("items[%d] missing key %q", i, key)
								continue
							}
							if !valuesEqual(got, expectedVal) {
								t.Errorf("items[%d].%s = %v, expected %v", i, key, got, expectedVal)
							}
						}
					}
				})
			}
		})
	}
}
