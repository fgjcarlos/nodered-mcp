package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// ---------------------------------------------------------------------------
// Issue #329: the node denylist must hold on every mutating surface.
//
// These tests drive the MCP tool handlers — the same surface a model calls —
// and assert two things for every write path that can carry a node type:
//
//  1. a forbidden node type is refused, and
//  2. the refusal happens before the runtime is asked to deploy anything.
//
// The second half is what makes the control worth having: a refusal that
// still deploys (or snapshots) has already done the damage it was meant to
// prevent.
//
// The fixtures below never serve a deployment. A test that reaches the
// runtime's write endpoints fails, so a regression that lets a denied type
// through shows up as a failure and not as a silently-accepted write.
// ---------------------------------------------------------------------------

// issue329Runtime starts a Node-RED stand-in that records every request it
// receives and fails the test when a deployment request (POST/PUT) arrives.
// Reads are answered with the given bodies, keyed by "METHOD /path".
//
// The returned slice is safe to read after the test body finishes.
func issue329Runtime(t *testing.T, bodies map[string]string) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()

		switch r.Method {
		case http.MethodGet:
			for key, body := range bodies {
				if key == "GET "+r.URL.Path {
					_, _ = w.Write([]byte(body))
					return
				}
			}
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("a denied write reached the runtime: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	t.Cleanup(srv.Close)

	return srv, &seen
}

// issue329Server builds a Server whose denylist holds exactly the given
// node types, wired to the given runtime stand-in.
func issue329Server(t *testing.T, srv *httptest.Server, types ...string) *Server {
	t.Helper()
	c, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return New(c, Options{Version: "test", NodeDenylist: types})
}

// issue329Requests returns the requests the runtime stand-in received.
func issue329Requests(seen *[]string) []string {
	return append([]string(nil), (*seen)...)
}

// assertIssue329Denied asserts the tool result is a typed refusal that
// names MCP_NODE_DENYLIST and the offending node type.
func assertIssue329Denied(t *testing.T, res *mcp.CallToolResult, nodeType string) {
	t.Helper()
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
	if !strings.Contains(tc.Text, `"`+nodeType+`"`) {
		t.Errorf("error must echo the denied type back, got %q", tc.Text)
	}
}

// issue329NodeObject decodes a node payload into the map shape the tool
// arguments accept.
func issue329NodeObject(raw string) map[string]any {
	var node map[string]any
	if err := json.Unmarshal([]byte(raw), &node); err != nil {
		panic("invalid node fixture: " + err.Error())
	}
	return node
}

// issue329ExecNode is the canonical forbidden node: a shell-executing one.
const issue329ExecNode = `{"id":"e1","type":"exec","z":"tabA","x":140,"y":140,"command":"id","wires":[]}`
