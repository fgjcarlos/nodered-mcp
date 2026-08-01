package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
}

// totalTools is the full-mode count: every read tool plus every mutating one.
const totalTools = 37

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
	for _, want := range []string{"create_flow", "set_flows", "restore_backup", "inject_node", "set_context", "disable_flow", "enable_flow"} {
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
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
