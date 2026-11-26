package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// serverWithMock returns (Server, *httptest.Server) so tests can both
// shape the upstream response and reach the handler directly via the
// receiver. Centralises the boilerplate so individual tests stay
// focused on the assertion. The BackupDir is the per-test temp dir so
// any handler that writes backups does not pollute the working tree.
func serverWithMock(t *testing.T, h http.HandlerFunc) (*Server, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir(), SkipVersionProbe: true})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return New(c, Options{Version: "test"}), srv
}

// serverWithMockVersionProbe is serverWithMock for the fixtures that
// exercise the Node-RED version detection: they mock GET /settings
// and expect the probe to reach it. Every other fixture opts out of
// the probe explicitly (issue #340), because a mock that records
// every request would fail on the unexpected GET /settings.
func serverWithMockVersionProbe(t *testing.T, h http.HandlerFunc) (*Server, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return New(c, Options{Version: "test"}), srv
}

// call runs a handler with a single string argument. Keeps the
// happy-path tests terse; error-path tests build their own requests.
func call(t *testing.T, h func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) (*mcp.CallToolResult, error) {
	t.Helper()
	return h(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}})
}

// --- flow lifecycle (handleSearchFlows, handleGetFlow) ---

func TestHandleSearchFlows_EmptyResultUsesNoMatchCopy(t *testing.T) {
	// Upstream returns an empty list — handler must surface the
	// "no nodes matched" hint, not a confusing empty JSON array.
	s, _ := serverWithMock(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[]"))
	})
	res, err := call(t, s.handleSearchFlows, map[string]any{"query": "nope"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "No nodes matched") {
		t.Errorf("expected 'No nodes matched' copy, got %q", tc.Text)
	}
}

