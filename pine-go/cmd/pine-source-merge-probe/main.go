// pine-source-merge-probe: cross-runtime probe for the _source rewrite
// exemption of the declared-output contract (issue #224).
//
// A merge keeps the first item per "id" and rewrites the kept item's _source
// with the sorted, comma-joined tags of its duplicates — the shape the DSL
// cannot declare, because it rejects underscore-prefixed outputs. Each
// scenario prints:
//
//	scenario=<name> error=<err or "nil"> items=[<id>:<_source>;...]
//
// Items are printed only when the run succeeds. cross-validate Section 24
// runs this probe against pine-java's SourceMergeProbe and pine-cpp's
// pineapple-source-merge-probe and asserts stdout is byte-identical.
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	pine "github.com/Liam0205/pineapple/pine-go"
	_ "github.com/Liam0205/pineapple/pine-go/operators"
)

// mergeOp dedups by "id" and rewrites the kept item's _source. With extra set
// it also writes an undeclared "extra" field on every kept item.
type mergeOp struct {
	extra bool
}

func (o *mergeOp) Init(map[string]any) error { return nil }

func (o *mergeOp) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	kept := map[string]int{}
	tags := map[int][]string{}
	for i := range in.ItemCount() {
		key, _ := in.Item(i, "id").(string)
		tag, _ := in.Item(i, "_source").(string) // "" when not projected
		if j, ok := kept[key]; ok {
			tags[j] = append(tags[j], tag)
			out.RemoveItem(i)
			continue
		}
		kept[key] = i
		tags[i] = []string{tag}
	}
	for i, list := range tags {
		if len(list) > 1 {
			sort.Strings(list)
			out.SetItem(i, "_source", strings.Join(list, ","))
		}
		if o.extra {
			out.SetItem(i, "extra", 1.0)
		}
	}
	return nil
}

// rowSetMerge carries the row-set markers the exemption requires.
type rowSetMerge struct {
	mergeOp
	pine.ConsumesRowSetMarker
	pine.MutatesRowSetMarker
}

func config(typeName, itemInput string) string {
	return `{
  "pipeline_config": {
    "operators": {
      "m": {"type_name": "` + typeName + `",
        "$metadata": {"common_input": [], "common_output": [], "item_input": ` + itemInput + `, "item_output": []}}
    },
    "pipeline_map": {}
  },
  "pipeline_group": {"main": {"pipeline": ["m"]}},
  "flow_contract": {"common_input": [], "common_output": [], "item_input": ["id", "_source"], "item_output": ["id", "_source"]}
}`
}

func run(name, cfg string) {
	engine, err := pine.NewEngine([]byte(cfg))
	if err != nil {
		fmt.Printf("scenario=%s FAIL:NewEngine: %v\n", name, err)
		os.Exit(1)
	}
	defer engine.Close()
	items := []map[string]any{
		{"id": "x", "_source": "b"},
		{"id": "x", "_source": "a"},
		{"id": "y", "_source": "c"},
	}
	res, err := engine.Execute(context.Background(), &pine.Request{Common: map[string]any{}, Items: items})
	errText := "nil"
	var values []string
	if err != nil {
		errText = err.Error()
	} else {
		for _, item := range res.Items {
			values = append(values, fmt.Sprintf("%v:%v", item["id"], item["_source"]))
		}
	}
	fmt.Printf("scenario=%s error=%s items=[%s]\n", name, errText, strings.Join(values, ";"))
}

func main() {
	register := func(name, desc string, factory func() pine.Operator) {
		pine.Register(pine.OperatorSchema{Name: name, Type: pine.OpTypeMerge, Description: desc}, factory)
	}
	register("probe224_merge", "probe: row-set merge rewriting _source",
		func() pine.Operator { return &rowSetMerge{} })
	register("probe224_merge_extra", "probe: row-set merge also writing an undeclared field",
		func() pine.Operator { return &rowSetMerge{mergeOp: mergeOp{extra: true}} })
	register("probe224_merge_plain", "probe: merge without row-set markers",
		func() pine.Operator { return &mergeOp{} })

	// MutatesRowSet and reads _source: the rewrite is exempt.
	run("exempt", config("probe224_merge", `["id", "_source"]`))
	// Other undeclared writes are still reported, without _source.
	run("exempt_other_undeclared", config("probe224_merge_extra", `["id", "_source"]`))
	// Does not read _source: no exemption.
	run("no_source_input", config("probe224_merge", `["id"]`))
	// Not MutatesRowSet: no exemption.
	run("no_row_set_markers", config("probe224_merge_plain", `["id", "_source"]`))
}
