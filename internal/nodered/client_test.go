package nodered

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := NewClient(Options{
		BaseURL: srv.URL,
		Token:   "test-token",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c, srv
}

func TestListFlows_Success(t *testing.T) {
	// A bare-array response (Node-RED admin API v1) with two tabs and a node.
	const body = `[
		{"id":"tab1","type":"tab","label":"Home"},
		{"id":"n1","type":"inject","z":"tab1","wires":[["n2"]]},
		{"id":"tab2","type":"tab","label":"Empty"}
	]`

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/flows" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("missing bearer auth, got %q", got)
		}
		_, _ = w.Write([]byte(body))
	})

	got, err := c.ListFlows(context.Background())
	if err != nil {
		t.Fatalf("ListFlows: %v", err)
	}
	if !json.Valid(got) {
		t.Fatalf("ListFlows returned invalid JSON: %s", got)
	}
	if n := FlowTabCount(got); n != 2 {
		t.Errorf("expected 2 flow tabs, got %d", n)
	}
}

func TestFlowTabCount_Envelope(t *testing.T) {
	// API v2 wraps the array in a {"rev":..,"flows":[..]} envelope.
	env := RawFlow(`{"rev":"abc","flows":[
		{"id":"tab1","type":"tab","label":"Home"},
		{"id":"n1","type":"function","z":"tab1"}
	]}`)
	if n := FlowTabCount(env); n != 1 {
		t.Errorf("expected 1 tab from envelope, got %d", n)
	}
}

func TestListFlows_ServerError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	_, err := c.ListFlows(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != 500 {
		t.Errorf("expected status 500, got %d", apiErr.StatusCode)
	}
}

// TestInjectNode covers the happy path: a known inject node is fired.
// Since issue #43, InjectNode first looks up the node's type via
// GET /flows, so the test mock answers both /flows and /inject/:id.
func TestInjectNode(t *testing.T) {
	var injectCalled bool
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			_, _ = w.Write([]byte(`[
				{"id":"n1","type":"inject","z":"tab1","name":"tick"},
				{"id":"tab1","type":"tab","label":"Home"}
			]`))
		case "/inject/n1":
			injectCalled = true
			if r.Method != "POST" {
				t.Errorf("expected POST, got %s", r.Method)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	})

	if err := c.InjectNode(context.Background(), "n1"); err != nil {
		t.Fatalf("InjectNode: %v", err)
	}
	if !injectCalled {
		t.Error("expected POST /inject/n1 to be called")
	}
}

func TestInjectNode_EmptyID(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be called for empty id")
	})
	if err := c.InjectNode(context.Background(), ""); err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

// TestInjectNode_RejectsNonInjectType is the regression test for
// issue #43 (audit of v0.5.12, 28 Jul 2026): the MCP used to return
// success for any node id that existed, even when the node was not an
// inject. Now InjectNode refuses and surfaces the actual type.
func TestInjectNode_RejectsNonInjectType(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			_, _ = w.Write([]byte(`[
				{"id":"aud_comment","type":"comment","z":"tab1","info":"staging"},
				{"id":"tab1","type":"tab","label":"Home"}
			]`))
		case "/inject/aud_comment":
			t.Error("POST /inject/aud_comment must not be called for a non-inject node")
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	})

	err := c.InjectNode(context.Background(), "aud_comment")
	if err == nil {
		t.Fatal("expected error for non-inject node, got nil")
	}
	if !strings.Contains(err.Error(), "comment") {
		t.Errorf("error should name the actual type, got %v", err)
	}
	if !strings.Contains(err.Error(), "aud_comment") {
		t.Errorf("error should name the node id, got %v", err)
	}
}

// TestInjectNode_UnknownIDPassesThrough covers the audit's
// observation that the runtime's HTTP /inject/:id endpoint already
// returns 404 for unknown ids — so the helper passes through to the
// runtime when the node is missing in /flows, and the operator sees
// the runtime's 404 (not a synthetic "wrong type" error).
func TestInjectNode_UnknownIDPassesThrough(t *testing.T) {
	var injectCalled bool
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			_, _ = w.Write([]byte(`[
				{"id":"n1","type":"inject","z":"tab1"},
				{"id":"tab1","type":"tab","label":"Home"}
			]`))
		case "/inject/missing":
			injectCalled = true
			http.Error(w, "Not Found", http.StatusNotFound)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	})

	err := c.InjectNode(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !injectCalled {
		t.Error("expected POST /inject/missing to be called")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("expected *APIError 404 from runtime, got %v", err)
	}
}