func TestHandleSearchFlows_ClampsLimitAndReportsTruncation(t *testing.T) {
	// 150 candidate nodes; default limit is 20 but the handler
	// clamps anything > 100 down to 100 and reports the truncation.
	s, _ := serverWithMock(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"t","type":"tab","nodes":[]}]`))
	})
	res, _ := call(t, s.handleSearchFlows, map[string]any{"limit": float64(500)})
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "matched") {
		t.Errorf("expected header to mention match count, got %q", tc.Text)
	}
}

func TestHandleSearchFlows_DefaultsLimitWhenZero(t *testing.T) {
	// limit=0 must be treated as the default (20), not "0 items".
	s, _ := serverWithMock(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[]"))
	})
	res, _ := call(t, s.handleSearchFlows, map[string]any{"limit": float64(0)})
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
}

func TestHandleSearchFlows_PropagatesUpstreamError(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	res, _ := call(t, s.handleSearchFlows, map[string]any{})
	if !res.IsError {
		t.Fatal("expected error result from upstream 500")
	}
}

func TestHandleGetFlow_ReturnsFencedJSON(t *testing.T) {
	const body = `{"id":"tab1","label":"Home","nodes":[]}`
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/flow/tab1" {
			t.Errorf("expected /flow/tab1, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(body))
	})
	res, err := call(t, s.handleGetFlow, map[string]any{"id": "tab1"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "tab1") {
		t.Errorf("response missing id; got %q", tc.Text)
	}
}

func TestHandleGetFlow_MissingIDIsError(t *testing.T) {
	s, _ := serverWithMock(t, func(_ http.ResponseWriter, _ *http.Request) {})
	res, _ := call(t, s.handleGetFlow, map[string]any{})
	if !res.IsError {
		t.Fatal("missing id should be a typed error, not a panic")
	}
}

// --- node CRUD (handleAddNode, handleUpdateNode, handleDeleteNode) ---

func TestAddNodeWiringMessage(t *testing.T) {
	const snapshot = `{"rev":"r","flows":[{"id":"tab1","type":"tab"},{"id":"n2","type":"debug","z":"tab1","x":300,"y":100,"wires":[]}]}`
	const prefix = `Node added to flow "tab1" (a backup was taken first). `
	const unwired = prefix + "Wire it up with connect_nodes."
	const wired = prefix + "The node was added with its wires."

	for _, tc := range []struct {
		name, wires, want string
	}{
		{"absent", "", unwired},
		{"empty", `[]`, unwired},
		{"empty output", `[[]]`, unwired},
		{"wired", `[["n2"]]`, wired},
		{"wired later output", `[[],["n2"]]`, wired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
				if serveTestPalette(w, r) {
					return
				}
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/flows":
					_, _ = w.Write([]byte(snapshot))
				case r.Method == http.MethodPost && r.URL.Path == "/flows":
					var deploy struct {
						Flows []struct {
							ID    string
							Wires json.RawMessage
						}
					}
					if err := json.NewDecoder(r.Body).Decode(&deploy); err != nil {
						t.Error(err)
						http.Error(w, "invalid deployment", http.StatusBadRequest)
						return
					}
					found := false
					for _, node := range deploy.Flows {
						if node.ID == "n1" {
							found = true
							if string(node.Wires) != tc.wires {
								t.Errorf("deployed wires = %s, want %s", node.Wires, tc.wires)
							}
						}
					}
					if !found {
						t.Error("deployment did not include n1")
					}
					w.WriteHeader(http.StatusNoContent)
				default:
					http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
				}
			})
			node := map[string]any{"id": "n1", "type": "inject", "x": 100, "y": 100}
			if tc.wires != "" {
				node["wires"] = json.RawMessage(tc.wires)
			}
			res, err := callHelperTool(context.Background(), s, "add_node", map[string]any{
				"flow_id": "tab1", "node": node,
			})
			if err != nil || res.IsError || len(res.Content) != 1 {
				t.Fatalf("add_node: result = %+v, err = %v", res, err)
			}
			if got, ok := res.Content[0].(mcp.TextContent); !ok || got.Text != tc.want {
				t.Errorf("add_node content = %+v, want text %q", res.Content[0], tc.want)
			}
		})
	}
}

func TestHandleUpdateNode_PartialPatch(t *testing.T) {
	// UpdateNode reads the nested tab, then deploys a revisioned flat config.
	const flow = `{"id":"tab1","label":"Home","nodes":[{"id":"n1","type":"inject","z":"tab1","name":"tick","wires":[]}]}`
	// The flat array is the whole configuration: the tab and the nodes
	// that belong to it. The granular edits are built on it (issue #345).
	const flowsList = `{"rev":"r","flows":[{"id":"tab1","type":"tab","disabled":false},{"id":"n1","type":"inject","z":"tab1","name":"tick","wires":[]}]}`
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		if serveTestPalette(w, r) {
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(flowsList))
		case r.Method == http.MethodPost && r.URL.Path == "/flows":
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/flow/tab1":
			_, _ = w.Write([]byte(flow))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
		}
	})
	res, err := call(t, s.handleUpdateNode, map[string]any{
		"flow_id":    "tab1",
		"node_id":    "n1",
		"properties": `{"name":"tick","topic":"home/door"}`,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "name") || !strings.Contains(tc.Text, "topic") {
		t.Errorf("response should list touched keys; got %q", tc.Text)
	}
}

func TestHandleUpdateNode_RejectsNonObjectProperties(t *testing.T) {
	s, _ := serverWithMock(t, func(_ http.ResponseWriter, _ *http.Request) {})
	res, _ := call(t, s.handleUpdateNode, map[string]any{
		"flow_id":    "tab1",
		"node_id":    "n1",
		"properties": `not an object`,
	})
	if !res.IsError {
		t.Fatal("non-object properties should be a typed error")
	}
}

func TestHandleUpdateNode_PropagatesUpstreamError(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	res, _ := call(t, s.handleUpdateNode, map[string]any{
		"flow_id":    "tab1",
		"node_id":    "n1",
		"properties": `{}`,
	})
	if !res.IsError {
		t.Fatal("expected error result from upstream 500")
	}
}

func TestHandleDeleteNode_RoundTrip(t *testing.T) {
	// editFlow: nested GET, revisioned snapshot, then POST /flows.
	const flow = `{"id":"tab1","label":"Home","nodes":[{"id":"n1","type":"inject","z":"tab1","name":"tick","wires":[]}]}`
	// The flat array is the whole configuration: the tab and the nodes
	// that belong to it. The granular edits are built on it (issue #345).
	const flowsList = `{"rev":"r","flows":[{"id":"tab1","type":"tab","disabled":false},{"id":"n1","type":"inject","z":"tab1","name":"tick","wires":[]}]}`
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			_, _ = w.Write([]byte(flowsList))
		case "/flow/tab1":
			_, _ = w.Write([]byte(flow))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
		}
	})
	res, err := call(t, s.handleDeleteNode, map[string]any{"flow_id": "tab1", "node_id": "n1"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
}

func TestHandleDeleteNode_MissingArgsIsError(t *testing.T) {
	s, _ := serverWithMock(t, func(_ http.ResponseWriter, _ *http.Request) {})
	if res, _ := call(t, s.handleDeleteNode, map[string]any{}); !res.IsError {
		t.Fatal("missing flow_id/node_id should be a typed error")
	}
}

// --- flow lifecycle (handleDeleteFlow, handleDisableFlow, handleEnableFlow) ---

func TestHandleDeleteFlow_RoundTrip(t *testing.T) {
	// DeleteFlow does snapshotFlows (GET /flows) then DELETE /flow/{id}.
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[{"id":"tab1","type":"tab","disabled":false}]`))
		case r.Method == http.MethodDelete && r.URL.Path == "/flow/tab1":
			_, _ = w.Write([]byte(`{}`))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
		}
	})
	res, _ := call(t, s.handleDeleteFlow, map[string]any{"id": "tab1"})
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
}

