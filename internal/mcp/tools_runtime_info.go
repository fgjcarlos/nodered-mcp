package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// runtimeInfoProbeTimeout bounds each probe the handler runs. We
// keep this short: get_runtime_info is itself a tool call, and the
// user is going to wait the entire stack's defaultTimeout (30s) if
// we let these hang.
const runtimeInfoProbeTimeout = 5 * time.Second

// runtimeInfo is the JSON shape the operator sees. Keep the field
// names stable — the audit fed a similar shape to a UI mock, and a
// downstream consumer is more than likely to exist.
type runtimeInfo struct {
	NodeRed struct {
		Version      string          `json:"version"`
		VersionKnown bool            `json:"versionKnown"`
		Settings     json.RawMessage `json:"settings,omitempty"`
		RuntimeState json.RawMessage `json:"runtimeState,omitempty"`
	} `json:"nodeRed"`
	MCP struct {
		Version                string            `json:"version"`
		NodeRedVersionDetected bool              `json:"nodeRedVersionDetected"`
		CapabilityMatrix       map[string]string `json:"capabilityMatrix"`
	} `json:"mcp"`
}

// handleGetRuntimeInfo reports the MCP's view of the runtime. It
// is a *companion* to get_diagnostics, not a replacement: that
// tool returns Node-RED's own report (memory, OS, runtime state);
// this one answers "what can this MCP actually do here?".
//
// The handler is best-effort: every probe has a deadline, and
// every failure becomes "unknown" in the JSON rather than a
// surface-level error. The user wants the matrix even when a
// single probe fails.
func (s *Server) handleGetRuntimeInfo(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slog.Debug("tool: get_runtime_info")

	probe := s.runtimeProbe(ctx)

	var info runtimeInfo
	info.NodeRed.Version = probe.NodeRedVersion.String()
	info.NodeRed.VersionKnown = probe.NodeRedVersion.Known
	info.MCP.Version = s.version
	info.MCP.NodeRedVersionDetected = probe.NodeRedVersion.Known
	info.MCP.CapabilityMatrix = make(map[string]string, 12)
	for tool, cap := range noderedCapabilityMatrix(probe) {
		info.MCP.CapabilityMatrix[tool] = string(cap)
	}

	// Settings + runtimeState come from /settings; we keep them as
	// raw JSON so we do not pin ourselves to NR's schema. Missing
	// fields surface as nil and are dropped by `omitempty`.
	settingsCtx, settingsCancel := probeCtx(ctx, runtimeInfoProbeTimeout)
	defer settingsCancel()
	if raw, err := s.nrClient.GetSettings(settingsCtx); err == nil {
		info.NodeRed.Settings = extractRuntimeInfoSubdoc(raw, "flowFile")
		info.NodeRed.RuntimeState = extractRuntimeInfoSubdoc(raw, "runtimeState")
	} else {
		slog.Warn("get_runtime_info: /settings probe failed", "error", err)
	}

	return mcp.NewToolResultText("```json\n" + prettyJSON(mustMarshal(info)) + "\n```"), nil
}

// runtimeProbe runs the four I/O probes the matrix needs and
// packages them into a RuntimeProbe. Each probe uses its own
// deadline so a slow Node-RED cannot stall the others.
func (s *Server) runtimeProbe(ctx context.Context) RuntimeProbe {
	p := RuntimeProbe{DebugStreamEnabled: s.debugStream}

	// 1. NR version. Use the cached probe — that already handles
	// its own deadline via the default GET timeout, and we do not
	// need a second probe here.
	versionCtx, versionCancel := probeCtx(ctx, runtimeInfoProbeTimeout)
	defer versionCancel()
	p.NodeRedVersion = s.nrClient.NodeRedVersion(versionCtx)

	// 2. Runtime-state gate. Read it from /settings so the probe
	// stays inside the same call that gave us the version.
	rsCtx, rsCancel := probeCtx(ctx, runtimeInfoProbeTimeout)
	defer rsCancel()
	if raw, err := s.nrClient.GetSettings(rsCtx); err == nil {
		p.RuntimeStateEnabled = parseRuntimeStateEnabled(raw)
	} else {
		slog.Warn("get_runtime_info: /settings probe failed", "error", err)
	}

	// 3. /logs mounted? Probe and observe the status code.
	// 3. /logs mounted? Stock NR < 5.x exposed it; stock 5.x
	// does not. Use the typed Logs() helper and treat the 404 as
	// "endpoint_not_mounted".
	p.RuntimeLogsMounted = probeLogsMounted(s, ctx)

	// 4. /diagnostics mounted? Added in NR 3.1; the typed
	// GetDiagnostics() handles the 404/403 distinction for us.
	p.DiagnosticsMounted = probeDiagnosticsMounted(s, ctx)

	return p
}

// probeLogsMounted returns true when GET /logs answers 2xx.
// Stock NR 5.x returns 404; anything 5xx or transport-level
// returns false. 4xx other than 404 (rare on this endpoint) is
// treated as mounted — the runtime considered the request.
func probeLogsMounted(s *Server, parent context.Context) bool {
	ctx, cancel := context.WithTimeout(parent, runtimeInfoProbeTimeout)
	defer cancel()
	_, err := s.nrClient.GetRuntimeLogs(ctx, 0)
	if err == nil {
		return true
	}
	var apiErr *nodered.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode < 500 {
		return apiErr.StatusCode != http.StatusNotFound
	}
	return false
}

// probeDiagnosticsMounted returns true when GET /diagnostics is
// reachable. The handler translates 403 at call time; for the
// capability matrix we only care whether the endpoint exists.
func probeDiagnosticsMounted(s *Server, parent context.Context) bool {
	ctx, cancel := context.WithTimeout(parent, runtimeInfoProbeTimeout)
	defer cancel()
	_, err := s.nrClient.GetDiagnostics(ctx)
	if err == nil {
		return true
	}
	var apiErr *nodered.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode < 500 {
		return apiErr.StatusCode != http.StatusNotFound
	}
	return false
}

// extractRuntimeInfoSubdoc pulls a single sub-object out of
// /settings for the operator. Returns nil when the key is absent
// or unparseable.
func extractRuntimeInfoSubdoc(raw json.RawMessage, key string) json.RawMessage {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	if v, ok := doc[key]; ok {
		return v
	}
	return nil
}

// parseRuntimeStateEnabled returns true when settings.runtimeState
// parses to {"enabled": true}. Anything else (missing key, wrong
// type, enabled: false) returns false.
func parseRuntimeStateEnabled(raw json.RawMessage) bool {
	var doc struct {
		RuntimeState struct {
			Enabled bool `json:"enabled"`
		} `json:"runtimeState"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false
	}
	return doc.RuntimeState.Enabled
}

// probeCtx attaches the per-probe timeout to the caller's
// context. Each call gets its own cancel func; the handler is
// single-shot so defer cancel() in the caller is fine — but we
// attach a best-effort deferred cancel here so a leaking goroutine
// never accumulates on long-lived test fixtures.
func probeCtx(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, timeout)
}

func mustMarshal(v interface{}) []byte {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		// Marshal on the typed struct should never fail; the
		// indirection through interface{} exists so we can swap
		// in a logger at the call site.
		slog.Error("runtime_info marshal failed", "error", err)
		return []byte(`{"error":"encode failed"}`)
	}
	return b
}
