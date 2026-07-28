package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// TestExportFlow_HappyPath: export_flow returns the same bytes the
// editor's Ctrl+C produces — a single-element array containing the
// tab document, pretty-printed.
func TestExportFlow_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/flow/tab1" {
			t.Errorf("expected /flow/tab1, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(sampleTab))
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	s := New(c, "test", false, false)

	res, err := s.handleExportFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"id": "tab1"}},
	})
	if err != nil {
		t.Fatalf("handleExportFlow: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	body := stripCodeFence(tc.Text)
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(body), &arr); err != nil {
		t.Fatalf("response is not a JSON array: %v\nbody: %s", err, body)
	}
	if len(arr) != 1 {
		t.Fatalf("clipboard has %d elements, want 1", len(arr))
	}
	// The single element must carry the tab's id and label.
	var tab map[string]any
	if err := json.Unmarshal(arr[0], &tab); err != nil {
		t.Fatalf("clipboard element is not an object: %v", err)
	}
	if tab["id"] != "tab1" {
		t.Errorf("clipboard element id = %v, want tab1", tab["id"])
	}
	if tab["label"] != "Home" {
		t.Errorf("clipboard element label = %v, want Home", tab["label"])
	}
}

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
	s := New(c, "test", false, false)

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

// TestExportImportRoundTrip: the issue's acceptance criterion —
// export → import → export must return the same bytes (modulo
// runtime-assigned id differences).
//
// The test runs against a single httptest server that holds the
// in-memory flow state. First export pulls tab1; import_flow creates
// a new tab with a runtime-assigned id; second export pulls that
// new tab; the bodies must match after the id is normalised.
func TestExportImportRoundTrip(t *testing.T) {
	var (
		mu      sync.Mutex
		flows   = map[string]string{"tab1": sampleTab}
		counter = 0
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/tab1":
			_, _ = w.Write([]byte(flows["tab1"]))
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/flow/"):
			id := strings.TrimPrefix(r.URL.Path, "/flow/")
			body, ok := flows[id]
			if !ok {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(body))
		case r.Method == "POST" && r.URL.Path == "/flow":
			counter++
			newID := fmt.Sprintf("runtime-%d", counter)
			body := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(body)
			flows[newID] = string(body)
			_, _ = w.Write([]byte(fmt.Sprintf(`{"id":%q}`, newID)))
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`{"rev":"x","flows":[]}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	s := New(c, "test", false, false)

	// First export.
	res1, err := s.handleExportFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"id": "tab1"}},
	})
	if err != nil {
		t.Fatalf("first export: %v", err)
	}
	first := stripCodeFence(res1.Content[0].(mcp.TextContent).Text)

	// Import the exported bytes.
	importRes, err := s.handleImportFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"clipboard": first}},
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	importText := importRes.Content[0].(mcp.TextContent).Text
	// The success message contains "New id: \"<id>\"".
	importedID := extractQuotedID(t, importText)

	// Second export, against the runtime-assigned id.
	res2, err := s.handleExportFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"id": importedID}},
	})
	if err != nil {
		t.Fatalf("second export: %v", err)
	}
	second := stripCodeFence(res2.Content[0].(mcp.TextContent).Text)

	// The two exports differ only in the id field — the runtime
	// assigns its own. We normalise the id in `second` to the
	// `first`'s id and re-marshal so the comparison is structural,
	// not byte-for-byte (which can drift on key ordering).
	if !equalExceptID(t, first, second, importedID, "tab1") {
		t.Errorf("round-trip changed the tab body\nfirst:\n%s\n\nsecond:\n%s", first, second)
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
	s := New(c, "test", false, false)

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
	s := New(c, "test", false, false)

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
	s := New(c, "test", false, false)

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
	s := New(c, "test", false, false)

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
	s := New(c, "test", false, false)

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

// extractQuotedID pulls the "id" out of an import_flow success
// message: "Flow imported. New id: \"<id>\". ..."
func extractQuotedID(t *testing.T, msg string) string {
	t.Helper()
	i := strings.Index(msg, "New id:")
	if i < 0 {
		t.Fatalf("success message has no 'New id:' prefix: %q", msg)
	}
	rest := msg[i:]
	j := strings.Index(rest, "\"")
	if j < 0 {
		t.Fatalf("no opening quote after 'New id:': %q", msg)
	}
	rest = rest[j+1:]
	j = strings.Index(rest, "\"")
	if j < 0 {
		t.Fatalf("no closing quote: %q", msg)
	}
	return rest[:j]
}

// equalExceptID compares two clipboard bodies, normalising the
// `id` field on each top-level element. Returns true when the bodies
// match modulo that field — which is what the round-trip test
// wants, since the runtime rewrites ids.
func equalExceptID(t *testing.T, a, b, aID, bID string) bool {
	t.Helper()
	normalise := func(raw, fromID, toID string) string {
		var arr []map[string]any
		if err := json.Unmarshal([]byte(raw), &arr); err != nil {
			t.Fatalf("not a JSON array: %v", err)
		}
		if len(arr) != 1 {
			t.Fatalf("expected 1 element, got %d", len(arr))
		}
		arr[0]["id"] = toID
		out, _ := json.Marshal(arr)
		return string(out)
	}
	return normalise(a, aID, "tab1") == normalise(b, bID, "tab1")
}