func TestHandleDeleteFlow_MissingIDIsError(t *testing.T) {
	s, _ := serverWithMock(t, func(_ http.ResponseWriter, _ *http.Request) {})
	if res, _ := call(t, s.handleDeleteFlow, map[string]any{}); !res.IsError {
		t.Fatal("missing id should be a typed error")
	}
}

func TestHandleDisableEnableFlow_RoundTrip(t *testing.T) {
	// SetFlowDisabled reads the tab and deploys via revisioned /flows.
	const flow = `{"id":"tab1","type":"tab","disabled":false,"nodes":[]}`
	const flowsList = `{"rev":"r","flows":[{"id":"tab1","type":"tab","disabled":false}]}`
	for _, name := range []string{"disable_flow", "enable_flow"} {
		t.Run(name, func(t *testing.T) {
			s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/flows":
					_, _ = w.Write([]byte(flowsList))
				case "/flow/tab1":
					_, _ = w.Write([]byte(flow))
				default:
					http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
				}
			})
			handler := s.handleDisableFlow
			if name == "enable_flow" {
				handler = s.handleEnableFlow
			}
			res, _ := call(t, handler, map[string]any{"id": "tab1"})
			if res.IsError {
				t.Fatalf("unexpected error: %v", res.Content)
			}
		})
	}
}

// --- subflow CRUD + helpers ---

func TestHandleGetSubflow_RoundTrip(t *testing.T) {
	// /flow/global returns {subflows: [...], configs: [...]} envelope.
	const envelope = `{"subflows":[{"id":"sf1","name":"Helper"}],"configs":[]}`
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/flow/global" {
			t.Errorf("expected /flow/global, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(envelope))
	})
	res, err := call(t, s.handleGetSubflow, map[string]any{"id": "sf1"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "sf1") {
		t.Errorf("expected subflow id in body, got %q", tc.Text)
	}
}

