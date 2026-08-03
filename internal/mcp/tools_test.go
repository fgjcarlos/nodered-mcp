package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	"export_flow",
	"get_runtime_logs",
	"get_node_status",
	"validate_flow",
	"list_subflows",
	"get_subflow",
}

// totalTools is the full-mode count: every read tool plus every mutating one.
// Includes the validate_flow read tool (#56 batch) plus the six subflow tools
// (list_subflows + get_subflow as reads, the rest as mutating).
const totalTools = 43

func newTestServer(t *testing.T, readOnly bool) *Server {
	t.Helper()
	c, err := nodered.NewClient(nodered.Options{BaseURL: "http://localhost:1880"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return New(c, Options{Version: "test", ReadOnly: readOnly})
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
		"disable_flow", "enable_flow", "set_context",
		"create_subflow", "update_subflow", "delete_subflow", "instantiate_subflow",
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
	for _, want := range []string{"create_flow", "set_flows", "restore_backup", "inject_node", "set_context", "disable_flow", "enable_flow", "create_subflow", "update_subflow", "delete_subflow", "instantiate_subflow"} {
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
	return New(c, Options{Version: "test", ReadOnly: readOnly, DebugStream: debugStream})
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

// TestHandleValidateFlow_FlatArrayInput closes issue #413: validate_flow
// used to call ValidateFlow on the raw bytes, so a flat-array payload
// (the same shape create_flow / update_flow accept) was rejected with
// invalid_document. The handler now routes through ValidateFlows, which
// iterates the tabs in a flat array and validates each one. A clean
// flat-array payload must come back with zero issues and no
// invalid_document mention.
func TestHandleValidateFlow_FlatArrayInput(t *testing.T) {
	s := newTestServer(t, false)

	flat := []any{
		map[string]any{
			"type":  "tab",
			"id":    "t1",
			"label": "X",
		},
		map[string]any{
			"id":    "n1",
			"type":  "inject",
			"z":     "t1",
			"x":     140,
			"y":     140,
			"wires": []any{},
		},
	}

	res, err := s.handleValidateFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"flow": flat}},
	})
	if err != nil {
		t.Fatalf("handleValidateFlow returned an error: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected a result with content, got nil")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if strings.Contains(tc.Text, "invalid_document") {
		t.Errorf("validate_flow must accept the flat-array shape, got: %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "ok") {
		t.Errorf("expected validate_flow to report ok on a clean flat array, got: %q", tc.Text)
	}
}

// TestHandleValidateFlow_StillRejectsGenuinelyInvalidPayload guards
// against the "now we always pass" regression: a tab object whose
// "nodes" field is not an array must still surface as invalid_document,
// because the validator cannot make sense of it.
func TestHandleValidateFlow_StillRejectsGenuinelyInvalidPayload(t *testing.T) {
	s := newTestServer(t, false)

	bad := map[string]any{
		"id":    "t1",
		"label": "X",
		"nodes": "not-an-array",
	}

	res, err := s.handleValidateFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"flow": bad}},
	})
	if err != nil {
		t.Fatalf("handleValidateFlow returned an error: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected a result with content, got nil")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "invalid_document") {
		t.Errorf("expected validate_flow to report invalid_document for a malformed payload, got: %q", tc.Text)
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

// TestNormalizeFlowDoc_NodesNullSubstituted covers issue #96: a flow
// payload that explicitly serializes "nodes":null (or "configs":null)
// must be coalesced into an empty array the same way a missing key is.
// Node-RED's runtime rejects a null with an opaque "Cannot read
// properties of null" error, so leaving the literal null through the
// auto-fill block was a user-facing bug.
func TestNormalizeFlowDoc_NodesNullSubstituted(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		flowID      string
		fill        bool
		wantNodes   string // substring expected in normalized flow ("" = key absent)
		wantConfigs string // substring expected in normalized flow ("" = key absent)
	}{
		{
			name:      "nodes:null coalesced to []",
			in:        `{"label":"x","nodes":null}`,
			flowID:    "",
			fill:      true,
			wantNodes: `"nodes":[]`,
		},
		{
			name:        "nodes:null and configs:null both coalesced",
			in:          `{"label":"x","nodes":null,"configs":null}`,
			flowID:      "",
			fill:        true,
			wantNodes:   `"nodes":[]`,
			wantConfigs: `"configs":[]`,
		},
		{
			name:        "absent nodes and configs both filled",
			in:          `{"label":"x"}`,
			flowID:      "",
			fill:        true,
			wantNodes:   `"nodes":[]`,
			wantConfigs: `"configs":[]`,
		},
		{
			name:      "real nodes array preserved, absent configs left absent",
			in:        `{"label":"x","nodes":[{"id":"n1"}]}`,
			flowID:    "",
			fill:      true,
			wantNodes: `"id":"n1"`,
		},
		{
			name:      "fillNodes=false leaves null alone",
			in:        `{"label":"x","nodes":null}`,
			flowID:    "",
			fill:      false,
			wantNodes: `"nodes":null`,
		},
		{
			name:      "fillNodes=false leaves absent keys absent",
			in:        `{"label":"x"}`,
			flowID:    "",
			fill:      false,
			wantNodes: "", // no nodes key in output
		},
		{
			name:        "configs:null alone is coalesced",
			in:          `{"label":"x","nodes":[{"id":"n1"}],"configs":null}`,
			flowID:      "",
			fill:        true,
			wantNodes:   `"id":"n1"`,
			wantConfigs: `"configs":[]`,
		},
		{
			name:        "present configs array left intact",
			in:          `{"label":"x","nodes":[],"configs":[{"id":"c1"}]}`,
			flowID:      "",
			fill:        true,
			wantNodes:   `"nodes":[]`,
			wantConfigs: `"id":"c1"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeFlowDoc(json.RawMessage(tc.in), tc.flowID, tc.fill)
			if err != nil {
				t.Fatalf("normalizeFlowDoc: %v", err)
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal(got, &m); err != nil {
				t.Fatalf("result not a JSON object: %v\nbody: %s", err, got)
			}
			assertSubstring(t, "nodes", string(got), tc.wantNodes, m)
			assertSubstring(t, "configs", string(got), tc.wantConfigs, m)
		})
	}
}

// assertSubstring validates a normalized flow document field. When
// want is empty the key must be absent from the parsed object; when
// want is set it must appear as a substring of the marshaled body.
func assertSubstring(t *testing.T, key, body, want string, m map[string]json.RawMessage) {
	t.Helper()
	if want == "" {
		if _, ok := m[key]; ok {
			t.Errorf("expected key %q to be absent, got %s", key, body)
		}
		return
	}
	if _, ok := m[key]; !ok {
		t.Errorf("expected key %q in normalized flow, got %s", key, body)
	}
	if !bytes.Contains([]byte(body), []byte(want)) {
		t.Errorf("expected %q in normalized flow, got %s", want, body)
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

// TestSetContext_HappyPathAndHelperReuse covers issue #52's two
// acceptance criteria together:
//
//  1. set_context scope=global key=foo value=42 is observable in
//     a subsequent get_context scope=global key=foo (here, in the
//     captured request, since the helper runs in a real runtime and
//     we do not have one in the unit test).
//  2. No new persistent nodes are left between calls — the helper
//     is reused, not re-created per invocation.
//
// The httptest mock records the sequence of calls AND keeps a
// minimal in-memory "live flow" so reads after writes reflect the
// current state (mirrors what a real Node-RED does). On the first
// set_context, we expect a flow creation + two add_node calls + a
// connect_nodes + the inject dispatch. On the second set_context
// (same scope/key/value), we expect exactly one call: the inject
// dispatch. Anything else is a regression.
func TestSetContext_HappyPathAndHelperReuse(t *testing.T) {
	var (
		mu       sync.Mutex
		postFlow int
		putFlow  int
		postInj  int
		injected [][]byte
		// liveFlow is the in-memory representation of the helper flow
		// on the (mocked) Node-RED instance. It starts as an empty
		// flow with just the tab and grows with each PUT the mock
		// sees. GET /flow/:id returns the current snapshot so the
		// read-modify-write AddNode / ConnectNodes path sees the
		// right state.
		liveFlow = map[string]any{
			"id":    "mcp_ctx_helper_tab",
			"label": "__mcp_context_helper__",
			"nodes": []any{},
		}
	)
	// refreshSnapshot rebuilds the doc the mock serves for
	// GET /flow/mcp_ctx_helper_tab from the current liveFlow.
	refreshSnapshot := func() []byte {
		out, _ := json.Marshal(liveFlow)
		return out
	}
	// ingestPUT applies a PUT /flow/mcp_ctx_helper_tab body to
	// liveFlow. It preserves the tab id/label and overwrites the
	// nodes array with whatever the body said. That is enough for
	// the wire-validation step and for the next read to see the
	// just-installed nodes.
	ingestPUT := func(body []byte) {
		var doc struct {
			ID    string           `json:"id"`
			Label string           `json:"label"`
			Nodes []map[string]any `json:"nodes"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			return
		}
		liveFlow["id"] = doc.ID
		liveFlow["label"] = doc.Label
		liveFlow["nodes"] = doc.Nodes
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			// Backup snapshot. Two-flow array so the snapshot file
			// looks like a real config, with the helper tab
			// alongside one unrelated tab.
			_, _ = w.Write([]byte(fmt.Sprintf(
				`[{"type":"tab","id":"other","label":"Other","nodes":[]},%s]`,
				refreshSnapshot(),
			)))
		case r.Method == "POST" && r.URL.Path == "/flow":
			// CreateFlow for the helper tab.
			postFlow++
			body, _ := io.ReadAll(r.Body)
			if !bytes.Contains(body, []byte(`"label":"__mcp_context_helper__"`)) {
				t.Errorf("CreateFlow: expected the helper label, got %s", body)
			}
			if !bytes.Contains(body, []byte(`"id":"mcp_ctx_helper_tab"`)) {
				t.Errorf("CreateFlow: expected the helper flow id, got %s", body)
			}
			// Echo the body so the post-CreateFlow response has a
			// valid nested shape.
			_, _ = w.Write(body)
		case r.Method == "GET" && r.URL.Path == "/flow/mcp_ctx_helper_tab":
			// GetFlow, called by AddNode (read-modify-write) and
			// also by the fallback path on /flows synthesis. The
			// snapshot reflects whatever PUT last wrote.
			_, _ = w.Write(refreshSnapshot())
		case r.Method == "PUT" && r.URL.Path == "/flow/mcp_ctx_helper_tab":
			// Every AddNode + ConnectNodes goes through PUT /flow/:id.
			// The mock captures the body so the next GET reflects
			// the just-installed nodes.
			putFlow++
			body, _ := io.ReadAll(r.Body)
			ingestPUT(body)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/inject/"):
			postInj++
			body, _ := io.ReadAll(r.Body)
			injected = append(injected, body)
			if !bytes.Contains(body, []byte(`"__user_inject_props__":[]`)) {
				t.Errorf("inject body missing the user-props trigger: %s", body)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	srv2 := New(c, Options{Version: "test"})

	makeReq := func(value string) mcp.CallToolRequest {
		return mcp.CallToolRequest{
			Params: mcp.CallToolParams{Arguments: map[string]any{
				"scope": "global",
				"key":   "foo",
				"value": value,
			}},
		}
	}

	// First call: provision + dispatch.
	if _, err := srv2.handleSetContext(context.Background(), makeReq("42")); err != nil {
		t.Fatalf("first set_context returned err=%v", err)
	}
	// Second call: reuse, no extra provisioning.
	if _, err := srv2.handleSetContext(context.Background(), makeReq("99")); err != nil {
		t.Fatalf("second set_context returned err=%v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if postFlow != 1 {
		t.Errorf("expected exactly 1 CreateFlow across 2 calls, got %d (helper should be reused)", postFlow)
	}
	if postInj != 2 {
		t.Errorf("expected 2 inject dispatches, got %d", postInj)
	}
	// The inject dispatch count == putFlow count only if AddNode and
	// ConnectNodes each went through PUT /flow/:id exactly once. We
	// provision 2 nodes (inject + function) and 1 wire, so 3 PUTs
	// total — the magic number, not a coincidence. If it changes the
	// test still catches it; the "exactly 3" assertion is the
	// anti-regression for the helper being created in one shot
	// rather than incrementally.
	if putFlow != 3 {
		t.Errorf("expected 3 PUT /flow/:id calls (2 add_node + 1 connect_nodes), got %d", putFlow)
	}
	// Bodies must carry the value the caller asked for, not the
	// value from the first call. Two separate dispatches with two
	// different values is the literal acceptance criterion.
	if len(injected) != 2 {
		t.Fatalf("expected 2 captured inject bodies, got %d", len(injected))
	}
	if !bytes.Contains(injected[0], []byte(`"value":42`)) {
		t.Errorf("first inject body missing value:42: %s", injected[0])
	}
	if !bytes.Contains(injected[1], []byte(`"value":99`)) {
		t.Errorf("second inject body missing value:99: %s", injected[1])
	}
}

func TestRestoreBackup_ClearsSetContextHelper(t *testing.T) {
	backupDir := t.TempDir()
	liveFlow := []byte(`{"id":"runtime_helper_tab","label":"__mcp_context_helper__","nodes":[]}`)
	var restoredBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[{"type":"tab","id":"other","label":"Other","nodes":[]}]`))
		case r.Method == "POST" && r.URL.Path == "/flow":
			liveFlow = []byte(`{"id":"runtime_helper_tab","label":"__mcp_context_helper__","nodes":[]}`)
			_, _ = w.Write(liveFlow)
		case r.Method == "GET" && r.URL.Path == "/flow/runtime_helper_tab":
			_, _ = w.Write(liveFlow)
		case r.Method == "PUT" && r.URL.Path == "/flow/runtime_helper_tab":
			var err error
			liveFlow, err = io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("reading flow update: %v", err)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "POST" && r.URL.Path == "/flows":
			var err error
			restoredBody, err = io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("reading restore body: %v", err)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/inject/"):
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: backupDir})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	s := New(c, Options{Version: "test"})

	setResult, err := s.handleSetContext(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"scope": "global",
			"key":   "foo",
			"value": "1",
		}},
	})
	if err != nil {
		t.Fatalf("handleSetContext returned err=%v", err)
	}
	if setResult == nil || setResult.IsError {
		t.Fatalf("set_context failed: %+v", setResult)
	}
	if s.ctxHelper == nil || s.ctxHelper.flowID == "" {
		t.Fatalf("set_context did not provision a helper: %+v", s.ctxHelper)
	}

	backupName := "flows-restore.json"
	if err := os.WriteFile(filepath.Join(backupDir, backupName), []byte(`[{"type":"tab","id":"restored","label":"Restored","nodes":[]}]`), 0o600); err != nil {
		t.Fatalf("write restore backup: %v", err)
	}

	restoreResult, err := s.handleRestoreBackup(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"backup": backupName}},
	})
	if err != nil {
		t.Fatalf("handleRestoreBackup returned err=%v", err)
	}
	if restoreResult == nil || restoreResult.IsError {
		t.Fatalf("restore_backup failed: %+v", restoreResult)
	}
	if s.ctxHelper != nil {
		t.Fatalf("restore_backup retained stale set_context helper: %+v", s.ctxHelper)
	}
	if !bytes.Contains(restoredBody, []byte(`"id":"restored"`)) {
		t.Errorf("restore_backup did not deploy the selected backup: %s", restoredBody)
	}
}

// TestSetContext_RejectsBadScope ensures the scope enum is enforced
// at the handler, even though the schema already says it. (The schema
// is the primary guard; the handler check is belt-and-braces —
// callers reaching the handler directly, e.g. the unit tests, must
// still get a typed error.)
func TestSetContext_RejectsBadScope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be called for an invalid scope, got %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	srv2 := New(c, Options{Version: "test"})

	res, err := srv2.handleSetContext(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"scope": "everywhere",
			"key":   "foo",
			"value": "1",
		}},
	})
	if err != nil {
		t.Fatalf("handleSetContext returned err=%v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result, got %+v", res)
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "scope must be") {
		t.Errorf("error should explain the scope rule, got %q", tc.Text)
	}
}

// TestSetContext_RejectsBadJSONValue mirrors get_context's "value
// must be parseable" rule. A value that is not valid JSON is a
// caller mistake, not a runtime condition — fail fast before the
// helper is even touched.
func TestSetContext_RejectsBadJSONValue(t *testing.T) {
	var serverCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalled = true
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	srv2 := New(c, Options{Version: "test"})

	res, err := srv2.handleSetContext(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"scope": "global",
			"key":   "foo",
			"value": "{not json",
		}},
	})
	if err != nil {
		t.Fatalf("handleSetContext returned err=%v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result, got %+v", res)
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "valid JSON") {
		t.Errorf("error should mention the JSON parse step, got %q", tc.Text)
	}
	if serverCalled {
		t.Error("server must not be called when the value fails to parse")
	}
}

// TestSetContext_RequiresIdForFlow covers the same rule get_context
// applies: scope=flow without an id is undefined. We reject
// proactively (before provisioning) so a confused caller does not
// also pay the cost of installing a helper.
func TestSetContext_RequiresIdForFlow(t *testing.T) {
	var serverCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalled = true
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	srv2 := New(c, Options{Version: "test"})
	srv2.ctxHelper = &setContextHelper{
		flowID:     "runtime_helper_tab",
		injectID:   setContextHelperInjectID,
		functionID: setContextHelperFunctionID,
	}

	res, err := srv2.handleSetContext(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"scope": "flow",
			"key":   "foo",
			"value": "1",
		}},
	})
	if err != nil {
		t.Fatalf("handleSetContext returned err=%v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result, got %+v", res)
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "requires an id") {
		t.Errorf("error should explain the id rule, got %q", tc.Text)
	}
	if serverCalled {
		t.Error("server must not be called when the id is missing")
	}
}

// TestSetContext_FlowScopeRejectsForeignId makes the limitation
// explicit: scope=flow can ONLY target the helper's own flow
// context, because a function node has no runtime API to reach
// another tab's flow context. The error message names the right id
// so the caller can fix the call without reading the source.
func TestSetContext_FlowScopeRejectsForeignId(t *testing.T) {
	var serverCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalled = true
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	srv2 := New(c, Options{Version: "test"})
	srv2.ctxHelper = &setContextHelper{
		flowID:     "runtime_helper_tab",
		injectID:   setContextHelperInjectID,
		functionID: setContextHelperFunctionID,
	}

	res, err := srv2.handleSetContext(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"scope": "flow",
			"id":    "some_other_tab",
			"key":   "foo",
			"value": "1",
		}},
	})
	if err != nil {
		t.Fatalf("handleSetContext returned err=%v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result, got %+v", res)
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "helper flow id") {
		t.Errorf("error should explain the only legal id, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "some_other_tab") {
		t.Errorf("error should echo the bad id back, got %q", tc.Text)
	}
	if serverCalled {
		t.Error("server must not be called for a foreign flow id")
	}
}

func TestSetContext_AcceptsRealRuntimeId(t *testing.T) {
	var injects int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/inject/"+setContextHelperInjectID {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			return
		}
		injects++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	s := New(c, Options{Version: "test"})
	s.ctxHelper = &setContextHelper{
		flowID:     "d23de851e7ed4098",
		injectID:   setContextHelperInjectID,
		functionID: setContextHelperFunctionID,
	}

	call := func(id string) *mcp.CallToolResult {
		res, err := s.handleSetContext(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Arguments: map[string]any{
				"scope": "flow",
				"id":    id,
				"key":   "foo",
				"value": "1",
			}},
		})
		if err != nil {
			t.Fatalf("handleSetContext returned err=%v", err)
		}
		return res
	}

	if res := call("d23de851e7ed4098"); res == nil || res.IsError {
		t.Fatalf("expected runtime id to succeed, got %+v", res)
	}
	if res := call(setContextHelperFlowID); res == nil || res.IsError {
		t.Fatalf("expected constant id alias to succeed, got %+v", res)
	}
	if res := call("some_other_tab"); res == nil || !res.IsError {
		t.Fatalf("expected foreign id to fail, got %+v", res)
	}
	if injects != 2 {
		t.Fatalf("expected two successful injects, got %d", injects)
	}
}

// TestSetContext_WithheldInReadOnly covers the read-only mode gate:
// set_context is a mutating tool (it creates a flow tab on the
// runtime on first use) and so must be absent from a read-only
// server's advertised surface, exactly like the other writers.
func TestSetContext_WithheldInReadOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be touched for tool enumeration, got %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	ro := New(c, Options{Version: "test", ReadOnly: true})

	names := toolNames(ro)
	if names["set_context"] {
		t.Error("read-only server must not advertise set_context")
	}
}

// TestGetRuntimeLogs_NotFoundSurfacesActionableHint covers the most
// likely real case: stock Node-RED 5.x has no /logs endpoint, so
// the handler must translate a 404 into something an operator
// can act on rather than the bare "Cannot GET /logs" string the
// admin API gives.
func TestGetRuntimeLogs_NotFoundSurfacesActionableHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/logs" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		http.Error(w, "Cannot GET /logs", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	s := New(c, Options{Version: "test"})

	res, err := s.handleGetRuntimeLogs(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleGetRuntimeLogs returned err=%v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result, got %+v", res)
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "/logs") {
		t.Errorf("the error should mention the endpoint, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "stdout") && !strings.Contains(tc.Text, "log") {
		t.Errorf("the error should mention the stdout/log fallback, got %q", tc.Text)
	}
}

// TestGetRuntimeLogs_ReturnsLines covers the happy path: the
// endpoint returns an envelope and the tool renders the lines
// newest-last, with the level normalised to {info,warn,error}.
func TestGetRuntimeLogs_ReturnsLines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/logs" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("count"); got == "" {
			t.Errorf("expected a count query, got nothing")
		}
		_, _ = w.Write([]byte(`{"logs":[
			{"ts":"2026-07-28T10:00:00Z","level":"info","msg":"started"},
			{"ts":"2026-07-28T10:00:01Z","level":"error","msg":"boom"}
		]}`))
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	s := New(c, Options{Version: "test"})

	res, err := s.handleGetRuntimeLogs(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleGetRuntimeLogs returned err=%v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("expected a non-error result, got %+v", res)
	}
	tc := res.Content[0].(mcp.TextContent)
	for _, want := range []string{"started", "boom", "ERROR", "INFO"} {
		if !strings.Contains(tc.Text, want) {
			t.Errorf("expected %q in the output, got:\n%s", want, tc.Text)
		}
	}
}

// TestGetRuntimeLogs_FilterByLevel covers the level filter: a
// caller asking for errors must not get info lines back, even when
// the runtime returns both.
func TestGetRuntimeLogs_FilterByLevel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"logs":[
			{"ts":"2026-07-28T10:00:00Z","level":"info","msg":"ok"},
			{"ts":"2026-07-28T10:00:01Z","level":"error","msg":"boom"}
		]}`))
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	s := New(c, Options{Version: "test"})

	res, _ := s.handleGetRuntimeLogs(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"level": "error"}},
	})
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "boom") {
		t.Errorf("error line should be present, got:\n%s", tc.Text)
	}
	if strings.Contains(tc.Text, "INFO") || strings.Contains(tc.Text, "ok\n") {
		t.Errorf("info line should be filtered out, got:\n%s", tc.Text)
	}
}

