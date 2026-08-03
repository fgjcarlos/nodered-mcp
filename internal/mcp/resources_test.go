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

// TestResources_Registered lists every resource the server advertises
// so a future refactor that silently drops one (or renames its URI)
// fails the test. The list lives next to registerResources for easy
// diffing.
func TestResources_Registered(t *testing.T) {
	c, err := nodered.NewClient(nodered.Options{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	s := New(c, Options{Version: "test"})

	wantURIs := map[string]string{
		"nodered://flows/current": "Current flows",
		"nodered://settings":      "Server settings",
		"nodered://flows/state":   "Runtime state",
	}
	seen := make(map[string]string, len(s.resources))
	for _, r := range s.resources {
		seen[r.URI] = r.Name
	}

	for uri, name := range wantURIs {
		got, ok := seen[uri]
		if !ok {
			t.Errorf("resource %q not registered", uri)
			continue
		}
		if got != name {
			t.Errorf("resource %q: name=%q want %q", uri, got, name)
		}
	}
	if len(seen) != len(wantURIs) {
		t.Errorf("resource set drifted: registered=%d want=%d (%v)", len(seen), len(wantURIs), seen)
	}
}

func TestResources_AllFlows(t *testing.T) {
	const flows = `[{"id":"tab1","label":"Home","nodes":[]},{"id":"tab2","label":"Auto","nodes":[]}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/flows" {
			t.Errorf("expected /flows, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(flows))
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL})
	s := New(c, Options{Version: "test"})

	got, err := s.handleFlowsResource(context.Background(), mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleFlowsResource: %v", err)
	}
	tc, ok := got[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("expected TextResourceContents, got %T", got[0])
	}
	if tc.URI != "nodered://flows/current" {
		t.Errorf("URI: got %q", tc.URI)
	}
	if tc.MIMEType != "application/json" {
		t.Errorf("MIMEType: got %q", tc.MIMEType)
	}
	// PrettyJSON produces indented output; assert the JSON parses to
	// the expected number of tabs, not on literal whitespace.
	var arr []map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &arr); err != nil {
		t.Fatalf("resource body is not valid JSON: %v\nbody: %s", err, tc.Text)
	}
	if len(arr) != 2 {
		t.Errorf("expected 2 flows, got %d", len(arr))
	}
}

func TestResources_AllFlows_EmptyReturnsArray(t *testing.T) {
	// Node-RED returns 200 with an empty body when no flows are
	// deployed. handleFlowsResource must substitute a valid empty
	// JSON array so clients can unmarshal without a special case.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(""))
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL})
	s := New(c, Options{Version: "test"})

	got, err := s.handleFlowsResource(context.Background(), mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleFlowsResource: %v", err)
	}
	tc := got[0].(mcp.TextResourceContents)
	body := strings.TrimSpace(tc.Text)
	if body != "[]" {
		t.Errorf("empty flows should marshal to \"[]\", got %q", body)
	}
}

func TestResources_AllFlows_PropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL})
	s := New(c, Options{Version: "test"})

	if _, err := s.handleFlowsResource(context.Background(), mcp.ReadResourceRequest{}); err == nil {
		t.Fatal("expected error from upstream 500, got nil")
	}
}

func TestResources_Settings(t *testing.T) {
	const settings = `{"httpNodeRoot":"/","port":1880,"theme":"dark"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/settings" {
			t.Errorf("expected /settings, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(settings))
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL})
	s := New(c, Options{Version: "test"})

	got, err := s.handleSettingsResource(context.Background(), mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleSettingsResource: %v", err)
	}
	tc := got[0].(mcp.TextResourceContents)
	if tc.URI != "nodered://settings" {
		t.Errorf("URI: got %q", tc.URI)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &doc); err != nil {
		t.Fatalf("settings body is not valid JSON: %v\nbody: %s", err, tc.Text)
	}
	if doc["port"].(float64) != 1880 {
		t.Errorf("port: got %v", doc["port"])
	}
}

func TestResources_FlowsState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/flows/state" {
			t.Errorf("expected /flows/state, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"state":"started"}`))
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL})
	s := New(c, Options{Version: "test"})

	got, err := s.handleFlowsStateResource(context.Background(), mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleFlowsStateResource: %v", err)
	}
	tc := got[0].(mcp.TextResourceContents)
	if tc.URI != "nodered://flows/state" {
		t.Errorf("URI: got %q", tc.URI)
	}
	if !strings.Contains(tc.Text, `"state": "started"`) {
		t.Errorf("body missing started state: %s", tc.Text)
	}
}