// TestInjectNode_LookupFails_SurfacesLookupError covers the
// "fail-loud" branch: when GET /flows errors out, we must NOT silently
// fall back to /inject/:id (that would re-introduce the v0.5.12 false
// positive for any transient runtime blip).
func TestInjectNode_LookupFails_SurfacesLookupError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			http.Error(w, "boom", http.StatusInternalServerError)
		case "/inject/n1":
			t.Error("POST /inject/n1 must not be called when type lookup fails")
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	})

	err := c.InjectNode(context.Background(), "n1")
	if err == nil {
		t.Fatal("expected error from failed type lookup, got nil")
	}
	if !strings.Contains(err.Error(), "verifying inject node type") {
		t.Errorf("error should explain the lookup step, got %v", err)
	}
}

func TestGetFlow_NotFound(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flow/missing", "/flows":
			// /flows must also 404 so the fallback path is exercised: a
			// successful /flows response with a different id would synthesize
			// a not-found and GetFlow would still 404, but a 404 here proves
			// the fallback path was tried.
			http.Error(w, `{"code":"not_found"}`, http.StatusNotFound)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	})
	_, err := c.GetFlow(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
}

func TestNewClient_RequiresBaseURL(t *testing.T) {
	_, err := NewClient(Options{BaseURL: ""})
	if err == nil {
		t.Fatal("expected error for empty BaseURL")
	}
}

func TestNewClient_NormalizesTrailingSlash(t *testing.T) {
	c, err := NewClient(Options{BaseURL: "http://x/", Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if c.baseURL != "http://x" {
		t.Errorf("trailing slash not stripped, got %q", c.baseURL)
	}
}

func TestAPIError_404ExpressStyle_SurfacesHint(t *testing.T) {
	err := &APIError{
		StatusCode: 404,
		Method:     "POST",
		Path:       "/flows/state",
		Body:       "Cannot POST /flows/state",
	}
	msg := err.Error()
	if !strings.Contains(msg, "Cannot POST /flows/state") {
		t.Errorf("original body should still be in the message, got %q", msg)
	}
	if !strings.Contains(msg, "the admin API does not expose this endpoint") {
		t.Errorf("expected the version-or-settings hint, got %q", msg)
	}
}

func TestAPIError_404EmptyBody_NoHint(t *testing.T) {
	err := &APIError{
		StatusCode: 404,
		Method:     "GET",
		Path:       "/nope",
		Body:       "",
	}
	msg := err.Error()
	if strings.Contains(msg, "the admin API does not expose this endpoint") {
		t.Errorf("empty body should not trigger the hint, got %q", msg)
	}
}

func TestAPIError_500_NoHint(t *testing.T) {
	err := &APIError{
		StatusCode: 500,
		Method:     "POST",
		Path:       "/flows/state",
		Body:       "Cannot POST /flows/state",
	}
	msg := err.Error()
	if strings.Contains(msg, "the admin API does not expose this endpoint") {
		t.Errorf("non-404 should not trigger the hint, got %q", msg)
	}
}

// TestInjectNodeWithBody covers the helper used by set_context (issue
// #52): POST /inject/:id with a body that includes
// __user_inject_props__ so the body becomes the inject's msg. The
// mock verifies the method, the path, the auth header and that the
// body round-trips intact.
func TestInjectNodeWithBody(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotBody   []byte
		gotCT     string
	)
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/inject/mcp_ctx_helper_inj" {
			gotMethod = r.Method
			gotPath = r.URL.Path
			gotCT = r.Header.Get("Content-Type")
			gotBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
			return
		}
		t.Errorf("unexpected path: %s", r.URL.Path)
	})

	body := json.RawMessage(`{"key":"foo","value":42,"scope":"global","__user_inject_props__":[]}`)
	if err := c.InjectNodeWithBody(context.Background(), "mcp_ctx_helper_inj", body); err != nil {
		t.Fatalf("InjectNodeWithBody: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/inject/mcp_ctx_helper_inj" {
		t.Errorf("expected /inject/mcp_ctx_helper_inj, got %s", gotPath)
	}
	if !strings.Contains(gotCT, "application/json") {
		t.Errorf("expected JSON content type, got %q", gotCT)
	}
	// Body must round-trip exactly — that's the contract the set_context
	// helper relies on (Node-RED sees __user_inject_props__ and forwards
	// the rest as msg).
	if !bytes.Contains(gotBody, []byte(`"__user_inject_props__":[]`)) {
		t.Errorf("body lost the trigger field: %s", gotBody)
	}
	if !bytes.Contains(gotBody, []byte(`"key":"foo"`)) {
		t.Errorf("body lost the key field: %s", gotBody)
	}
}

func TestInjectNodeWithBody_EmptyID(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be called for empty id")
	})
	if err := c.InjectNodeWithBody(context.Background(), "", json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

func TestInjectNodeWithBody_EmptyBody(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be called for empty body")
	})
	if err := c.InjectNodeWithBody(context.Background(), "mcp_ctx_helper_inj", nil); err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

func TestInjectNodeWithBody_PropagatesAPIErrors(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "node not found", http.StatusNotFound)
	})
	err := c.InjectNodeWithBody(context.Background(), "missing", json.RawMessage(`{"__user_inject_props__":[]}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", apiErr.StatusCode)
	}
}
