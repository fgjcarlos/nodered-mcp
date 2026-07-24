package nodered

import (
	"context"
	"encoding/json"
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

func TestInjectNode(t *testing.T) {
	var called string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = r.URL.Path
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	})

	if err := c.InjectNode(context.Background(), "n1"); err != nil {
		t.Fatalf("InjectNode: %v", err)
	}
	if called != "/inject/n1" {
		t.Errorf("expected path /inject/n1, got %s", called)
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

func TestGetFlow_NotFound(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/flow/") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		http.Error(w, `{"code":"not_found"}`, http.StatusNotFound)
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
