package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
	_ "github.com/Liam0205/pineapple/pine-go/operators"
	"github.com/Liam0205/pineapple/pine-go/pkg/resource"
)

type pipelineFixture struct {
	Name            string                    `json:"name"`
	Requires        []string                  `json:"requires"`
	Config          json.RawMessage           `json:"config"`
	StaticResources map[string]any            `json:"static_resources"`
	Cases           []pipelineCase            `json:"cases"`
}

type pipelineCase struct {
	Name     string          `json:"name"`
	Request  pipelineRequest `json:"request"`
	Expected pipelineResult  `json:"expected"`
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
							if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", expectedVal) {
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
							if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", expectedVal) {
								t.Errorf("items[%d].%s = %v, expected %v", i, key, got, expectedVal)
							}
						}
					}
				})
			}
		})
	}
}