// TestGetRuntimeLogs_RejectsBadLevel covers the schema's
// belt-and-braces handler check: an unknown level is rejected
// without hitting the runtime.
func TestGetRuntimeLogs_RejectsBadLevel(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	s := New(c, Options{Version: "test"})

	res, _ := s.handleGetRuntimeLogs(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"level": "trace"}},
	})
	if res == nil || !res.IsError {
		t.Fatalf("expected an error, got %+v", res)
	}
	if called {
		t.Error("a bad level must not reach the runtime")
	}
}

// TestGetRuntimeLogs_LineOffset covers the "-N" form of since:
// the caller asks for the last N lines and we trim to that
// window. The runtime may return more; we trim.
func TestGetRuntimeLogs_LineOffset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"logs":[
			{"ts":"2026-07-28T10:00:00Z","level":"info","msg":"a"},
			{"ts":"2026-07-28T10:00:01Z","level":"info","msg":"b"},
			{"ts":"2026-07-28T10:00:02Z","level":"info","msg":"c"},
			{"ts":"2026-07-28T10:00:03Z","level":"info","msg":"d"}
		]}`))
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	s := New(c, Options{Version: "test"})

	res, _ := s.handleGetRuntimeLogs(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"since": "-2"}},
	})
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "c") || !strings.Contains(tc.Text, "d") {
		t.Errorf("expected the last 2 lines, got:\n%s", tc.Text)
	}
	if strings.Contains(tc.Text, " a\n") || strings.Contains(tc.Text, " b\n") {
		t.Errorf("earlier lines should be trimmed, got:\n%s", tc.Text)
	}
}

// TestGetRuntimeLogs_RejectsBadSince covers the same input
// validation: a since string that is neither a timestamp nor a
// line offset is rejected up-front.
func TestGetRuntimeLogs_RejectsBadSince(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be called for a bad since, got %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	s := New(c, Options{Version: "test"})

	for _, bad := range []string{"not-a-date", "-abc", "0", "-0", "yesterday"} {
		t.Run(bad, func(t *testing.T) {
			res, _ := s.handleGetRuntimeLogs(context.Background(), mcp.CallToolRequest{
				Params: mcp.CallToolParams{Arguments: map[string]any{"since": bad}},
			})
			if res == nil || !res.IsError {
				t.Errorf("expected an error for since=%q, got %+v", bad, res)
			}
		})
	}
}

// TestGetNodeStatus_DisabledByDefault covers the MCP_DEBUG_STREAM
// gate: when the flag is off, the tool reports a clear opt-in
// hint, never empty data (which a model would read as "node is
// fine, no events to report").
func TestGetNodeStatus_DisabledByDefault(t *testing.T) {
	s := newTestServerDebugStream(t, false, false)
	res, err := s.handleGetNodeStatus(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"node_id": "n1"}},
	})
	if err != nil {
		t.Fatalf("handleGetNodeStatus returned err=%v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result, got %+v", res)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "MCP_DEBUG_STREAM") {
		t.Errorf("the error should name the opt-in flag, got %q", tc.Text)
	}
}

// TestGetNodeStatus_RequiresIdOrFlow covers the schema's
// belt-and-braces: pass one of node_id / flow_id, not both, not
// neither.
func TestGetNodeStatus_RequiresIdOrFlow(t *testing.T) {
	s := newTestServerDebugStream(t, false, true)

	t.Run("both", func(t *testing.T) {
		res, _ := s.handleGetNodeStatus(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Arguments: map[string]any{"node_id": "n1", "flow_id": "tabA"}},
		})
		if res == nil || !res.IsError {
			t.Errorf("expected an error for both, got %+v", res)
		}
	})
	t.Run("neither", func(t *testing.T) {
		res, _ := s.handleGetNodeStatus(context.Background(), mcp.CallToolRequest{})
		if res == nil || !res.IsError {
			t.Errorf("expected an error for neither, got %+v", res)
		}
	})
}

// TestGetNodeStatus_UnknownNode covers the audit's "never seen"
// case: a model asks about an id the cache has no record of.
// The reply must say "unknown", not "disconnected" — they mean
// different things to the operator.
func TestGetNodeStatus_UnknownNode(t *testing.T) {
	s := newTestServerDebugStream(t, false, true)

	res, _ := s.handleGetNodeStatus(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"node_id": "n_never_seen"}},
	})
	if res == nil || res.IsError {
		t.Fatalf("expected a non-error result, got %+v", res)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "unknown") {
		t.Errorf("expected 'unknown' in the reply, got %q", tc.Text)
	}
}

// TestGetNodeStatus_ConnectedNode covers the audit's headline
// acceptance criterion: a known connected node reports
// `connected: true` (here: status=connected).
func TestGetNodeStatus_ConnectedNode(t *testing.T) {
	s := newTestServerDebugStream(t, false, true)
	// Seed the cache directly. The tail is nil in this
	// test server (no real /comms), so we exercise the
	// render path through the same lookup the handler
	// uses; the production path is covered by the
	// nodered-package status tests.
	entry := nodered.StatusEntry{
		ID:     "n1",
		Status: nodered.StatusConnected,
		Text:   "ready",
		Fill:   "green", Shape: "dot",
	}
	// Inject through the tail's public record method via
	// the construction helper.
	tail := s.statusTail
	if tail == nil {
		// The test server does not construct a status tail
		// when debugStream is true with a localhost URL
		// (it cannot derive /comms for "http://localhost:1880").
		// We need a server that does, so build a real one.
		c, err := nodered.NewClient(nodered.Options{BaseURL: "http://127.0.0.1:1"})
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		tail, err = nodered.NewStatusTail(c)
		if err != nil {
			t.Fatalf("NewStatusTail: %v", err)
		}
	}
	// record() is package-private to nodered, so we go
	// through consume() with a wire-shaped frame.
	tail.ConsumeForTest([]byte(
		`[{"topic":"status/n1","data":{"text":"ready","fill":"green","shape":"dot"}}]`,
	))

	res, _ := s.handleGetNodeStatus(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"node_id": "n1"}},
	})
	if res == nil || res.IsError {
		t.Fatalf("expected a non-error result, got %+v", res)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "status=connected") {
		t.Errorf("expected status=connected in the reply, got %q", tc.Text)
	}
	_ = entry // referenced for documentation
}

// TestGetNodeStatus_ErroredNodeWithLastError covers the second
// half of the audit's acceptance criteria: a node that errored
// reports `connected: false` (status=errored) plus the last
// error text. We also assert that LastError survives a recovery
// to connected, so an operator who asks "why was this red?"
// after the node has come back to green still gets the answer.
func TestGetNodeStatus_ErroredNodeWithLastError(t *testing.T) {
	c, _ := nodered.NewClient(nodered.Options{BaseURL: "http://127.0.0.1:1"})
	tail, _ := nodered.NewStatusTail(c)
	tail.ConsumeForTest([]byte(
		`[{"topic":"status/n1","data":{"text":"broker unreachable","fill":"red","shape":"dot"}}]`,
	))
	tail.ConsumeForTest([]byte(
		`[{"topic":"status/n1","data":{"text":"ok","fill":"green","shape":"dot"}}]`,
	))

	// Build a server that uses this tail directly, so we
	// can assert on its output without going through a
	// real /comms dial.
	s := &Server{
		nrClient:    c,
		statusTail:  tail,
		debugStream: true,
	}

	res, _ := s.handleGetNodeStatus(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"node_id": "n1"}},
	})
	if res == nil || res.IsError {
		t.Fatalf("expected a non-error result, got %+v", res)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "status=connected") {
		t.Errorf("expected the recovered status=connected, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "broker unreachable") {
		t.Errorf("expected the last_error to survive recovery, got %q", tc.Text)
	}
}

// ---------------------------------------------------------------------------
// Issue #81: defense in depth against RCE via the Node-RED "exec" / "system"
// node types. The MCP write tools must refuse to deploy a flow that contains
// any node type in MCP_NODE_DENYLIST, before any side effect on Node-RED
// (backup, deploy) is triggered.
// ---------------------------------------------------------------------------

// serverWithDenylist builds a Server whose denylist contains exactly
// the given node types. Used by the issue #81 tests so each scenario
// controls the policy independently of the global config layer.
func serverWithDenylist(t *testing.T, types ...string) (*Server, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Snapshot-only: any handler reaching this point means the
		// denylist guard let the request through, which is what the
		// "positive" tests assert. The "negative" tests expect an
		// early error and so never arrive here.
		t.Errorf("denylist let an exec/system node through to the runtime: %s %s", r.Method, r.URL.Path)
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(srv.Close)

	c, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return New(c, Options{Version: "test", NodeDenylist: types}), srv
}

// TestCreateFlow_RejectsDeniedNodeType covers the headline acceptance
// criterion for issue #81: a write tool must refuse to deploy a flow
// containing an exec node when "exec" is in the denylist. The
// rejection has to fire BEFORE the runtime is contacted, so the
// httptest server's `t.Errorf` in serverWithDenylist would catch any
// leak — and it never fires, because the handler returns the typed
// error to the MCP layer first.
func TestCreateFlow_RejectsDeniedNodeType(t *testing.T) {
	s, _ := serverWithDenylist(t, "exec")

	res, err := s.handleCreateFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"flow": `{"label":"pwn","nodes":[{"id":"e1","type":"exec","z":"pwn","x":140,"y":140,"command":"id","wires":[]}]}`,
		}},
	})
	if err != nil {
		t.Fatalf("handleCreateFlow returned err=%v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result, got %+v", res)
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "MCP_NODE_DENYLIST") {
		t.Errorf("error must name MCP_NODE_DENYLIST so the operator can act, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, `"exec"`) {
		t.Errorf("error must echo the denied type back, got %q", tc.Text)
	}
}

// TestCreateFlow_AllowsNonDeniedNodeType is the positive case for the
// same denylist: an inject node is not exec/system, so the write goes
// through to the runtime. The httptest mock returns an empty array
// for GET /flows (the backup snapshot) and accepts the POST /flow.
func TestCreateFlow_AllowsNonDeniedNodeType(t *testing.T) {
	var posted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == "POST" && r.URL.Path == "/flow":
			posted = true
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"type":"inject"`) {
				t.Errorf("expected the inject node type to be forwarded, got %s", body)
			}
			_, _ = w.Write(body)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	s := New(c, Options{Version: "test", NodeDenylist: []string{"exec"}})

	res, err := s.handleCreateFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"flow": `{"label":"safe","nodes":[{"id":"i1","type":"inject","z":"safe","x":140,"y":140,"wires":[]}]}`,
		}},
	})
	if err != nil {
		t.Fatalf("handleCreateFlow returned err=%v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("expected a non-error result, got %+v", res)
	}
	if !posted {
		t.Error("non-denied node must reach the runtime POST /flow")
	}
}

