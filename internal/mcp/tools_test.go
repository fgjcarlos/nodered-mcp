package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// readOnlyTools is the exact set a server started with readOnly=true must
// expose. Anything that writes config, mutates the palette, or fires a node
// into a live runtime is deliberately absent — inject_node included: firing an
// inject can send a real command to a real device.
var readOnlyTools = []string{
	"list_flows",
	"search_flows",
	"get_diagnostics",
	"list_plugins",
	"get_context",
	"get_debug_messages",
	"get_flow",
	"list_nodes",
	"get_node_info",
	"search_nodes",
	"get_settings",
	"get_flows_state",
	"list_backups",
	"diff_flows",
}

// totalTools is the full-mode count: every read tool plus every mutating one.
const totalTools = 29

func newTestServer(t *testing.T, readOnly bool) *Server {
	t.Helper()
	c, err := nodered.NewClient(nodered.Options{BaseURL: "http://localhost:1880"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return New(c, "test", readOnly, false)
}

func toolNames(s *Server) map[string]bool {
	out := make(map[string]bool, len(s.tools))
	for _, tool := range s.tools {
		out[tool.Name] = true
	}
	return out
}

func TestReadOnlyExposesOnlyReadTools(t *testing.T) {
	got := toolNames(newTestServer(t, true))

	for _, want := range readOnlyTools {
		if !got[want] {
			t.Errorf("read-only server is missing read tool %q", want)
		}
	}
	if len(got) != len(readOnlyTools) {
		t.Errorf("read-only server exposed %d tools, want %d: %v", len(got), len(readOnlyTools), got)
	}
}

func TestReadOnlyWithholdsEveryMutatingTool(t *testing.T) {
	got := toolNames(newTestServer(t, true))

	// Explicit list rather than "everything not in readOnlyTools": if a new
	// mutating tool is added and forgotten, that derived form would pass while
	// this one keeps failing until the tool is classified on purpose.
	for _, banned := range []string{
		"create_flow", "update_flow", "delete_flow", "set_flows",
		"inject_node", "install_node", "uninstall_node",
		"enable_node", "disable_node", "set_flows_state", "restore_backup",
	} {
		if got[banned] {
			t.Errorf("read-only server exposed mutating tool %q", banned)
		}
	}
}

func TestFullServerExposesEveryTool(t *testing.T) {
	got := toolNames(newTestServer(t, false))

	if len(got) != totalTools {
		t.Errorf("full server exposed %d tools, want %d: %v", len(got), totalTools, got)
	}
	for _, want := range readOnlyTools {
		if !got[want] {
			t.Errorf("full server is missing read tool %q", want)
		}
	}
	for _, want := range []string{"create_flow", "set_flows", "restore_backup", "inject_node"} {
		if !got[want] {
			t.Errorf("full server is missing mutating tool %q", want)
		}
	}
}

// Registration state must live on the Server, not on the package. Two servers
// in one process (a test binary, or an http host serving both modes) would
// otherwise accumulate into the same slice and report each other's tools.
func TestServersDoNotShareRegistrationState(t *testing.T) {
	full := newTestServer(t, false)
	readOnly := newTestServer(t, true)

	if len(full.tools) != totalTools {
		t.Errorf("first server has %d tools after a second was built, want %d", len(full.tools), totalTools)
	}
	if len(readOnly.tools) != len(readOnlyTools) {
		t.Errorf("second server has %d tools, want %d", len(readOnly.tools), len(readOnlyTools))
	}
	if len(full.resources) != len(readOnly.resources) {
		t.Errorf("resource counts diverged: %d vs %d", len(full.resources), len(readOnly.resources))
	}
}

// Resources and prompts are read-only surfaces, so read-only mode keeps them.
func TestReadOnlyKeepsResourcesAndPrompts(t *testing.T) {
	s := newTestServer(t, true)

	if len(s.resources) != 3 {
		t.Errorf("read-only server exposed %d resources, want 3", len(s.resources))
	}
	if len(s.prompts) != 2 {
		t.Errorf("read-only server exposed %d prompts, want 2", len(s.prompts))
	}
}
func newTestServerDebugStream(t *testing.T, readOnly, debugStream bool) *Server {
	t.Helper()
	c, err := nodered.NewClient(nodered.Options{BaseURL: "http://localhost:1880"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return New(c, "test", readOnly, debugStream)
}

// When MCP_DEBUG_STREAM is off, get_debug_messages must answer with an
// actionable error pointing at the flag — not with the underlying
// "tail unavailable" message, which makes it sound broken.
func TestHandleGetDebugMessages_DisabledByDefault(t *testing.T) {
	s := newTestServerDebugStream(t, false, false)
	res, err := s.handleGetDebugMessages(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleGetDebugMessages returned an error: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected a result with an error message, got nil")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "debug streaming is disabled") {
		t.Errorf("expected the opt-in hint, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "MCP_DEBUG_STREAM") {
		t.Errorf("expected the flag name to be mentioned, got %q", tc.Text)
	}
}

// When MCP_DEBUG_STREAM is on but the tail URL is invalid, the existing
// "tail unavailable" message is the right answer — no behaviour change
// for the on-but-broken case.
func TestHandleGetDebugMessages_OnButUnavailable(t *testing.T) {
	s := newTestServerDebugStream(t, false, true)
	res, err := s.handleGetDebugMessages(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleGetDebugMessages returned an error: %v", err)
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if strings.Contains(tc.Text, "debug streaming is disabled") {
		t.Errorf("on-but-broken should not use the opt-in message, got %q", tc.Text)
	}
}

// TestNormalizeFlowDoc covers the auto-fill guard reported in Mavis's
// 2026-07-27 testing pass: a tab object without "nodes" must round-trip
// with an empty array rather than bouncing off the runtime as
// "missing nodes property". It also covers the flat-array shape update_flow
// accepts when the caller passes it the literal output of GET /flows.
func TestNormalizeFlowDoc(t *testing.T) {
	flat := `[
		{"type":"tab","id":"tabA","label":"A"},
		{"type":"inject","id":"i1","z":"tabA","x":140,"y":140,"wires":[[]]},
		{"type":"debug","id":"d1","z":"tabA","x":300,"y":140,"wires":[]},
		{"type":"tab","id":"tabB","label":"B"}
	]`
	tests := []struct {
		name     string
		in       string
		flowID   string
		fill     bool
		wantKeys []string
		wantHas  string
		wantErr  bool
	}{
		{"tab without nodes, fill on", `{"type":"tab","label":"t"}`, "", true, []string{"type", "label", "nodes"}, `"nodes":[]`, false},
		{"tab with nodes, fill on", `{"type":"tab","nodes":[{"id":"a"}]}`, "", true, []string{"type", "nodes"}, `"id":"a"`, false},
		{"update path, fill off", `{"type":"tab","label":"t"}`, "", false, []string{"type", "label"}, "", false},
		{"flat array to nested, fill off", flat, "tabA", false, []string{"id", "label", "nodes"}, `"id":"i1"`, false},
		{"flat array, wrong tab id", flat, "tabC", false, nil, "", true},
		{"flat array, fill on for create", flat, "tabA", true, []string{"id", "label", "nodes"}, `"id":"i1"`, false},
		{"non-object/array rejected", `42`, "", true, nil, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeFlowDoc(json.RawMessage(tc.in), tc.flowID, tc.fill)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// ponytail: round-trip via map keeps the test independent of key
			// ordering, which json.Marshal does not guarantee.
			var m map[string]json.RawMessage
			if err := json.Unmarshal(got, &m); err != nil {
				t.Fatalf("result not a JSON object: %v", err)
			}
			for _, k := range tc.wantKeys {
				if _, ok := m[k]; !ok {
					t.Errorf("expected key %q in normalized flow, got %s", k, got)
				}
			}
			if tc.wantHas != "" && !bytes.Contains(got, []byte(tc.wantHas)) {
				t.Errorf("expected %q in normalized flow, got %s", tc.wantHas, got)
			}
		})
	}
}

// TestNodeParam covers issue #25: add_node's `node` argument must accept
// either a JSON-encoded string or a node object directly. Anything else
// (number, boolean, array) is rejected with a single actionable message.
func TestNodeParam(t *testing.T) {
	tests := []struct {
		name    string
		args    map[string]any
		want    string // substring expected in the normalised JSON
		wantErr bool
	}{
		{
			name: "object argument",
			args: map[string]any{
				"node": map[string]any{"id": "n1", "type": "inject"},
			},
			want: `"id":"n1"`,
		},
		{
			name: "string argument",
			args: map[string]any{
				"node": `{"id":"n1","type":"inject"}`,
			},
			want: `"id":"n1"`,
		},
		{
			name:    "missing key",
			args:    map[string]any{},
			wantErr: true,
		},
		{
			name:    "wrong type (number)",
			args:    map[string]any{"node": 42},
			wantErr: true,
		},
		{
			name:    "invalid JSON string",
			args:    map[string]any{"node": "{not json"},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := nodeParam(mcp.CallToolRequest{
				Params: mcp.CallToolParams{Arguments: tc.args},
			}, "node")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", string(got))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Contains(got, []byte(tc.want)) {
				t.Errorf("expected %q in normalised node, got %s", tc.want, got)
			}
		})
	}
}

// TestNormalizeFlowDoc_FlatArray covers issue #35 end-to-end through the
// client: feeding the literal output of GET /flows into update_flow and
// expecting it to land at Node-RED as a nested tab doc with the right
// nodes/configs split.
func TestNormalizeFlowDoc_FlatArray(t *testing.T) {
	var putBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			// Snapshot for backup.
			_, _ = w.Write([]byte(`[
				{"type":"tab","id":"tabA","label":"A","nodes":[]}
			]`))
		case r.Method == "PUT" && r.URL.Path == "/flow/tabA":
			putBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	// The caller shape: a literal flat array (the GET /flows response shape)
	// with one tab and one canvas-positioned inject.
	flat := []byte(`[
		{"type":"tab","id":"tabA","label":"A"},
		{"type":"inject","id":"i1","z":"tabA","x":140,"y":140,"wires":[]}
	]`)

	// Run the flat payload through the same path the MCP handler does:
	// flowParam is a string/object helper we cannot exercise here, but
	// normalizeFlowDoc takes the bytes and does the shape work.
	normalized, err := normalizeFlowDoc(flat, "tabA", false)
	if err != nil {
		t.Fatalf("normalizeFlowDoc: %v", err)
	}
	if err := c.UpdateFlow(context.Background(), "tabA", normalized); err != nil {
		t.Fatalf("UpdateFlow: %v", err)
	}

	// The PUT must carry a nested shape: top-level "label" (not "type":"tab"),
	// a nodes array containing i1.
	if !bytes.Contains(putBody, []byte(`"label":"A"`)) {
		t.Errorf("expected nested doc with label, got %s", putBody)
	}
	if !bytes.Contains(putBody, []byte(`"id":"i1"`)) {
		t.Errorf("expected nodes[].id=i1, got %s", putBody)
	}
	if bytes.Contains(putBody, []byte(`"type":"tab"`)) {
		t.Errorf("expected the type:tab marker to be dropped, got %s", putBody)
	}
}
