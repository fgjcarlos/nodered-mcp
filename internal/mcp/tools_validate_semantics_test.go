package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestValidateFlowSemanticWarnings(t *testing.T) {
	type warning struct{ Kind, NodeID, Message string }
	cases := []struct {
		name, flow string
		want       []warning
	}{
		{
			name: "declared outputs in a flat flow",
			flow: `[{"id":"t","type":"tab"},
				{"id":"fn","type":"function","z":"t","x":1,"y":1,"outputs":2,"wires":[[]]},
				{"id":"tr","type":"trigger","z":"t","x":1,"y":1,"outputs":"2","wires":[]},
				{"id":"sw","type":"switch","z":"t","x":1,"y":1,"outputs":1,"rules":[{"t":"true"},{"t":"else"}],"wires":[[]]},
				{"id":"custom","type":"custom","z":"t","x":1,"y":1,"outputs":5,"wires":[[]]},
				{"id":"ok","type":"function","z":"t","x":1,"y":1,"outputs":2,"wires":[[],[]]}]`,
			want: []warning{
				{"output_count_mismatch", "fn", `node "fn" (function) has wires length 1, expected 2`},
				{"output_count_mismatch", "tr", `node "tr" (trigger) has wires length 0, expected 2`},
				{"output_count_mismatch", "sw", `node "sw" (switch) has wires length 1, expected 2`},
			},
		},
		{
			name: "Change and Switch property types in a tab object",
			flow: `{"id":"t","nodes":[
				{"id":"c","type":"change","x":1,"y":1,"rules":[{"t":"set","pt":"str","p":"payload","tot":"str","to":"hello"},{"t":"move","pt":"flow","p":"a","tot":"str","to":"b"},{"t":"set","p":"payload","tot":"str","to":"hello"}]},
				{"id":"s","type":"switch","x":1,"y":1,"propertyType":"str","property":"payload"},
				{"id":"ok","type":"switch","x":1,"y":1,"propertyType":"jsonata","property":"payload","rules":[{"t":"eq","vt":"str","v":"hello"}]}]}`,
			want: []warning{
				{"invalid_property_type", "c", `node "c" (change) rule 1 pt must be msg, flow or global; got "str"`},
				{"invalid_property_type", "c", `node "c" (change) rule 2 tot must be msg, flow or global; got "str"`},
				{"invalid_property_type", "s", `node "s" (switch) propertyType must be msg, flow, global, jsonata or env; got "str"`},
			},
		},
		{
			name: "HTTP reachability through branches cycles and cross-tab links",
			flow: `[{"id":"t","type":"tab"},{"id":"t2","type":"tab"},
				{"id":"no","type":"http in","z":"t","x":1,"y":1,"wires":[["dead"]]},
				{"id":"dead","type":"debug","z":"t","x":1,"y":1,"wires":[["dead"]]},
				{"id":"yes","type":"http in","z":"t","x":1,"y":1,"wires":[["dead","out"]]},
				{"id":"out","type":"link out","z":"t","x":1,"y":1,"links":["in"],"wires":[]},
				{"id":"in","type":"link in","z":"t2","x":1,"y":1,"wires":[["reply"]]},
				{"id":"reply","type":"http response","z":"t2","x":1,"y":1}]`,
			want: []warning{{"http_response_unreachable", "no", `node "no" (http in) has no reachable http response in the supplied flow`}},
		},
		{
			name: "warnings omitted for clean flow and inconclusive external path",
			flow: `{"id":"t","nodes":[{"id":"h","type":"http in","x":1,"y":1,"wires":[["out"]]},
				{"id":"out","type":"link out","x":1,"y":1,"links":["external"],"wires":[]}]}`,
		},
	}
	s := newTestServer(t, true)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := s.handleValidateFlow(context.Background(), mcp.CallToolRequest{
				Params: mcp.CallToolParams{Arguments: map[string]any{"flow": tc.flow}},
			})
			if err != nil || result == nil || result.IsError || len(result.Content) != 1 {
				t.Fatalf("result = %+v, err = %v", result, err)
			}
			content, ok := result.Content[0].(mcp.TextContent)
			if !ok {
				t.Fatalf("expected text content, got %T", result.Content[0])
			}
			summary, body, _ := strings.Cut(content.Text, "\n\n```json\n")
			var got struct {
				OK       bool              `json:"ok"`
				Issues   []json.RawMessage `json:"issues"`
				Warnings []warning         `json:"warnings"`
			}
			if err := json.Unmarshal([]byte(strings.TrimSuffix(body, "\n```")), &got); err != nil {
				t.Fatalf("invalid result JSON: %v\n%s", err, content.Text)
			}
			wantSummary := "ok — 0 issues"
			if len(tc.want) != 0 {
				wantSummary += fmt.Sprintf(", %d warning(s)", len(tc.want))
			} else if strings.Contains(body, `"warnings"`) {
				t.Errorf("clean result must omit the warnings field: %s", body)
			}
			if summary != wantSummary || !got.OK || got.Issues == nil || len(got.Issues) != 0 || !slices.Equal(got.Warnings, tc.want) {
				t.Errorf("want summary %q, ok, no issues, warnings %+v; got %s", wantSummary, tc.want, content.Text)
			}
		})
	}
}