func TestHandleCreateSubflow_ObjectArgument(t *testing.T) {
	// CreateSubflow reads /flow/global (must NOT yet contain the id,
	// otherwise the client treats it as an update collision), then
	// PUTs the merged envelope back. snapshotFlows adds GET /flows.
	const empty = `{"subflows":[],"configs":[]}`
	const created = `{"subflows":[{"id":"sf2","name":"New"}],"configs":[]}`
	const flowsList = `[{"id":"tab1","type":"tab","disabled":false}]`
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			_, _ = w.Write([]byte(flowsList))
		case "/flow/global":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(empty))
				return
			}
			_, _ = w.Write([]byte(created))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
		}
	})
	res, err := call(t, s.handleCreateSubflow, map[string]any{
		"subflow": map[string]any{"id": "sf2", "name": "New"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
}

func TestHandleCreateSubflow_StringArgument(t *testing.T) {
	// MCP args can be either a JSON object or a JSON-encoded string;
	// subflowParam supports both. Cover the string path.
	const empty = `{"subflows":[],"configs":[]}`
	const created = `{"subflows":[{"id":"sf3","name":"X"}],"configs":[]}`
	const flowsList = `[{"id":"tab1","type":"tab","disabled":false}]`
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			_, _ = w.Write([]byte(flowsList))
		case "/flow/global":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(empty))
				return
			}
			_, _ = w.Write([]byte(created))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
		}
	})
	res, err := call(t, s.handleCreateSubflow, map[string]any{
		"subflow": `{"id":"sf3","name":"X"}`,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
}

func TestHandleCreateSubflow_MissingIsError(t *testing.T) {
	s, _ := serverWithMock(t, func(_ http.ResponseWriter, _ *http.Request) {})
	if res, _ := call(t, s.handleCreateSubflow, map[string]any{}); !res.IsError {
		t.Fatal("missing subflow arg should be a typed error")
	}
}

func TestSubflowParam_RejectsInvalidJSONString(t *testing.T) {
	s, _ := serverWithMock(t, func(_ http.ResponseWriter, _ *http.Request) {})
	res, _ := call(t, s.handleCreateSubflow, map[string]any{
		"subflow": "not json",
	})
	if !res.IsError {
		t.Fatal("non-JSON string should be a typed error")
	}
}

func TestSubflowParam_RejectsNonStringNonObject(t *testing.T) {
	s, _ := serverWithMock(t, func(_ http.ResponseWriter, _ *http.Request) {})
	res, _ := call(t, s.handleCreateSubflow, map[string]any{
		"subflow": 42,
	})
	if !res.IsError {
		t.Fatal("non-string non-object arg should be a typed error")
	}
}

func TestHandleUpdateSubflow_RoundTrip(t *testing.T) {
	// /flow/global must already contain the subflow with id=sf1 for
	// the update to find it; PUT echoes back the updated envelope.
	// /flows snapshot for the backup.
	const envelope = `{"subflows":[{"id":"sf1","name":"Renamed"}],"configs":[]}`
	const flowsList = `[{"id":"tab1","type":"tab","disabled":false}]`
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			_, _ = w.Write([]byte(flowsList))
		case "/flow/global":
			_, _ = w.Write([]byte(envelope))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
		}
	})
	res, err := call(t, s.handleUpdateSubflow, map[string]any{
		"id":      "sf1",
		"subflow": map[string]any{"id": "sf1", "name": "Renamed"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
}

func TestHandleDeleteSubflow_RoundTrip(t *testing.T) {
	// /flow/global envelope must contain the subflow being deleted;
	// DELETE rewrites the envelope without it. /flows for backup.
	const envelope = `{"subflows":[{"id":"sf1","name":"Helper"}],"configs":[]}`
	const flowsList = `[{"id":"tab1","type":"tab","disabled":false}]`
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			_, _ = w.Write([]byte(flowsList))
		case "/flow/global":
			_, _ = w.Write([]byte(envelope))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
		}
	})
	res, err := call(t, s.handleDeleteSubflow, map[string]any{"id": "sf1"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
}

func TestHandleInstantiateSubflow_RoundTrip(t *testing.T) {
	// InstantiateSubflow: GetSubflow reads /flow/global; then writes the
	// new instance via editFlow (nested GET + revisioned POST /flows).
	const globalEnvelope = `{"subflows":[{"id":"sf1","name":"Helper"}],"configs":[]}`
	const flow = `{"id":"tab1","type":"tab","nodes":[]}`
	// The runtime serves the subflow definition in the same flat array
	// as the tabs, so the fixture carries it.
	const flowsList = `{"rev":"r","flows":[{"id":"tab1","type":"tab","disabled":false},{"id":"sf1","type":"subflow","name":"Helper","in":[],"out":[]}]}`
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			_, _ = w.Write([]byte(flowsList))
		case "/flow/global":
			_, _ = w.Write([]byte(globalEnvelope))
		case "/flow/tab1":
			_, _ = w.Write([]byte(flow))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
		}
	})
	res, err := call(t, s.handleInstantiateSubflow, map[string]any{
		"flow_id":    "tab1",
		"subflow_id": "sf1",
		"params":     map[string]any{"name": "Instance-1"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
}

func TestHandleInstantiateSubflow_ParamsOptional(t *testing.T) {
	// instanceParamsParam returns nil when "params" is absent — handler
	// must not require it. Same /flow/global + /flow/{id} + /flows trio.
	const globalEnvelope = `{"subflows":[{"id":"sf1","name":"Helper"}],"configs":[]}`
	const flow = `{"id":"tab1","type":"tab","nodes":[]}`
	// The runtime serves the subflow definition in the same flat array
	// as the tabs, so the fixture carries it.
	const flowsList = `{"rev":"r","flows":[{"id":"tab1","type":"tab","disabled":false},{"id":"sf1","type":"subflow","name":"Helper","in":[],"out":[]}]}`
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			_, _ = w.Write([]byte(flowsList))
		case "/flow/global":
			_, _ = w.Write([]byte(globalEnvelope))
		case "/flow/tab1":
			_, _ = w.Write([]byte(flow))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
		}
	})
	res, err := call(t, s.handleInstantiateSubflow, map[string]any{
		"flow_id":    "tab1",
		"subflow_id": "sf1",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
}

func TestInstanceParamsParam_RejectsInvalidJSONString(t *testing.T) {
	s, _ := serverWithMock(t, func(_ http.ResponseWriter, _ *http.Request) {})
	res, _ := call(t, s.handleInstantiateSubflow, map[string]any{
		"flow_id":    "tab1",
		"subflow_id": "sf1",
		"params":     "not json",
	})
	if !res.IsError {
		t.Fatal("non-JSON params string should be a typed error")
	}
}

// A JSON number arrives as a float64, so port 1.5 used to be truncated to
// port 1 (issue #448). The handler is invoked directly: the guard fires
// before any Node-RED call.
func TestHandleConnectNodes_RejectsANonWholePort(t *testing.T) {
	s := newTestServer(t, false)
	for _, port := range []float64{1.5, -1, 1000} {
		res, err := call(t, s.handleConnectNodes, map[string]any{
			"flow_id": "tabA", "from_id": "a", "to_id": "b", "port": port,
		})
		if err != nil {
			t.Fatalf("port %v: handleConnectNodes returned err=%v", port, err)
		}
		tc := res.Content[0].(mcp.TextContent)
		if !res.IsError || !strings.Contains(tc.Text, "whole number from 0 to 999") {
			t.Errorf("port %v: expected a refusal naming the rule, got %q", port, tc.Text)
		}
		if port == 1.5 && !strings.Contains(tc.Text, "1.5") {
			t.Errorf("the refusal must echo the decimal port as sent, got %q", tc.Text)
		}
	}
}

// A wire that already exists is answered as such, with neither a deployment
// nor a backup (issue #448): the mock records every request, and the backup
// directory must stay empty. Only the snapshot read is expected.
func TestHandleConnectNodes_ExistingWireDeploysNothing(t *testing.T) {
	var requests []string
	tab := []byte(`{"id":"tabA","label":"Home","nodes":[{"id":"n1","type":"inject","z":"tabA","x":1,"y":1,"wires":[["n2"]]},{"id":"n2","type":"function","z":"tabA","x":2,"y":1,"wires":[]}]}`)
	s, backupDir := serverWithMockAndBackups(t, func(w http.ResponseWriter, r *http.Request) {
		if serveTestPalette(w, r) {
			return
		}
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet && r.URL.Path == "/flows" {
			_, _ = w.Write(mockFlowSnapshot(r, tab))
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected request", http.StatusBadRequest)
	})
	res, err := call(t, s.handleConnectNodes, map[string]any{
		"flow_id": "tabA", "from_id": "n1", "to_id": "n2", "port": float64(0),
	})
	if err != nil {
		t.Fatalf("handleConnectNodes returned err=%v", err)
	}
	tc := res.Content[0].(mcp.TextContent)
	if res.IsError || !strings.Contains(tc.Text, "already wired") || !strings.Contains(tc.Text, "no backup") {
		t.Errorf("expected an already-wired answer without a backup, got %q", tc.Text)
	}
	for _, req := range requests {
		if req != "GET /flows" {
			t.Errorf("an existing wire must only read the snapshot, got %q", req)
		}
	}
	if files, _ := os.ReadDir(backupDir); len(files) != 0 {
		t.Errorf("an existing wire must not create a backup, got %d files", len(files))
	}
}

// The complement of the test above: a wire that is NOT there must still be
// written. Without it, answering "already connected" for every call would
// look like a pass (issue #448).
func TestHandleConnectNodes_NewWireDeploysAndBacksUp(t *testing.T) {
	tab := []byte(`{"id":"tabA","label":"Home","nodes":[{"id":"n1","type":"function","z":"tabA","x":1,"y":1,"wires":[]},{"id":"n3","type":"debug","z":"tabA","x":3,"y":1,"wires":[]}]}`)
	deployed := false
	s, backupDir := serverWithMockAndBackups(t, func(w http.ResponseWriter, r *http.Request) {
		if serveTestPalette(w, r) {
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/flows" {
			_, _ = w.Write(mockFlowSnapshot(r, tab))
			return
		}
		if mockTabDeployment(r) {
			deployed = true
			w.WriteHeader(http.StatusOK)
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected request", http.StatusBadRequest)
	})
	res, err := call(t, s.handleConnectNodes, map[string]any{
		"flow_id": "tabA", "from_id": "n1", "to_id": "n3", "port": float64(0),
	})
	if err != nil {
		t.Fatalf("handleConnectNodes returned err=%v", err)
	}
	tc := res.Content[0].(mcp.TextContent)
	if res.IsError || !strings.Contains(tc.Text, "Wired") || !strings.Contains(tc.Text, "backup was taken") {
		t.Fatalf("a new wire must be written and say so, got %q", tc.Text)
	}
	if !deployed {
		t.Error("a new wire must deploy")
	}
	if files, _ := os.ReadDir(backupDir); len(files) == 0 {
		t.Error("a new wire must create a backup")
	}
}

// The issue's acceptance criterion is that validate_flow does not answer ok
// on a flow carrying an impossible wire: the kind has to reach the caller and
// flip ok, on both shapes the tool accepts (issue #448).
func TestHandleValidateFlow_ImpossibleWireAffectsOK(t *testing.T) {
	s := newTestServer(t, true)
	for name, flow := range map[string]string{
		"flat": `[{"id":"t","type":"tab"},{"id":"f","type":"function","z":"t","x":1,"y":1,"wires":[["i"]]},{"id":"i","type":"inject","z":"t","x":2,"y":2,"wires":[]}]`,
		"tab":  `{"id":"t","nodes":[{"id":"a","type":"debug","z":"t","x":1,"y":1,"wires":[["b"]]},{"id":"b","type":"inject","z":"t","x":2,"y":2,"wires":[]}]}`,
	} {
		res, err := call(t, s.handleValidateFlow, map[string]any{"flow": flow})
		if err != nil || res == nil || res.IsError {
			t.Fatalf("%s: err=%v res=%v", name, err, res)
		}
		tc := res.Content[0].(mcp.TextContent)
		if !strings.Contains(tc.Text, `"ok": false`) || !strings.Contains(tc.Text, "impossible_wire") || !strings.Contains(tc.Text, "1 issue(s)") {
			t.Errorf("%s: an impossible wire must flip ok and name the kind, got %s", name, tc.Text)
		}
	}
}
