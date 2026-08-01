package mcp

import (
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

// injectServer builds the smallest test setup for the inject_node handler:
// a Server whose nrClient points at an httptest server that captures the
// request. Mirror the existing clipboard_test.go setup so reviewers see the
// same scaffolding everywhere.
func injectServer(t *testing.T, h http.HandlerFunc) (*Server, *capture) {
	t.Helper()
	got := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		if r.Body != nil {
			body, _ := io.ReadAll(r.Body)
			got.body = body
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	c, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return New(c, Options{Version: "test"}), got
}

type capture struct {
	method string
	path   string
	body   []byte
}

// Without a payload, inject_node must hit /inject/:id with no body —
// the historical behaviour every existing caller depends on. The
// Node-RED client (introduced by #59) does a GET /flow/:id first to
// confirm the node is an inject; the captured request list must include
// that probe AND the POST. We accept either order; what matters is
// that the POST carries no body.
func TestHandleInjectNode_NoPayload(t *testing.T) {
	var posts []capture
	var srv *Server
	var got *capture
	srv, got = injectServer(t, func(w http.ResponseWriter, r *http.Request) {
		posts = append(posts, *got)
		// GET /flow/:id returns a single inject node so the type check passes.
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"n1","type":"inject","z":"t","name":"tick","wires":[],"x":1,"y":1}`))
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
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected a non-empty result")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, `fired`) {
		t.Errorf("expected success text, got %q", tc.Text)
	}

	var sawPost bool
	for _, p := range posts {
		if p.method == "POST" && p.path == "/inject/n1" {
			sawPost = true
			if len(p.body) != 0 {
				t.Errorf("expected no body on POST, got %q", string(p.body))
			}
		}
	}
	if !sawPost {
		t.Errorf("expected a POST /inject/n1, got requests: %+v", posts)
	}
}

// With a payload, inject_node wraps the value in the
// __user_inject_props__ envelope and POSTs it as a body. The user
// payload becomes msg.payload downstream — see InjectNodeWithBody
// doc comment for the underlying mechanism.
func TestHandleInjectNode_WithPayload(t *testing.T) {
	var posts []capture
	var srv *Server
	var got *capture
	srv, got = injectServer(t, func(w http.ResponseWriter, r *http.Request) {
		posts = append(posts, *got)
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"n1","type":"inject","z":"t","wires":[],"x":1,"y":1}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	res, err := srv.handleInjectNode(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"id":      "n1",
			"payload": map[string]any{"foo": 1},
		}},
	})
	if err != nil {
		t.Fatalf("handleInjectNode: %v", err)
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "override payload") {
		t.Errorf("expected the payload-overridden success text, got %q", tc.Text)
	}

	var post capture
	for _, p := range posts {
		if p.method == "POST" && p.path == "/inject/n1" {
			post = p
		}
	}
	if post.method == "" {
		t.Fatalf("expected a POST /inject/n1, got: %+v", posts)
	}

	var sent struct {
		UserProps []map[string]any `json:"__user_inject_props__"`
	}
	if err := json.Unmarshal(post.body, &sent); err != nil {
		t.Fatalf("body is not the expected envelope: %v (raw: %s)", err, string(post.body))
	}
	if len(sent.UserProps) != 1 {
		t.Fatalf("expected one prop entry, got %d (%+v)", len(sent.UserProps), sent.UserProps)
	}
	entry := sent.UserProps[0]
	if entry["p"] != "payload" {
		t.Errorf(`expected entry.p = "payload", got %v`, entry["p"])
	}
	if entry["vt"] != "json" {
		t.Errorf(`expected entry.vt = "json", got %v`, entry["vt"])
	}
	v, ok := entry["v"].(map[string]any)
	if !ok {
		t.Fatalf("expected entry.v to be an object, got %T (%v)", entry["v"], entry["v"])
	}
	if v["foo"] != float64(1) {
		t.Errorf(`expected entry.v.foo = 1, got %v`, v["foo"])
	}
}

// Primitive payloads (string, number, bool, null) round-trip through the
// envelope the same way objects do. A user passing payload="hello"
// expects msg.payload="hello" downstream, not the literal string
// `"hello"` JSON-quoted.
func TestHandleInjectNode_PrimitivePayloads(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want any
	}{
		{"string", "hello", "hello"},
		{"number", 42.5, 42.5},
		{"bool", true, true},
		{"null", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var posts []capture
			var srv *Server
			var got *capture
			srv, got = injectServer(t, func(w http.ResponseWriter, r *http.Request) {
				posts = append(posts, *got)
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"id":"n1","type":"inject","z":"t","wires":[],"x":1,"y":1}`))
					return
				}
				w.WriteHeader(http.StatusOK)
			})

			_, err := srv.handleInjectNode(context.Background(), mcp.CallToolRequest{
				Params: mcp.CallToolParams{Arguments: map[string]any{
					"id":      "n1",
					"payload": tc.in,
				}},
			})
			if err != nil {
				t.Fatalf("handleInjectNode: %v", err)
			}
			var post capture
			for _, p := range posts {
				if p.method == "POST" && p.path == "/inject/n1" {
					post = p
				}
			}
			if post.method == "" {
				t.Fatalf("expected a POST /inject/n1, got: %+v", posts)
			}
			var sent struct {
				UserProps []map[string]any `json:"__user_inject_props__"`
			}
			if err := json.Unmarshal(post.body, &sent); err != nil {
				t.Fatalf("envelope decode: %v", err)
			}
			if len(sent.UserProps) != 1 {
				t.Fatalf("expected one prop entry, got %d", len(sent.UserProps))
			}
			v := sent.UserProps[0]["v"]
			if v != tc.want {
				t.Errorf("expected entry.v = %v, got %v (%T)", tc.want, v, v)
			}
		})
	}
}

// Empty id is still refused before the wire is hit — same validation
// behaviour callers have always relied on.
func TestHandleInjectNode_EmptyID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be called for empty id, got %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(srv.Close)
	c, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	s := New(c, Options{Version: "test"})

	res, err := s.handleInjectNode(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"id": ""}},
	})
	if err != nil {
		t.Fatalf("handleInjectNode: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected a non-empty result")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "id") || !strings.Contains(tc.Text, "required") {
		t.Errorf("expected a required-id error, got %q", tc.Text)
	}
}

// injectPayloadEnvelope is the unit test for the envelope builder
// itself, separated from the handler so a future change to either
// surface is independently localisable. The user-supplied payload lands
// in entry.v verbatim — what arrives at Node-RED is exactly what the
// caller passed.
func TestInjectPayloadEnvelope_ObjectPayload(t *testing.T) {
	out, err := injectPayloadEnvelope(map[string]any{"foo": 1})
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	var sent struct {
		UserProps []map[string]any `json:"__user_inject_props__"`
	}
	if err := json.Unmarshal(out, &sent); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(sent.UserProps) != 1 || sent.UserProps[0]["p"] != "payload" || sent.UserProps[0]["vt"] != "json" {
		t.Fatalf("envelope shape wrong: %+v", sent)
	}
}
