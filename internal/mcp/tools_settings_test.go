package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// --- settings / diagnostics / plugins ---

func TestHandleGetSettings_RoundTrip(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/settings" {
			t.Errorf("expected /settings, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"httpNodeRoot":"/","version":"3.1.0"}`))
	})
	res, err := call(t, s.handleGetSettings, map[string]any{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "3.1.0") {
		t.Errorf("response should include settings version, got %q", tc.Text)
	}
}

func TestHandleGetSettings_PropagatesUpstreamError(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	if res, _ := call(t, s.handleGetSettings, map[string]any{}); !res.IsError {
		t.Fatal("expected error result from upstream 500")
	}
}

func TestHandleGetDiagnostics_RoundTrip(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/diagnostics" {
			t.Errorf("expected /diagnostics, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"runtime":{"heapUsed":120000}}`))
	})
	res, err := call(t, s.handleGetDiagnostics, map[string]any{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "heapUsed") {
		t.Errorf("response should include runtime heap, got %q", tc.Text)
	}
}

func TestHandleGetDiagnostics_NotFoundHasActionableHint(t *testing.T) {
	// On Node-RED < 3.1 /diagnostics is not mounted. The handler must
	// surface the older-version hint rather than a generic 404.
	s, _ := serverWithMock(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	res, _ := call(t, s.handleGetDiagnostics, map[string]any{})
	if !res.IsError {
		t.Fatal("404 should be a typed error")
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "3.1") {
		t.Errorf("hint should mention Node-RED 3.1, got %q", tc.Text)
	}
}

func TestHandleListPlugins_Empty(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/plugins" {
			t.Errorf("expected /plugins, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[]`))
	})
	res, err := call(t, s.handleListPlugins, map[string]any{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "No editor plugins") {
		t.Errorf("expected empty-plugins copy, got %q", tc.Text)
	}
}

func TestHandleListPlugins_Lists(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"node-red-example-plugin","enabled":true}]`))
	})
	res, err := call(t, s.handleListPlugins, map[string]any{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "node-red-example-plugin") {
		t.Errorf("response should list plugin id, got %q", tc.Text)
	}
}

// --- runtime state (handleGetFlowsState / handleSetFlowsState) ---

func TestHandleGetFlowsState_RoundTrip(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/flows/state" || r.Method != http.MethodGet {
			t.Errorf("expected GET /flows/state, got %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"state":"started"}`))
	})
	res, err := call(t, s.handleGetFlowsState, map[string]any{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "started") {
		t.Errorf("response should include state, got %q", tc.Text)
	}
}

func TestHandleGetFlowsState_NotFound_HintMentionsRuntimeState(t *testing.T) {
	// Without settings.runtimeState.enabled, GET /flows/state 404s.
	// The handler should surface the runtimeState hint.
	s, _ := serverWithMock(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	res, _ := call(t, s.handleGetFlowsState, map[string]any{})
	if !res.IsError {
		t.Fatal("404 should be a typed error")
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "runtimeState") {
		t.Errorf("hint should mention runtimeState, got %q", tc.Text)
	}
}

func TestHandleSetFlowsState_Start(t *testing.T) {
	const flowsList = `[{"id":"tab1","type":"tab","disabled":false}]`
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			_, _ = w.Write([]byte(flowsList))
		case "/flows/state":
			if r.Method != http.MethodPost {
				t.Errorf("expected POST /flows/state, got %s", r.Method)
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
		}
	})
	res, err := call(t, s.handleSetFlowsState, map[string]any{"state": "start"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "started") {
		t.Errorf("response should report started, got %q", tc.Text)
	}
}

func TestHandleSetFlowsState_Stop(t *testing.T) {
	const flowsList = `[{"id":"tab1","type":"tab","disabled":false}]`
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			_, _ = w.Write([]byte(flowsList))
		case "/flows/state":
			_, _ = w.Write([]byte(`{}`))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
		}
	})
	res, err := call(t, s.handleSetFlowsState, map[string]any{"state": "stop"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "stop") {
		t.Errorf("response should report stop, got %q", tc.Text)
	}
}

func TestHandleSetFlowsState_MissingStateIsError(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, _ *http.Request) {})
	if res, _ := call(t, s.handleSetFlowsState, map[string]any{}); !res.IsError {
		t.Fatal("missing state should be a typed error")
	}
}

