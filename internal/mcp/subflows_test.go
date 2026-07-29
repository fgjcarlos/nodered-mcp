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