// TestCreateFlow_EmptyDenylistAllowsExec is the explicit opt-out:
// MCP_NODE_DENYLIST="" translates to a Server built with no denylist,
// and the write goes through. This is the path operators use when
// they need exec/system intentionally.
func TestCreateFlow_EmptyDenylistAllowsExec(t *testing.T) {
	var posted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == "POST" && r.URL.Path == "/flow":
			posted = true
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"type":"exec"`) {
				t.Errorf("expected the exec node to be forwarded under empty denylist, got %s", body)
			}
			_, _ = w.Write(body)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	// Empty (non-nil) NodeDenylist = "opt out" — every type is allowed.
	s := New(c, Options{Version: "test", NodeDenylist: []string{}})

	res, err := s.handleCreateFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"flow": `{"label":"explicit","nodes":[{"id":"e1","type":"exec","z":"explicit","x":140,"y":140,"command":"id","wires":[]}]}`,
		}},
	})
	if err != nil {
		t.Fatalf("handleCreateFlow returned err=%v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("expected a non-error result under empty denylist, got %+v", res)
	}
	if !posted {
		t.Error("exec node must reach the runtime under empty denylist")
	}
}

// TestUpdateFlow_RejectsDeniedNodeType confirms the denylist is wired
// into update_flow too — the issue #81 plan lists all four write
// tools. Same shape as TestCreateFlow_RejectsDeniedNodeType, but
// through the PUT /flow/:id path.
func TestUpdateFlow_RejectsDeniedNodeType(t *testing.T) {
	s, _ := serverWithDenylist(t, "exec")

	res, err := s.handleUpdateFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"id":   "tabA",
			"flow": `{"label":"pwn","nodes":[{"id":"e1","type":"exec","z":"tabA","x":140,"y":140,"command":"id","wires":[]}]}`,
		}},
	})
	if err != nil {
		t.Fatalf("handleUpdateFlow returned err=%v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result, got %+v", res)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "MCP_NODE_DENYLIST") || !strings.Contains(tc.Text, `"exec"`) {
		t.Errorf("error must mention denylist + denied type, got %q", tc.Text)
	}
}

// TestAddNode_RejectsDeniedNodeType covers the third write tool: a
// single node payload (the add_node argument shape) is checked the
// same way a flow document is.
func TestAddNode_RejectsDeniedNodeType(t *testing.T) {
	s, _ := serverWithDenylist(t, "exec")

	res, err := s.handleAddNode(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"flow_id": "tabA",
			"node":    map[string]any{"id": "e1", "type": "exec", "z": "tabA", "x": 140, "y": 140, "command": "id", "wires": []any{}},
		}},
	})
	if err != nil {
		t.Fatalf("handleAddNode returned err=%v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result, got %+v", res)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "MCP_NODE_DENYLIST") || !strings.Contains(tc.Text, `"exec"`) {
		t.Errorf("error must mention denylist + denied type, got %q", tc.Text)
	}
}

// TestSetFlows_RejectsDeniedNodeType covers the full-deployment tool:
// the flows argument is a JSON array of flows, each of which may
// contain nodes. A single denied node in any flow rejects the entire
// call. This is the worst-case path because set_flows replaces the
// whole config.
func TestSetFlows_RejectsDeniedNodeType(t *testing.T) {
	s, _ := serverWithDenylist(t, "exec")

	res, err := s.handleSetFlows(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"flows": `[
				{"type":"tab","id":"tabA","label":"A"},
				{"type":"exec","id":"e1","z":"tabA","x":140,"y":140,"command":"id","wires":[]}
			]`,
		}},
	})
	if err != nil {
		t.Fatalf("handleSetFlows returned err=%v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result, got %+v", res)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "MCP_NODE_DENYLIST") || !strings.Contains(tc.Text, `"exec"`) {
		t.Errorf("error must mention denylist + denied type, got %q", tc.Text)
	}
}

// TestHandleSetFlows_NonTabOnlyRejected is the MCP-layer regression
// pin for issue #106: an array of orphan nodes (no tab entry) used to
// pass through normalizeFlowsArray and deploy, leaving the runtime
// with zero tabs. The handler must reject it before any Node-RED call.
func TestHandleSetFlows_NonTabOnlyRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be called for a no-tab flows array: %s %s", r.Method, r.URL.Path)
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(srv.Close)

	c, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	s := New(c, Options{Version: "test"})

	res, err := s.handleSetFlows(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"flows": `[
				{"id":"orphan","type":"inject","name":"o","topic":"t","payload":"p","payloadType":"str","repeat":"","crontab":"","once":false,"onceDelay":0.1,"x":140,"y":140,"wires":[]}
			]`,
		}},
	})
	if err != nil {
		t.Fatalf("handleSetFlows returned err=%v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result, got %+v", res)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, `"tab"`) {
		t.Errorf("error must mention tab requirement, got %q", tc.Text)
	}
}

// TestHandleValidateFlow_StringWiresReturnsIssue is the MCP-layer pin
// for issue #415: a model that hands a node with a string-typed wires
// field back to validate_flow used to get a misleading "0 issues" answer
// because the validator silently skipped the bad node. The handler must
// surface an issue that names the offending node and the JSON shape it
// actually saw, so the model can fix the document before retrying.
func TestHandleValidateFlow_StringWiresReturnsIssue(t *testing.T) {
	s := newTestServer(t, false)

	res, err := s.handleValidateFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"flow": `{"id":"tabA","label":"Home","nodes":[{"id":"n1","type":"inject","z":"tabA","x":140,"y":140,"wires":"not-an-array"}]}`,
		}},
	})
	if err != nil {
		t.Fatalf("handleValidateFlow returned err=%v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("expected a non-error result (issues are reported in-body, not as a tool error), got %+v", res)
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, `"n1"`) {
		t.Errorf("response must name the offending node id, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "invalid_wires") {
		t.Errorf("response must mention the issue kind, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "string") {
		t.Errorf("response must mention the actual JSON type encountered, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "1 issue") {
		t.Errorf("response must report a non-zero issue count, got %q", tc.Text)
	}
}

// TestHandleConnectNodes_SelfLoopRejected covers the MCP-layer guard added
// for issue #414: when from_id and to_id point at the same node, the
// handler must reject the call with a clear error before touching the
// runtime. The handler is invoked directly, so no httptest server is
// needed — the guard fires before any Node-RED call.
func TestHandleConnectNodes_SelfLoopRejected(t *testing.T) {
	s := newTestServer(t, false)

	res, err := s.handleConnectNodes(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"flow_id": "tabA",
			"from_id": "n1",
			"to_id":   "n1",
			"port":    float64(0),
		}},
	})
	if err != nil {
		t.Fatalf("handleConnectNodes returned err=%v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result for self-loop, got %+v", res)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "from_id and to_id must differ") {
		t.Errorf("error must explain the self-loop guard, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "infinite") {
		t.Errorf("error must mention the infinite loop, got %q", tc.Text)
	}
}

// TestHandleUpdateFlow_BadZRejected mirrors the underlying nodered guard
// through the MCP layer: a flow whose node carries z referencing a non-
// existent tab must be rejected by update_flow before any Node-RED call,
// so the wire the model intended is not silently lost on deploy (issue
// #99). The handler is invoked directly so no httptest server is needed
// for the negative case — the validator fires before the runtime is hit.
func TestHandleUpdateFlow_BadZRejected(t *testing.T) {
	s := newTestServer(t, false)

	res, err := s.handleUpdateFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"id": "tabA",
			"flow": `{
				"id":"tabA","label":"Home",
				"nodes":[{"id":"n1","type":"inject","z":"ghost","x":140,"y":140,"wires":[]}]
			}`,
		}},
	})
	if err != nil {
		t.Fatalf("handleUpdateFlow returned err=%v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result for bad z, got %+v", res)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, `z="ghost"`) {
		t.Errorf("error must name the bad z, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "owning tab") {
		t.Errorf("error must explain the z resolution rule, got %q", tc.Text)
	}
}

// TestHandleInjectNode_DisabledNodeRejected covers issue #104:
// the admin /inject/:id endpoint accepts a node with
// "disabled":true and returns success, but the runtime silently
// drops the message. inject_node must refuse the call with a typed
// error so the operator sees the actual cause instead of a phantom
// success.
func TestHandleInjectNode_DisabledNodeRejected(t *testing.T) {
	srv, _ := injectServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/flows" {
			// Node itself is disabled; tab is fine. The exact
			// POST request must not be made.
			_, _ = w.Write([]byte(`[
				{"id":"tab1","type":"tab","label":"Home"},
				{"id":"n1","type":"inject","z":"tab1","disabled":true}
			]`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	})

	res, err := srv.handleInjectNode(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"id": "n1"}},
	})
	if err != nil {
		t.Fatalf("handleInjectNode: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result for a disabled node, got %+v", res)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "n1") {
		t.Errorf("error must name the offending node id, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "disabled") {
		t.Errorf("error must mention the disabled flag, got %q", tc.Text)
	}
	// Crucially, must NOT say "fired" — that was the phantom
	// success bug #104 caught.
	if strings.Contains(tc.Text, "fired") {
		t.Errorf("error must not claim the node fired, got %q", tc.Text)
	}
}

// TestHandleInjectNode_DisabledTabRejected mirrors the previous
// case: the node itself is enabled, but the tab it lives in is
// disabled. The runtime accepts the inject and returns success
// while dropping the message — and the operator sees nothing
// downstream. Reject the call with the same typed-error treatment.
func TestHandleInjectNode_DisabledTabRejected(t *testing.T) {
	srv, _ := injectServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/flows" {
			// Tab is disabled, node is enabled. Same phantom
			// bug as the disabled-node case.
			_, _ = w.Write([]byte(`[
				{"id":"tab1","type":"tab","label":"Home","disabled":true},
				{"id":"n1","type":"inject","z":"tab1"}
			]`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	})

	res, err := srv.handleInjectNode(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"id": "n1"}},
	})
	if err != nil {
		t.Fatalf("handleInjectNode: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result for a node in a disabled tab, got %+v", res)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "tab") {
		t.Errorf("error must mention the disabled tab, got %q", tc.Text)
	}
	if strings.Contains(tc.Text, "fired") {
		t.Errorf("error must not claim the node fired, got %q", tc.Text)
	}
}

// TestHandleInjectNode_ActiveNodeFires is the regression case for
// the happy path: when the node and tab are both enabled, the new
// lookup must NOT block the call. POST /inject/:id must still be hit
// and the success text must still include "fired".
func TestHandleInjectNode_ActiveNodeFires(t *testing.T) {
	var posts []capture
	var srv *Server
	var got *capture
	srv, got = injectServer(t, func(w http.ResponseWriter, r *http.Request) {
		posts = append(posts, *got)
		if r.Method == http.MethodGet && r.URL.Path == "/flows" {
			_, _ = w.Write([]byte(`[
				{"id":"tab1","type":"tab","label":"Home"},
				{"id":"n1","type":"inject","z":"tab1"}
			]`))
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	res, err := srv.handleInjectNode(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"id": "n1"}},
	})
	if err != nil {
		t.Fatalf("handleInjectNode: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("expected success for an enabled inject in an enabled tab, got %+v", res)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "fired") {
		t.Errorf("expected success text to mention fired, got %q", tc.Text)
	}
	var sawPost bool
	for _, p := range posts {
		if p.method == "POST" && p.path == "/inject/n1" {
			sawPost = true
		}
	}
	if !sawPost {
		t.Errorf("expected POST /inject/n1, got %+v", posts)
	}
}

// TestHandleGetContext_NonExistentNodeId covers issue #105: when Node-RED
// returns {} for an unknown node id, get_context must surface a clear error
// rather than silently reporting "no context stored".
func TestHandleGetContext_NonExistentNodeId(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/context/"):
			// Node-RED returns HTTP 200 {} for unknown ids.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		case r.Method == "GET" && r.URL.Path == "/flows":
			// Flows list has no matching node.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"type":"tab","id":"tab1","label":"T"}]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	s := New(c, Options{Version: "test"})

	res, handlerErr := s.handleGetContext(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"scope": "node",
			"id":    "nonexistent-id",
		}},
	})
	if handlerErr != nil {
		t.Fatalf("handleGetContext returned unexpected Go error: %v", handlerErr)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result for unknown node id, got %+v", res)
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "not found") {
		t.Errorf("expected 'not found' in error, got %q", tc.Text)
	}
}

// TestHandleGetContext_ExistingNodeWithNoContext covers issue #105: when the
// node exists in the deployment but has no context stored, the handler must
// return the empty-context success message — not an error.
func TestHandleGetContext_ExistingNodeWithNoContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/context/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		case r.Method == "GET" && r.URL.Path == "/flows":
			// Flows list DOES contain the node.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[
				{"type":"tab","id":"tab1","label":"T"},
				{"type":"inject","id":"n1","z":"tab1","x":100,"y":100,"wires":[]}
			]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	s := New(c, Options{Version: "test"})

	res, handlerErr := s.handleGetContext(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"scope": "node",
			"id":    "n1",
		}},
	})
	if handlerErr != nil {
		t.Fatalf("handleGetContext returned unexpected Go error: %v", handlerErr)
	}
	if res == nil || res.IsError {
		t.Fatalf("expected success for existing node with no context, got %+v", res)
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "No context values") {
		t.Errorf("expected empty-context message, got %q", tc.Text)
	}
}

// buildFlowsJSON returns a GET /flows response whose non-tab nodes total n.
// One tab + n inject nodes.
func buildFlowsJSON(t *testing.T, n int) []byte {
	t.Helper()
	type node struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Z    string `json:"z,omitempty"`
	}
	nodes := make([]node, 0, n+1)
	nodes = append(nodes, node{Type: "tab", ID: "tab1"})
	for i := 0; i < n; i++ {
		nodes = append(nodes, node{Type: "inject", ID: fmt.Sprintf("n%d", i), Z: "tab1"})
	}
	b, err := json.Marshal(nodes)
	if err != nil {
		t.Fatalf("buildFlowsJSON: %v", err)
	}
	return b
}

func newListFlowsServer(t *testing.T, flows []byte, threshold int) *Server {
	t.Helper()
	nrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/flows" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(flows)
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(nrSrv.Close)
	c, err := nodered.NewClient(nodered.Options{BaseURL: nrSrv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return New(c, Options{Version: "test", ListFlowsFullThreshold: threshold})
}

// TestHandleListFlows_FullDetailAboveThresholdBlocked: >threshold nodes, no
// force → warning returned, not the full payload.
func TestHandleListFlows_FullDetailAboveThresholdBlocked(t *testing.T) {
	flows := buildFlowsJSON(t, 201)
	s := newListFlowsServer(t, flows, 200)

	res, err := s.handleListFlows(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"detail": "full",
		}},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected a result")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "may exhaust the model context") {
		t.Errorf("expected threshold warning, got %q", tc.Text)
	}
	if strings.Contains(tc.Text, "```json") {
		t.Errorf("should not return full JSON when blocked, got %q", tc.Text)
	}
}

// TestHandleListFlows_FullDetailAboveThresholdForced: >threshold nodes,
// force=true → full response returned.
func TestHandleListFlows_FullDetailAboveThresholdForced(t *testing.T) {
	flows := buildFlowsJSON(t, 201)
	s := newListFlowsServer(t, flows, 200)

	res, err := s.handleListFlows(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"detail": "full",
			"force":  true,
		}},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected a result")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if strings.Contains(tc.Text, "may exhaust the model context") {
		t.Errorf("force=true should bypass the warning, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "```json") {
		t.Errorf("expected full JSON, got %q", tc.Text)
	}
}

// TestHandleListFlows_FullDetailBelowThreshold: <threshold nodes, no force
// → full response returned, no safeguard triggered.
func TestHandleListFlows_FullDetailBelowThreshold(t *testing.T) {
	flows := buildFlowsJSON(t, 5)
	s := newListFlowsServer(t, flows, 200)

	res, err := s.handleListFlows(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"detail": "full",
		}},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected a result")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if strings.Contains(tc.Text, "may exhaust the model context") {
		t.Errorf("below threshold should not warn, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "```json") {
		t.Errorf("expected full JSON for small flow, got %q", tc.Text)
	}
}
