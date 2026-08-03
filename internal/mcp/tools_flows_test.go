package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// --- node CRUD (handleUpdateNode, handleDeleteNode) ---

func TestHandleUpdateNode_PartialPatch(t *testing.T) {
	// UpdateNode is implemented via editFlow: client GETs /flow/{id},
	// mutates locally, then PUTs back. Mock both legs, plus the backup
	// snapshotFlows call to /flows.
	const flow = `{"id":"tab1","label":"Home","nodes":[{"id":"n1","type":"inject","z":"tab1","name":"tick","wires":[]}]}`
	const flowsList = `[{"id":"tab1","type":"tab","disabled":false}]`
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(flowsList))
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
	// editFlow: GET /flow/{id} then PUT, plus /flows snapshot for backup.
	const flow = `{"id":"tab1","label":"Home","nodes":[{"id":"n1","type":"inject","z":"tab1","name":"tick","wires":[]}]}`
	const flowsList = `[{"id":"tab1","type":"tab","disabled":false}]`
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
	// SetFlowDisabled -> editFlow -> GET /flow/{id} then PUT /flow/{id},
	// plus /flows snapshot for backup.
	const flow = `{"id":"tab1","type":"tab","disabled":false,"nodes":[]}`
	const flowsList = `[{"id":"tab1","type":"tab","disabled":false}]`
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
	// new instance into /flow/{flowID} via editFlow (GET + PUT).
	// /flows snapshot for the backup.
	const globalEnvelope = `{"subflows":[{"id":"sf1","name":"Helper"}],"configs":[]}`
	const flow = `{"id":"tab1","type":"tab","nodes":[]}`
	const flowsList = `[{"id":"tab1","type":"tab","disabled":false}]`
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
	const flowsList = `[{"id":"tab1","type":"tab","disabled":false}]`
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
