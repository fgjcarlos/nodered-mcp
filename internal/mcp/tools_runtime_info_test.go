package mcp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// TestHandleGetRuntimeInfo_AlwaysReturnsJson is the smoke test:
// regardless of which probes fail, the handler must return a JSON
// block the operator can read.
func TestHandleGetRuntimeInfo_AlwaysReturnsJson(t *testing.T) {
	s := newTestServer(t, false)
	res, err := call(t, s.handleGetRuntimeInfo, map[string]any{})
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("handler reported error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "```json") {
		t.Errorf("response should be a json code block, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "capabilityMatrix") {
		t.Errorf("response should include capabilityMatrix, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "nodeRedVersionDetected") {
		t.Errorf("response should include nodeRedVersionDetected, got %q", tc.Text)
	}
}

// TestHandleGetRuntimeInfo_NR5FullOk exercises the happy path: a
// healthy NR 5.0.1 with everything on. The capability matrix should
// classify everything as ok except the debug-stream tools
// (debugStream is off in the default test server).
func TestHandleGetRuntimeInfo_NR5FullOk(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/settings":
			_, _ = w.Write([]byte(`{"version":"5.0.1","runtimeState":{"enabled":true,"ui":false},"flowFile":"flows.json"}`))
		case "/diagnostics":
			_, _ = w.Write([]byte(`{"heapUsed":1234}`))
		case "/logs":
			_, _ = w.Write([]byte(`[]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	res, _ := call(t, s.handleGetRuntimeInfo, map[string]any{})
	if res.IsError {
		t.Fatalf("handler reported error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, `"version": "5.0.1"`) {
		t.Errorf("response should report NR version 5.0.1, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, `"nodeRedVersionDetected": true`) {
		t.Errorf("response should report version as detected, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, `"get_runtime_logs": "ok"`) {
		t.Errorf("/logs was mounted (mock returns []), should be ok, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, `"get_diagnostics": "ok"`) {
		t.Errorf("/diagnostics returned 200, should be ok, got %q", tc.Text)
	}
}

// TestHandleGetRuntimeInfo_NR3BelowFloor exercises the
// version_too_low branch: a NR 3.0 mock with no /diagnostics.
func TestHandleGetRuntimeInfo_NR3BelowFloor(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/settings":
			_, _ = w.Write([]byte(`{"version":"3.0.0"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	res, _ := call(t, s.handleGetRuntimeInfo, map[string]any{})
	if res.IsError {
		t.Fatalf("handler reported error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, `"get_diagnostics": "version_too_low"`) {
		t.Errorf("get_diagnostics should be version_too_low on NR 3.0, got %q", tc.Text)
	}
}

// TestHandleGetRuntimeInfo_NoProbesSucceeds proves the handler
// survives a complete /settings outage — every probe fails, but
// the response still carries a (mostly empty) capability matrix.
func TestHandleGetRuntimeInfo_NoProbesSucceeds(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	})
	res, _ := call(t, s.handleGetRuntimeInfo, map[string]any{})
	if res.IsError {
		t.Fatalf("handler should not surface a typed error on probe failure, got %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, `"nodeRedVersionDetected": false`) {
		t.Errorf("response should report version as NOT detected, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, `"get_diagnostics": "unknown"`) {
		t.Errorf("versioned tool should classify as unknown when probe failed, got %q", tc.Text)
	}
}