func TestHandleSetFlowsState_InvalidStateIsError(t *testing.T) {
	// "start"/"stop" only; the client rejects anything else.
	s, _ := serverWithMock(t, func(w http.ResponseWriter, _ *http.Request) {})
	if res, _ := call(t, s.handleSetFlowsState, map[string]any{"state": "pause"}); !res.IsError {
		t.Fatal("invalid state should be a typed error")
	}
}

// --- set_flows (full deployment) ---

func TestHandleSetFlows_RoundTrip(t *testing.T) {
	// SetFlows expects flows with at least one {"type":"tab"} entry —
	// the client rejects a non-tab-only array (issue #106 guard).
	const flowsList = `[{"id":"tab1","type":"tab","disabled":false}]`
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(flowsList))
				return
			}
			// POST /flows (full deploy) — the client sets the
			// Node-RED-Deployment-Type header; check it.
			if got := r.Header.Get("Node-RED-Deployment-Type"); got != "full" {
				t.Errorf("expected Node-RED-Deployment-Type: full, got %q", got)
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
		}
	})
	res, err := call(t, s.handleSetFlows, map[string]any{
		"flows": []any{map[string]any{"id": "tab1", "type": "tab", "nodes": []any{}}},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
}

func TestHandleSetFlows_RejectsNoTabFlows(t *testing.T) {
	// The SetFlows client refuses to deploy an array with no tab.
	s, _ := serverWithMock(t, func(w http.ResponseWriter, _ *http.Request) {
		// No upstream should be called; the client errors out first.
	})
	if res, _ := call(t, s.handleSetFlows, map[string]any{
		"flows": []any{map[string]any{"id": "n1", "type": "inject"}},
	}); !res.IsError {
		t.Fatal("non-tab-only flows should be a typed error")
	}
}

// --- search_nodes (npm registry, isolated via SearchBaseURL) ---

func TestHandleSearchNodes_RoundTrip(t *testing.T) {
	// Mock the npm registry locally and aim the client at it.
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "node-red") {
			t.Errorf("expected node-red keyword in query, got %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"objects":[{"package":{"name":"node-red-dashboard","description":"UI","version":"3.0.0"}}]}`))
	}))
	t.Cleanup(registry.Close)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	c, err := nodered.NewClient(nodered.Options{
		BaseURL:       srv.URL,
		BackupDir:     t.TempDir(),
		SearchBaseURL: registry.URL,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	s := New(c, Options{Version: "test"})

	res, err := call(t, s.handleSearchNodes, map[string]any{"query": "dashboard"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "node-red-dashboard") {
		t.Errorf("response should list the hit, got %q", tc.Text)
	}
}

func TestHandleSearchNodes_EmptyResults(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"objects":[]}`))
	}))
	t.Cleanup(registry.Close)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{
		BaseURL:       srv.URL,
		BackupDir:     t.TempDir(),
		SearchBaseURL: registry.URL,
	})
	s := New(c, Options{Version: "test"})

	res, err := call(t, s.handleSearchNodes, map[string]any{"query": "dashboard"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "No node-red modules") {
		t.Errorf("empty result should report no matches, got %q", tc.Text)
	}
}

func TestHandleSearchNodes_MissingQueryIsError(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, _ *http.Request) {})
	if res, _ := call(t, s.handleSearchNodes, map[string]any{}); !res.IsError {
		t.Fatal("missing query should be a typed error")
	}
}

// --- guard against regressions in errors.As / APIError plumbing ---

func TestHandleSetFlowsState_NotFound_HintMentionsRuntimeState(t *testing.T) {
	const flowsList = `[{"id":"tab1","type":"tab","disabled":false}]`
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flows":
			_, _ = w.Write([]byte(flowsList))
		case "/flows/state":
			http.Error(w, "not found", http.StatusNotFound)
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
		}
	})
	res, _ := call(t, s.handleSetFlowsState, map[string]any{"state": "start"})
	if !res.IsError {
		t.Fatal("404 should be a typed error")
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "runtimeState") {
		t.Errorf("hint should mention runtimeState, got %q", tc.Text)
	}
}

// Sanity check: errors.As wiring is in place. This is a one-shot smoke
// test that documents the APIError type is exposed via errors.As from
// the client layer — relies on the production error path.
func TestAPIError_AsWraps404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	c, _ := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	_, err := c.GetSettings(context.Background())
	if err == nil {
		t.Fatal("expected error from 404")
	}
	var apiErr *nodered.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("errors.As did not extract *nodered.APIError: %v", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", apiErr.StatusCode)
	}
}
