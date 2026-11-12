package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// sampleTab is the canonical flow document the round-trip tests
// start from. It carries wires between two nodes, a config-node
// reference, and a label — every shape the editor's clipboard uses.
const sampleTab = `{
  "id": "tab1",
  "label": "Home",
  "nodes": [
    {"id": "n1", "type": "inject", "z": "tab1", "name": "tick", "wires": [["n2"]], "x": 100, "y": 100},
    {"id": "n2", "type": "debug", "z": "tab1", "name": "show", "wires": [], "x": 300, "y": 100}
  ]
}`

// TestImportFlow_HappyPath: import_flow accepts a clipboard array,
// POSTs /flow, and returns the runtime-assigned id.
func TestImportFlow_HappyPath(t *testing.T) {
	var postedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			// backup endpoint
			_, _ = w.Write([]byte(`{"rev":"abc","flows":[]}`))
		case "/flow":
			body := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(body)
			postedBody = body
			_, _ = w.Write([]byte(`{"id":"newtab42","label":"Home"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	s := New(c, Options{Version: "test"})

	clipboard := "[" + sampleTab + "]"
	res, err := s.handleImportFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"clipboard": clipboard}},
	})
	if err != nil {
		t.Fatalf("handleImportFlow: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "newtab42") {
		t.Errorf("response should name the new id, got %q", tc.Text)
	}
	if len(postedBody) == 0 {
		t.Error("server was not POSTed to")
	}
}

// TestImportFlow_RejectsMultiTabClipboard: import_flow accepts only
// single-tab pastes. A multi-tab array is rejected without hitting
// the runtime.
func TestImportFlow_RejectsMultiTabClipboard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be called for a multi-tab clipboard, got %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	s := New(c, Options{Version: "test"})

	multi := `[{"id":"a","type":"tab"},{"id":"b","type":"tab"}]`
	res, err := s.handleImportFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"clipboard": multi}},
	})
	if err != nil {
		t.Fatalf("handleImportFlow: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected an error result, got %+v", res)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "2 tabs") {
		t.Errorf("error should mention the tab count, got %q", tc.Text)
	}
}

// TestImportFlow_RejectsNonArrayClipboard: a non-array clipboard is
// rejected without hitting the runtime.
func TestImportFlow_RejectsNonArrayClipboard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be called for a non-array clipboard")
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	s := New(c, Options{Version: "test"})

	res, err := s.handleImportFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"clipboard": `{"id":"tab","type":"tab"}`}},
	})
	if err != nil {
		t.Fatalf("handleImportFlow: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected an error result, got %+v", res)
	}
}

// TestImportFlow_RejectsInvalidJSON: a string that is not valid JSON
// is rejected by the param layer.
func TestImportFlow_RejectsInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server should not be called for invalid JSON")
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	s := New(c, Options{Version: "test"})

	res, err := s.handleImportFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"clipboard": "not json"}},
	})
	if err != nil {
		t.Fatalf("handleImportFlow: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected an error, got %+v", res)
	}
}

// TestImportFlow_AcceptsRawArray: the clipboard arg accepts a raw
// array (not just a JSON-encoded string), matching how MCP clients
// naturally describe data.
func TestImportFlow_AcceptsRawArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			_, _ = w.Write([]byte(`{"rev":"x","flows":[]}`))
		case "/flow":
			_, _ = w.Write([]byte(`{"id":"raw-1","label":"raw"}`))
		default:
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	s := New(c, Options{Version: "test"})

	clipboardArg := []any{
		map[string]any{
			"id":    "raw",
			"label": "raw",
			"nodes": []any{},
		},
	}
	res, err := s.handleImportFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"clipboard": clipboardArg}},
	})
	if err != nil {
		t.Fatalf("handleImportFlow: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "raw-1") {
		t.Errorf("response should name the runtime id, got %q", tc.Text)
	}
}

// TestImportFlow_PreservesWires: the acceptance criterion that
// matters most for clipboard round-trips — wires survive.
func TestImportFlow_PreservesWires(t *testing.T) {
	var postedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			_, _ = w.Write([]byte(`{"rev":"x","flows":[]}`))
		case "/flow":
			b := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(b)
			postedBody = b
			_, _ = w.Write([]byte(`{"id":"wires-1"}`))
		default:
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	s := New(c, Options{Version: "test"})

	clipboard := `[` + sampleTab + `]`
	res, err := s.handleImportFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"clipboard": clipboard}},
	})
	if err != nil {
		t.Fatalf("handleImportFlow: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	// The POST body sent to Node-RED must carry the wire from n1 -> n2.
	if !strings.Contains(string(postedBody), `"n2"`) {
		t.Errorf("posted body should carry the wire target n2: %s", string(postedBody))
	}
	// The two nodes' wiring is the literal acceptance: the
	// `wires` field of n1 includes n2. Decode and check.
	var posted map[string]any
	if err := json.Unmarshal(postedBody, &posted); err != nil {
		t.Fatalf("posted body is not JSON: %v", err)
	}
	nodes, _ := posted["nodes"].([]any)
	if len(nodes) < 2 {
		t.Fatalf("posted body has %d nodes, want 2", len(nodes))
	}
	var n1 map[string]any
	for _, n := range nodes {
		if m, ok := n.(map[string]any); ok && m["id"] == "n1" {
			n1 = m
			break
		}
	}
	if n1 == nil {
		t.Fatal("posted body has no node n1")
	}
	wires, _ := n1["wires"].([]any)
	if len(wires) == 0 {
		t.Fatal("n1 has no wires")
	}
	first, _ := wires[0].([]any)
	if len(first) == 0 || first[0] != "n2" {
		t.Errorf("n1.wires[0] = %v, want [n2]", wires[0])
	}
}

// stripCodeFence removes the ```json ... ``` wrapper the tool adds
// for readability. Tests that need the raw JSON call this and parse
// the result themselves.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Drop the opening fence (with optional language tag) and
		// the closing fence.
		end := strings.Index(s, "\n")
		if end >= 0 {
			s = s[end+1:]
		}
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
	}
	return strings.TrimSpace(s)
}
