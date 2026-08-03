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
		// GET /flows (the disabled-and-type lookup introduced by
		// #104 and #43) returns the active configuration as a
		// flat array: the inject node and its owning tab.
		if r.Method == http.MethodGet && r.URL.Path == "/flows" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"t","type":"tab","label":"Home"},{"id":"n1","type":"inject","z":"t","name":"tick","wires":[],"x":1,"y":1}]`))
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
// __user_inject_props__ trigger gets inserted into the body so Node-RED 5.x
// forwards the body to node.receive. The user's payload lands as msg.payload
// downstream — see InjectNodeWithBody doc comment for the underlying
// mechanism.
func TestHandleInjectNode_WithPayload(t *testing.T) {
	var posts []capture
	var srv *Server
	var got *capture
	srv, got = injectServer(t, func(w http.ResponseWriter, r *http.Request) {
		posts = append(posts, *got)
		if r.Method == http.MethodGet && r.URL.Path == "/flows" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"t","type":"tab"},{"id":"n1","type":"inject","z":"t","wires":[],"x":1,"y":1}]`))
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
	if !strings.Contains(tc.Text, "payload") {
		t.Errorf("expected the payload success text, got %q", tc.Text)
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

	var sent map[string]json.RawMessage
	if err := json.Unmarshal(post.body, &sent); err != nil {
		t.Fatalf("body is not JSON: %v (raw: %s)", err, string(post.body))
	}
	// The magic trigger must be present and empty (a non-empty list would
	// tell Node-RED to apply per-call msg.* prop overrides instead of
	// forwarding the body as msg).
	props := sent["__user_inject_props__"]
	var propsList []any
	if err := json.Unmarshal(props, &propsList); err != nil || len(propsList) != 0 {
		t.Errorf("expected __user_inject_props__ to be an empty array, got %s", string(props))
	}
	// The caller's payload fields reach the wire at the top level —
	// a "payload" wrapper only appears for non-object payloads
	// (scalars / arrays), where buildInjectPayloadBody wraps them
	// under that key so msg.payload lands as the caller's value.
	fooRaw, ok := sent["foo"]
	if !ok {
		t.Errorf("expected body[\"foo\"] to be present, got body keys: %v", keysOf(sent))
	}
	var foo float64
	if err := json.Unmarshal(fooRaw, &foo); err != nil {
		t.Fatalf("body[foo] is not a number: %v (raw: %s)", err, string(fooRaw))
	}
	if foo != 1 {
		t.Errorf("expected body[foo] = 1, got %v", foo)
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// JSON-encoded scalar payloads (e.g. payload="42" or payload="[1,2,3]")
// are accepted: encodePayloadArg only requires the string itself to be
// valid JSON. Bare primitives like the number 42 or the string hello
// (without quotes) come through the switch as default and are rejected
// before the runtime is touched, so they do not reach this test path.
func TestHandleInjectNode_PrimitivePayloads(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"number-string", "42"},
		{"array-string", "[1,2,3]"},
		{"object-string", `{"x":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var posts []capture
			var srv *Server
			var got *capture
			srv, got = injectServer(t, func(w http.ResponseWriter, r *http.Request) {
				posts = append(posts, *got)
				if r.Method == http.MethodGet && r.URL.Path == "/flows" {
					_, _ = w.Write([]byte(`[{"id":"t","type":"tab"},{"id":"n1","type":"inject","z":"t","wires":[],"x":1,"y":1}]`))
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
			if len(post.body) == 0 {
				t.Errorf("expected a body, got empty")
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
