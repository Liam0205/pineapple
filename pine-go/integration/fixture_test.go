package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
	_ "github.com/Liam0205/pineapple/pine-go/operators"
)

type fixtureFile struct {
	Operator string        `json:"operator"`
	Cases    []fixtureCase `json:"cases"`
}

type fixtureCase struct {
	Name     string          `json:"name"`
	Params   map[string]any  `json:"params"`
	Metadata fixtureMetadata `json:"metadata"`
	Input    fixtureInput    `json:"input"`
	Expected fixtureExpected `json:"expected"`
}

type fixtureMetadata struct {
	CommonInput  []string `json:"common_input"`
	ItemInput    []string `json:"item_input"`
	CommonOutput []string `json:"common_output"`
	ItemOutput   []string `json:"item_output"`
}

type fixtureInput struct {
	Common map[string]any   `json:"common"`
	Items  []map[string]any `json:"items"`
}

type fixtureExpected struct {
	Common         map[string]any   `json:"common"`
	Items          []map[string]any `json:"items"`
	AddedItems     []map[string]any `json:"added_items"`
	RemovedIndices []int            `json:"removed_indices"`
	ItemOrder      []int            `json:"item_order"`
	Warnings       []string         `json:"warnings"`
}

func TestFixtures(t *testing.T) {
	files, err := filepath.Glob("../../fixtures/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("no fixture files found")
	}

	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var ff fixtureFile
			if err := json.Unmarshal(data, &ff); err != nil {
				t.Fatal(err)
			}
			for _, tc := range ff.Cases {
				t.Run(tc.Name, func(t *testing.T) {
					runFixtureCase(t, ff.Operator, tc)
				})
			}
		})
	}
}

func runFixtureCase(t *testing.T, operatorName string, tc fixtureCase) {
	t.Helper()

	// Build operator via registry
	op, _, err := pine.BuildOperator(operatorName, tc.Params)
	if err != nil {
		t.Fatalf("BuildOperator(%q): %v", operatorName, err)
	}

	// Inject metadata if the operator is MetadataAware
	if ma, ok := op.(interface {
		SetMetadata(commonInput, commonOutput, itemInput, itemOutput []string)
	}); ok {
		ma.SetMetadata(
			tc.Metadata.CommonInput,
			tc.Metadata.CommonOutput,
			tc.Metadata.ItemInput,
			tc.Metadata.ItemOutput,
		)
	}

	// Build input
	input := pine.NewOperatorInput(tc.Input.Common, tc.Input.Items)

	// Execute
	output := pine.NewOperatorOutput()
	err = op.Execute(context.Background(), input, output)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Validate warnings
	if tc.Expected.Warnings != nil {
		w := output.GetWarning()
		if w == nil && len(tc.Expected.Warnings) > 0 {
			t.Errorf("expected warnings but got none")
		} else if w != nil {
			warnStr := w.Error()
			for _, substr := range tc.Expected.Warnings {
				if !strings.Contains(warnStr, substr) {
					t.Errorf("warning %q does not contain %q", warnStr, substr)
				}
			}
		}
	}

	// Validate removed indices (Filter)
	if tc.Expected.RemovedIndices != nil {
		removed := output.GetRemovedItems()
		for _, idx := range tc.Expected.RemovedIndices {
			if _, ok := removed[idx]; !ok {
				t.Errorf("expected item[%d] to be removed, but it wasn't", idx)
			}
		}
		if len(removed) != len(tc.Expected.RemovedIndices) {
			t.Errorf("removed %d items, expected %d", len(removed), len(tc.Expected.RemovedIndices))
		}
	}

	// Validate added items (Recall)
	if tc.Expected.AddedItems != nil {
		added := output.GetAddedItems()
		if len(added) != len(tc.Expected.AddedItems) {
			t.Fatalf("added_items: got %d, expected %d", len(added), len(tc.Expected.AddedItems))
		}
		for i, expectedItem := range tc.Expected.AddedItems {
			for key, expectedVal := range expectedItem {
				got := added[i][key]
				if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", expectedVal) {
					t.Errorf("added_items[%d].%s = %v, expected %v", i, key, got, expectedVal)
				}
			}
		}
	}

	// Validate common writes
	if tc.Expected.Common != nil {
		cw := output.GetCommonWrites()
		for key, expectedVal := range tc.Expected.Common {
			got, ok := cw[key]
			if !ok {
				t.Errorf("common_writes missing key %q", key)
				continue
			}
			if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", expectedVal) {
				t.Errorf("common_writes[%q] = %v, expected %v", key, got, expectedVal)
			}
		}
	}

	// Validate item order (Reorder)
	if tc.Expected.ItemOrder != nil {
		order := output.GetItemOrder()
		if len(order) != len(tc.Expected.ItemOrder) {
			t.Fatalf("item_order: got %d entries, expected %d", len(order), len(tc.Expected.ItemOrder))
		}
		for i, expected := range tc.Expected.ItemOrder {
			if order[i] != expected {
				t.Errorf("item_order[%d] = %d, expected %d", i, order[i], expected)
			}
		}
	}

	// Validate item writes (Transform)
	if tc.Expected.Items != nil {
		iw := output.GetItemWrites()
		for i, expectedItem := range tc.Expected.Items {
			for key, expectedVal := range expectedItem {
				writes, ok := iw[i]
				if !ok {
					t.Errorf("item_writes[%d] missing, expected writes for key %q", i, key)
					continue
				}
				got, ok := writes[key]
				if !ok {
					t.Errorf("item_writes[%d] missing key %q", i, key)
					continue
				}
				if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", expectedVal) {
					t.Errorf("item_writes[%d].%s = %v, expected %v", i, key, got, expectedVal)
				}
			}
		}
	}
}
