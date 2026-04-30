// Operator: transform_by_remote_pineapple
// Type: Transform
// Description: Calls a downstream Pineapple service and maps response fields back to the local frame.
//
// Params:
//   - host (string, required): Downstream service host.
//   - port (int64, required): Downstream service port.
//   - endpoint (string, optional, default="/execute"): Downstream endpoint path.
//   - timeout (float64, optional, default=5.0): Request timeout in seconds.
//   - fail_on_error (bool, optional, default=true): true=fatal on downstream error; false=warning and skip.
//   - common_request ([]string, optional): Downstream common field names, positionally mapped to common_input.
//   - item_request ([]string, optional): Downstream item field names, positionally mapped to item_input.
//   - common_response ([]string, optional): Downstream common response field names, positionally mapped to common_output.
//   - item_response ([]string, optional): Downstream item response field names, positionally mapped to item_output.
//
// Metadata contract (typical usage):
//   CommonInput:  [<local_common_fields...>]
//   CommonOutput: [<local_common_output_fields...>]
//   ItemInput:    [<local_item_fields...>]
//   ItemOutput:   [<local_item_output_fields...>]
package transform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	pine "github.com/Liam0205/pineapple"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "transform_by_remote_pineapple",
		Type:        pine.OpTypeTransform,
		Description: "Calls a downstream Pineapple service and maps response fields back to the local frame.",
		Params: map[string]pine.ParamSpec{
			"host":            {Type: "string", Required: true, Description: "Downstream service host."},
			"port":            {Type: "int64", Required: true, Description: "Downstream service port."},
			"endpoint":        {Type: "string", Required: false, Default: "/execute", Description: "Downstream endpoint path."},
			"timeout":         {Type: "float64", Required: false, Default: 5.0, Description: "Request timeout in seconds."},
			"fail_on_error":   {Type: "bool", Required: false, Default: true, Description: "true=fatal on downstream error; false=warning and skip."},
			"common_request":  {Type: "any", Required: false, Description: "Downstream common field names, positionally mapped to common_input."},
			"item_request":    {Type: "any", Required: false, Description: "Downstream item field names, positionally mapped to item_input."},
			"common_response": {Type: "any", Required: false, Description: "Downstream common response field names, positionally mapped to common_output."},
			"item_response":   {Type: "any", Required: false, Description: "Downstream item response field names, positionally mapped to item_output."},
		},
	}, func() pine.Operator {
		return &RemotePineappleOp{}
	})
}

type RemotePineappleOp struct {
	pine.MetadataHolder
	pine.ConcurrentSafeMarker
	url         string
	timeout     time.Duration
	failOnError bool
	client      *http.Client

	commonReq  []string
	itemReq    []string
	commonResp []string
	itemResp   []string
}

func (o *RemotePineappleOp) Init(params map[string]any) error {
	host, _ := params["host"].(string)
	port := toInt64Param(params["port"])
	endpoint, _ := params["endpoint"].(string)
	if endpoint == "" {
		endpoint = "/execute"
	}
	o.url = fmt.Sprintf("http://%s:%d%s", host, port, endpoint)

	timeout := 5.0
	if v, ok := params["timeout"]; ok {
		if f, ok := v.(float64); ok {
			timeout = f
		}
	}
	o.timeout = time.Duration(timeout * float64(time.Second))

	o.failOnError = true
	if v, ok := params["fail_on_error"]; ok {
		if b, ok := v.(bool); ok {
			o.failOnError = b
		}
	}

	o.commonReq, _ = toStringSlice(params["common_request"])
	o.itemReq, _ = toStringSlice(params["item_request"])
	o.commonResp, _ = toStringSlice(params["common_response"])
	o.itemResp, _ = toStringSlice(params["item_response"])

	o.client = &http.Client{}
	return nil
}

func (o *RemotePineappleOp) Execute(ctx context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	commonReqFields := o.commonReq
	if len(commonReqFields) == 0 {
		commonReqFields = o.CommonInput
	}
	itemReqFields := o.itemReq
	if len(itemReqFields) == 0 {
		itemReqFields = o.ItemInput
	}
	commonRespFields := o.commonResp
	if len(commonRespFields) == 0 {
		commonRespFields = o.CommonOutput
	}
	itemRespFields := o.itemResp
	if len(itemRespFields) == 0 {
		itemRespFields = o.ItemOutput
	}

	reqCommon := make(map[string]any, len(o.CommonInput))
	for i, localField := range o.CommonInput {
		if i < len(commonReqFields) {
			reqCommon[commonReqFields[i]] = in.Common(localField)
		}
	}

	reqItems := make([]map[string]any, in.ItemCount())
	for j := 0; j < in.ItemCount(); j++ {
		item := make(map[string]any, len(o.ItemInput))
		for i, localField := range o.ItemInput {
			if i < len(itemReqFields) {
				item[itemReqFields[i]] = in.Item(j, localField)
			}
		}
		reqItems[j] = item
	}

	reqBody := remoteRequest{Common: reqCommon, Items: reqItems}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("transform_by_remote_pineapple: marshal request: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, o.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("transform_by_remote_pineapple: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return o.handleError(out, fmt.Errorf("transform_by_remote_pineapple: request failed: %w", err))
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return o.handleError(out, fmt.Errorf("transform_by_remote_pineapple: read response: %w", err))
	}

	if resp.StatusCode != http.StatusOK {
		return o.handleError(out, fmt.Errorf("transform_by_remote_pineapple: HTTP %d: %s", resp.StatusCode, string(respBody)))
	}

	var result remoteResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return o.handleError(out, fmt.Errorf("transform_by_remote_pineapple: unmarshal response: %w", err))
	}

	if result.Error != "" {
		return o.handleError(out, fmt.Errorf("transform_by_remote_pineapple: downstream error: %s", result.Error))
	}

	for i, localField := range o.CommonOutput {
		if i < len(commonRespFields) {
			if val, ok := result.Common[commonRespFields[i]]; ok {
				out.SetCommon(localField, val)
			}
		}
	}

	for j := 0; j < in.ItemCount() && j < len(result.Items); j++ {
		for i, localField := range o.ItemOutput {
			if i < len(itemRespFields) {
				if val, ok := result.Items[j][itemRespFields[i]]; ok {
					out.SetItem(j, localField, val)
				}
			}
		}
	}

	return nil
}

func (o *RemotePineappleOp) handleError(out *pine.OperatorOutput, err error) error {
	if o.failOnError {
		return err
	}
	out.SetWarning(err)
	return nil
}

type remoteRequest struct {
	Common map[string]any   `json:"common"`
	Items  []map[string]any `json:"items"`
}

type remoteResponse struct {
	Common map[string]any   `json:"common"`
	Items  []map[string]any `json:"items"`
	Error  string           `json:"error,omitempty"`
}

