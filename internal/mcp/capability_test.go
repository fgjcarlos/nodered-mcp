package mcp

import (
	"testing"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

func v(maj, min, pat int) nodered.Version {
	return nodered.Version{Major: maj, Minor: min, Patch: pat, Known: true}
}

// Stock NR 5.0.1 with debug-stream off: get_runtime_logs and the
// /comms tail tools should classify accordingly; everything else
// should be ok.
func TestCapabilityMatrix_NR5Stock(t *testing.T) {
	p := RuntimeProbe{
		NodeRedVersion:      v(5, 0, 1),
		RuntimeStateEnabled: true,
		DebugStreamEnabled:  false,
		RuntimeLogsMounted:  false, // stock 5.x lacks /logs
		DiagnosticsMounted:  true,
	}
	m := noderedCapabilityMatrix(p)
	if got := m["get_diagnostics"]; got != CapOK {
		t.Errorf("get_diagnostics on 5.0.1 = %q, want ok", got)
	}
	if got := m["inject_node"]; got != CapOK {
		t.Errorf("inject_node on 5.0.1 = %q, want ok", got)
	}
	if got := m["set_context"]; got != CapOK {
		t.Errorf("set_context on 5.0.1 = %q, want ok", got)
	}
	if got := m["get_runtime_logs"]; got != CapEndpointNotMounted {
		t.Errorf("get_runtime_logs on stock 5.x = %q, want endpoint_not_mounted", got)
	}
	if got := m["get_node_status"]; got != CapStreamDisabled {
		t.Errorf("get_node_status with debugStream off = %q, want stream_disabled", got)
	}
	if got := m["get_debug_messages"]; got != CapStreamDisabled {
		t.Errorf("get_debug_messages with debugStream off = %q, want stream_disabled", got)
	}
	if got := m["get_flows_state"]; got != CapOK {
		t.Errorf("get_flows_state with runtimeState on = %q, want ok", got)
	}
}

// NR 3.0.0 — below the get_diagnostics minimum. Other version-gated
// tools report version_too_low too.
func TestCapabilityMatrix_NR3(t *testing.T) {
	p := RuntimeProbe{
		NodeRedVersion:      v(3, 0, 0),
		RuntimeStateEnabled: true,
		DiagnosticsMounted:  false, // endpoint not mounted below 3.1
	}
	m := noderedCapabilityMatrix(p)
	if got := m["get_diagnostics"]; got != CapVersionTooLow {
		t.Errorf("get_diagnostics on 3.0.0 = %q, want version_too_low", got)
	}
	if got := m["inject_node"]; got != CapVersionTooLow {
		t.Errorf("inject_node on 3.0.0 = %q, want version_too_low", got)
	}
	if got := m["set_context"]; got != CapVersionTooLow {
		t.Errorf("set_context on 3.0.0 = %q, want version_too_low", got)
	}
}

// Unknown NR version (probe failed) — every version-gated tool
// reports unknown so the operator knows the MCP could not tell.
func TestCapabilityMatrix_UnknownVersion(t *testing.T) {
	p := RuntimeProbe{RuntimeStateEnabled: true}
	m := noderedCapabilityMatrix(p)
	for _, tool := range []string{"get_diagnostics", "inject_node", "set_context"} {
		if got := m[tool]; got != CapUnavailableUnknown {
			t.Errorf("%s on unknown NR = %q, want unknown", tool, got)
		}
	}
}

// NR >= 5.0 with runtimeState disabled — state tools classify as
// setting_disabled; version-gated tools still ok.
func TestCapabilityMatrix_RuntimeStateOff(t *testing.T) {
	p := RuntimeProbe{
		NodeRedVersion:      v(5, 0, 1),
		RuntimeStateEnabled: false,
		RuntimeLogsMounted:  false,
		DiagnosticsMounted:  true,
	}
	m := noderedCapabilityMatrix(p)
	if got := m["get_flows_state"]; got != CapSettingDisabled {
		t.Errorf("get_flows_state with runtimeState off = %q, want setting_disabled", got)
	}
	if got := m["set_flows_state"]; got != CapSettingDisabled {
		t.Errorf("set_flows_state with runtimeState off = %q, want setting_disabled", got)
	}
	if got := m["get_diagnostics"]; got != CapOK {
		t.Errorf("get_diagnostics on 5.0.1 with runtimeState off = %q, want ok", got)
	}
}

// Sanity: the matrix includes every entry in
// nodered_min_version_for (the audit said: if a tool has a
// minimum, the matrix must reflect it).
func TestCapabilityMatrix_CoversGateTable(t *testing.T) {
	p := RuntimeProbe{NodeRedVersion: v(5, 0, 1), RuntimeStateEnabled: true, DiagnosticsMounted: true}
	m := noderedCapabilityMatrix(p)
	for tool := range nodered_min_version_for {
		if _, ok := m[tool]; !ok {
			t.Errorf("capability matrix missing entry for gated tool %q", tool)
		}
	}
}
