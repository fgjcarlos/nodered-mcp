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

// TestInjectNode_WithoutPayloadCallsOriginal asserts the
// backward-compatible path: when no payload arg is present,
// inject_node calls the original InjectNode (no body) — what
// callers relied on before issue #54.
func TestInjectNode_WithoutPayloadCallsOriginal(t *testing.T) {
	var injectCalled, withBodyCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			// node-type lookup via the helper added in #43
			_, _ = w.Write([]byte(`[{"id":"n1","type":"inject","z":"tab1"}]`))
		case "/inject/n1":
			injectCalled = true
			if r.ContentLength > 0 {
				withBodyCalled = true
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	srv2 := New(c, Options{Version: "test"})
	res, err := srv2.handleInjectNode(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"id": "n1"}},
	})
	if err != nil {
		t.Fatalf("handleInjectNode: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	if !injectCalled {
		t.Error("InjectNode was not called")
	}
	if withBodyCalled {
		t.Error("InjectNode was called with a body — should be empty (backward-compat path)")
	}
}

// TestInjectNode_WithPayloadCallsBody covers the new path: when
// a payload arg is present, inject_node uses InjectNodeWithBody
// and the body must contain the caller-supplied fields and the
// __user_inject_props__ trigger.
func TestInjectNode_WithPayloadCallsBody(t *testing.T) {
	var seenBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			_, _ = w.Write([]byte(`[{"id":"n1","type":"inject","z":"tab1"}]`))
		case "/inject/n1":
			seenBody = make([]byte, r.ContentLength)
			_, _ = r.Body.Read(seenBody)
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	srv2 := New(c, Options{Version: "test"})
	res, err := srv2.handleInjectNode(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"id":      "n1",
			"payload": map[string]any{"foo": "bar", "n": 42},
		}},
	})
	if err != nil {
		t.Fatalf("handleInjectNode: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	if len(seenBody) == 0 {
		t.Fatal("body was empty")
	}
	var body map[string]any
	if err := json.Unmarshal(seenBody, &body); err != nil {
		t.Fatalf("body is not JSON: %v (body: %s)", err, string(seenBody))
	}
	if body["foo"] != "bar" {
		t.Errorf("body[foo] = %v, want bar", body["foo"])
	}
	if v, _ := body["n"].(float64); v != 42 {
		t.Errorf("body[n] = %v, want 42", body["n"])
	}
	// The magic trigger must be present.
	props, _ := body["__user_inject_props__"].([]any)
	if len(props) != 0 {
		t.Errorf("__user_inject_props__ should be an empty array, got %v", props)
	}
}

// TestInjectNode_RejectsInvalidPayloadString asserts that a
// non-JSON string payload is rejected before any server call.
func TestInjectNode_RejectsInvalidPayloadString(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be called for invalid payload: %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	srv2 := New(c, Options{Version: "test"})
	res, err := srv2.handleInjectNode(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"id":      "n1",
			"payload": "this is not json",
		}},
	})
	if err != nil {
		t.Fatalf("handleInjectNode: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error for an invalid payload string")
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "JSON") {
		t.Errorf("error should mention JSON, got %q", tc.Text)
	}
}

// TestListSubflows_EmptyResult is a thin wrapper at the MCP
// layer. The nodered.Client test already covers the helper; this
// one exercises the handler-side rendering of an empty list.
func TestListSubflows_EmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/flow/global" {
			t.Errorf("expected /flow/global, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"global"}`))
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	srv2 := New(c, Options{Version: "test"})
	res, err := srv2.handleListSubflows(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{}},
	})
	if err != nil {
		t.Fatalf("handleListSubflows: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "[]") {
		t.Errorf("response should contain an empty JSON array, got %q", tc.Text)
	}
}

// TestDeleteSubflow_InUseSubflowIsRefusedNotReplayed pins the
// user-facing behaviour of the guard added for issue #332 at the layer
// where it is surfaced.
//
// The refusal has to reach the caller as a plain tool error that names
// the instance and its owner. It must NOT be classified as a transport
// error: a write tool that fails that way is reported as having an
// indeterminate outcome, which is exactly the wrong advice for a
// refusal that changed nothing.
func TestDeleteSubflow_InUseSubflowIsRefusedNotReplayed(t *testing.T) {
	var putGlobal bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			_, _ = w.Write([]byte(`{"id":"global","subflows":[
				{"id":"sf_a","type":"subflow","name":"A","in":[],"out":[],"env":[],"nodes":[]}
			],"configs":[]}`))
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[
				{"id":"tab1","type":"tab","label":"Home"},
				{"id":"inst1","type":"subflow:sf_a","z":"tab1","x":140,"y":140}
			]`))
		case r.Method == "PUT" && r.URL.Path == "/flow/global":
			putGlobal = true
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
	s := New(c, Options{Version: "test"})
	res, err := s.handleDeleteSubflow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"id": "sf_a"}},
	})
	if err != nil {
		t.Fatalf("handleDeleteSubflow returned a transport error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected a tool error result, got %+v", res)
	}
	text := resultFirstText(res)
	for _, want := range []string{"sf_a", "inst1", "tab1"} {
		if !strings.Contains(text, want) {
			t.Errorf("refusal does not mention %q: %s", want, text)
		}
	}
	if strings.Contains(text, "indeterminate") {
		t.Errorf("a refusal must not be reported as an indeterminate outcome: %s", text)
	}
	if putGlobal {
		t.Error("the definition was written despite being in use")
	}
}
