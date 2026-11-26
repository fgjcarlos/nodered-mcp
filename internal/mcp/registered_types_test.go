package mcp

import (
	"context"
	"net/http"
	"testing"
)

func TestFlowToolsRejectUnregisteredNodeTypes(t *testing.T) {
	const snapshot = `{"rev":"r1","flows":[{"id":"t1","type":"tab"}]}`
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		if serveTestPalette(w, r) {
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/flows" {
			_, _ = w.Write([]byte(snapshot))
			return
		}
		t.Errorf("unexpected runtime request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	})
	const bad = `{"id":"n1","type":"not-installed","z":"t1","x":1,"y":1,"wires":[]}`
	const flow = `{"id":"t1","label":"Example","nodes":[` + bad + `]}`
	const message = `node type "not-installed" is not registered; use search_nodes to find it or install_node to install it`
	for _, tt := range []struct {
		tool   string
		args   map[string]any
		prefix string
	}{
		{"create_flow", map[string]any{"flow": flow}, "calling Node-RED: "},
		{"update_flow", map[string]any{"id": "t1", "flow": flow}, "calling Node-RED: "},
		{"import_flow", map[string]any{"clipboard": `[` + flow + `]`}, "creating flow: "},
		{"set_flows", map[string]any{"flows": `[{"id":"t1","type":"tab"},` + bad + `]`}, "calling Node-RED: "},
	} {
		t.Run(tt.tool, func(t *testing.T) {
			res, err := callHelperTool(context.Background(), s, tt.tool, tt.args)
			if err != nil || res == nil || !res.IsError {
				t.Fatalf("expected tool error, got %v, %v", res, err)
			}
			if got := resultFirstText(res); got != tt.prefix+message {
				t.Errorf("tool error = %q, want %q", got, tt.prefix+message)
			}
		})
	}
}
