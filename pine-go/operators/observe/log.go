// Operator: observe_log
// Type: Observe
// Description: Reads declared input fields and writes them to Go standard log.
//
//	This is a read-only operator: it produces no output fields and does not
//	modify the DataFrame. It is exempt from dead-code detection.
//
// Params:
//   - log_prefix (string, optional, default ""): Prefix prepended to each log line.
//
// Metadata contract (typical usage):
//
//	CommonInput:  [<fields to observe>]
//	CommonOutput: []
//	ItemInput:    [<fields to observe>]
//	ItemOutput:   []
package observe

import (
	"context"
	"encoding/json"
	"log"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "observe_log",
		Type:        pine.OpTypeObserve,
		Description: "Reads declared input fields and writes them to Go standard log. This is a read-only operator: it produces no output fields and does not modify the DataFrame. It is exempt from dead-code detection.",
		Params: map[string]pine.ParamSpec{
			"log_prefix": {Type: "string", Required: false, Default: "", Description: "Prefix prepended to each log line."},
		},
	}, func() pine.Operator {
		return &LogOp{}
	})
}

// LogOp writes declared input fields to the Go standard logger.
type LogOp struct {
	pine.MetadataHolder
	prefix string
}

func (o *LogOp) Init(params map[string]any) error {
	if v, ok := params["log_prefix"]; ok {
		o.prefix = v.(string)
	}
	return nil
}

func (o *LogOp) Execute(_ context.Context, in *pine.OperatorInput, _ *pine.OperatorOutput) error {
	snapshot := make(map[string]any)

	// Capture common fields
	if len(o.CommonInput) > 0 {
		common := make(map[string]any, len(o.CommonInput))
		for _, k := range o.CommonInput {
			common[k] = in.Common(k)
		}
		snapshot["common"] = common
	}

	// Capture item fields
	if len(o.ItemInput) > 0 && in.ItemCount() > 0 {
		items := make([]map[string]any, in.ItemCount())
		for i := 0; i < in.ItemCount(); i++ {
			row := make(map[string]any, len(o.ItemInput))
			for _, k := range o.ItemInput {
				row[k] = in.Item(i, k)
			}
			items[i] = row
		}
		snapshot["items"] = items
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		log.Printf("[observe_log] %s marshal error: %v", o.prefix, err)
		return nil // observe errors are non-fatal
	}

	if o.prefix != "" {
		log.Printf("[observe_log] %s %s", o.prefix, string(data))
	} else {
		log.Printf("[observe_log] %s", string(data))
	}

	return nil
}
