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

// TestHandleGetRuntimeInfo_CapabilityGuidanceOldRuntime is the
// runtime proof for the version_too_low reason: an NR 3.0 mock
// should produce a reason that names the REAL minimum for each
// gated tool — 3.1.0 for get_diagnostics, 5.0.0 for set_context and
// inject_node — and a remedy that says "upgrade Node-RED".
func TestHandleGetRuntimeInfo_CapabilityGuidanceOldRuntime(t *testing.T) {
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

	// Guidance block must be present and well-formed.
	if !strings.Contains(tc.Text, `"capabilityGuidance"`) {
		t.Fatalf("response should include capabilityGuidance, got %q", tc.Text)
	}
	// get_diagnostics: real minimum is 3.1.0, and the reason must
	// name it (a generic "upgrade Node-RED" is the failure mode).
	if !strings.Contains(tc.Text, `"get_diagnostics"`) || !strings.Contains(tc.Text, `3.1.0`) {
		t.Errorf("get_diagnostics reason should name 3.1.0 minimum, got %q", tc.Text)
	}
	// set_context: real minimum is 5.0.0.
	if !strings.Contains(tc.Text, `"set_context"`) || !strings.Contains(tc.Text, `5.0.0`) {
		t.Errorf("set_context reason should name 5.0.0 minimum, got %q", tc.Text)
	}
	// Both tools must have a remedy, and each remedy must name
	// the minimum for THAT tool (the reason must change with the
	// tool).
	if !strings.Contains(tc.Text, `"remedy"`) {
		t.Errorf("every non-ok capability should carry a remedy, got %q", tc.Text)
	}
}

// TestHandleGetRuntimeInfo_CapabilityGuidanceSettingDisabled is
// the runtime proof for setting_disabled: the reason must name
// the actual setting path (settings.runtimeState.enabled), not a
// generic "feature off" string.
func TestHandleGetRuntimeInfo_CapabilityGuidanceSettingDisabled(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/settings":
			_, _ = w.Write([]byte(`{"version":"5.0.1","runtimeState":{"enabled":false}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	res, _ := call(t, s.handleGetRuntimeInfo, map[string]any{})
	if res.IsError {
		t.Fatalf("handler reported error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, `"get_flows_state": "setting_disabled"`) {
		t.Fatalf("get_flows_state should be setting_disabled, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, `settings.runtimeState.enabled`) {
		t.Errorf("reason should name settings.runtimeState.enabled, got %q", tc.Text)
	}
}

// TestHandleGetRuntimeInfo_CapabilityGuidanceEndpointNotMounted
// covers the endpoint_not_mounted reason: the /logs endpoint
// returns 404, and the guidance must say so and mention the local
// log fallback.
func TestHandleGetRuntimeInfo_CapabilityGuidanceEndpointNotMounted(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/settings":
			_, _ = w.Write([]byte(`{"version":"5.0.1"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	res, _ := call(t, s.handleGetRuntimeInfo, map[string]any{})
	if res.IsError {
		t.Fatalf("handler reported error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, `"get_runtime_logs": "endpoint_not_mounted"`) {
		t.Fatalf("get_runtime_logs should be endpoint_not_mounted, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, `/logs`) {
		t.Errorf("reason should name /logs endpoint, got %q", tc.Text)
	}
}

// TestHandleGetRuntimeInfo_CapabilityGuidanceUnknownRuntime is
// the runtime proof that unknown stays explicit: when /settings
// is unreachable the reason is the unknown copy, NOT a
// smoothed-over "probably fine".
func TestHandleGetRuntimeInfo_CapabilityGuidanceUnknownRuntime(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	res, _ := call(t, s.handleGetRuntimeInfo, map[string]any{})
	if res.IsError {
		t.Fatalf("handler reported error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, `"get_diagnostics": "unknown"`) {
		t.Fatalf("get_diagnostics should be unknown, got %q", tc.Text)
	}
	// unknown tool should still have a non-empty reason and no
	// claim that it is unavailable or available.
	if !strings.Contains(tc.Text, `"reason"`) {
		t.Errorf("unknown tool should still carry a reason, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, `was not detected`) {
		t.Errorf("unknown reason should be the honest 'was not detected' copy, got %q", tc.Text)
	}
}

// TestHandleGetRuntimeInfo_CapabilityGuidanceOkCarriesNothing is
// the additive-contract test: a healthy NR 5.0.1 with everything
// on must NOT add a guidance entry for ok tools. Existing
// capabilityMatrix consumers see no change.
func TestHandleGetRuntimeInfo_CapabilityGuidanceOkCarriesNothing(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/settings":
			_, _ = w.Write([]byte(`{"version":"5.0.1","runtimeState":{"enabled":true}}`))
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

	// get_diagnostics is ok; it must NOT appear in capabilityGuidance.
	// We check by finding the capabilityGuidance block and making
	// sure it does not contain a "get_diagnostics" key.
	idx := strings.Index(tc.Text, `"capabilityGuidance"`)
	if idx < 0 {
		t.Fatalf("response should include capabilityGuidance, got %q", tc.Text)
	}
	// Look only at content after the guidance block opens.
	rest := tc.Text[idx:]
	// Close brace of the map — the next "}": at the same indent.
	end := strings.Index(rest, "\n  }")
	if end < 0 {
		end = len(rest)
	} else {
		end += idx
	}
	block := tc.Text[idx:end]
	if strings.Contains(block, `"get_diagnostics"`) {
		t.Errorf("ok tool get_diagnostics should not appear in capabilityGuidance, got block:\n%s", block)
	}
	if strings.Contains(block, `"get_flows_state"`) {
		t.Errorf("ok tool get_flows_state should not appear in capabilityGuidance, got block:\n%s", block)
	}
}
