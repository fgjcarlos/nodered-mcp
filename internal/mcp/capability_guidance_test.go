package mcp

import (
	"strings"
	"testing"
)

// Old runtime: every version-gated tool gets a reason naming the
// real minimum from nodered_min_version_for, and the reason MUST
// change when the tool changes. A generic message that ignores
// the tool is the failure mode the issue names.
func TestCapabilityGuidance_OldRuntimeNamesCorrectMinPerTool(t *testing.T) {
	p := RuntimeProbe{NodeRedVersion: v(3, 0, 0), RuntimeStateEnabled: true}

	// set_context: minimum is 5.0.0
	if r, _ := capabilityGuidance("set_context", CapVersionTooLow, p); !strings.Contains(r, "5.0.0") {
		t.Errorf("set_context reason should name 5.0.0 minimum, got %q", r)
	}
	// get_diagnostics: minimum is 3.1.0
	if r, _ := capabilityGuidance("get_diagnostics", CapVersionTooLow, p); !strings.Contains(r, "3.1.0") {
		t.Errorf("get_diagnostics reason should name 3.1.0 minimum, got %q", r)
	}
	// inject_node: minimum is 5.0.0
	if r, _ := capabilityGuidance("inject_node", CapVersionTooLow, p); !strings.Contains(r, "5.0.0") {
		t.Errorf("inject_node reason should name 5.0.0 minimum, got %q", r)
	}

	// And the two minimums must actually differ when the tool differs.
	rSet, _ := capabilityGuidance("set_context", CapVersionTooLow, p)
	rDiag, _ := capabilityGuidance("get_diagnostics", CapVersionTooLow, p)
	if rSet == rDiag {
		t.Errorf("set_context and get_diagnostics produced identical reasons (%q); guidance must be tool-specific", rSet)
	}
}

// Current runtime: every tool reports ok, and the guidance function
// returns no reason and no remedy for ok. Absence is the signal.
func TestCapabilityGuidance_OkCarriesNothing(t *testing.T) {
	p := RuntimeProbe{
		NodeRedVersion:      v(5, 0, 1),
		RuntimeStateEnabled: true,
		DebugStreamEnabled:  true,
		RuntimeLogsMounted:  true,
		DiagnosticsMounted:  true,
	}
	for _, tool := range []string{"set_context", "get_diagnostics", "inject_node", "get_flows_state", "get_node_status", "get_debug_messages", "get_runtime_logs"} {
		if r, m := capabilityGuidance(tool, CapOK, p); r != "" || m != "" {
			t.Errorf("%s at ok: want (\"\", \"\"), got (%q, %q)", tool, r, m)
		}
	}
}

// Unknown NR version: every version-gated tool reports unknown,
// and the reason must be honest — we did not detect the version,
// so we must NOT claim the tool is unavailable or available. The
// remedy stays empty because we do not know what would fix it.
func TestCapabilityGuidance_UnknownRuntimeIsExplicit(t *testing.T) {
	p := RuntimeProbe{RuntimeStateEnabled: true}
	for _, tool := range []string{"set_context", "get_diagnostics", "inject_node"} {
		r, m := capabilityGuidance(tool, CapUnavailableUnknown, p)
		if r == "" {
			t.Errorf("%s: unknown reason must be non-empty (the issue requires unknown to be visible)", tool)
		}
		// Reason must not claim the tool is unavailable or available.
		low := strings.ToLower(r)
		if strings.Contains(low, "unavailable") || strings.Contains(low, "not supported") || strings.Contains(low, "not available") || strings.Contains(low, "ok") {
			t.Errorf("%s: unknown reason must not state a fact about availability, got %q", tool, r)
		}
		// Remedy is allowed to be empty — we don't know what to do.
		_ = m
	}
}

// Setting-disabled reason must name the actual setting path that
// was parsed: settings.runtimeState.enabled. The probe comes from
// /settings, not from /flows/state, and the guidance has to say so.
func TestCapabilityGuidance_SettingDisabledNamesRuntimeState(t *testing.T) {
	p := RuntimeProbe{NodeRedVersion: v(5, 0, 1), RuntimeStateEnabled: false}
	for _, tool := range []string{"get_flows_state", "set_flows_state"} {
		r, m := capabilityGuidance(tool, CapSettingDisabled, p)
		if !strings.Contains(r, "settings.runtimeState.enabled") {
			t.Errorf("%s reason should name settings.runtimeState.enabled, got %q", tool, r)
		}
		if m == "" {
			t.Errorf("%s remedy should be non-empty: changing the setting is a concrete next step", tool)
		}
	}
}

// Endpoint-not-mounted reason must name the actual endpoint. The
// probe is GET /logs.
func TestCapabilityGuidance_EndpointNotMountedNamesLogs(t *testing.T) {
	p := RuntimeProbe{NodeRedVersion: v(5, 0, 1), RuntimeLogsMounted: false}
	r, m := capabilityGuidance("get_runtime_logs", CapEndpointNotMounted, p)
	if !strings.Contains(r, "/logs") {
		t.Errorf("reason should name /logs, got %q", r)
	}
	// The tool has a real fallback (local log file), so the remedy
	// should mention it — that is the concrete next step.
	if m == "" {
		t.Error("remedy should be non-empty: the tool falls back to local logs")
	}
}

// Stream-disabled reason must name the MCP-side flag in the REMEDY, not
// blame Node-RED settings — DebugStreamEnabled is s.debugStream, set by
// MCP_DEBUG_STREAM.
//
// It must also NOT claim the flag is off. The matrix assigns
// stream_disabled unconditionally (no classifier reads
// p.DebugStreamEnabled), so the flag's state is not evidence about the
// cause: asserting "the flag is off" would send an operator who already
// set it after a remedy that changes nothing.
func TestCapabilityGuidance_StreamDisabledNamesMCPFlag(t *testing.T) {
	// Both flag states must produce the same reason, because the matrix
	// does not consult the flag.
	on := RuntimeProbe{DebugStreamEnabled: true}
	off := RuntimeProbe{DebugStreamEnabled: false}

	for _, tool := range []string{"get_node_status", "get_debug_messages"} {
		r, m := capabilityGuidance(tool, CapStreamDisabled, on)
		rOff, _ := capabilityGuidance(tool, CapStreamDisabled, off)

		if r == "" {
			t.Errorf("%s reason should be non-empty", tool)
		}
		if r != rOff {
			t.Errorf("%s reason must not depend on the debug-stream flag "+
				"(the matrix ignores it): on=%q off=%q", tool, r, rOff)
		}
		// Must not assert a cause the probe never checked.
		lower := strings.ToLower(r)
		for _, claim := range []string{"is off", "is disabled", "is not connected", "not connected"} {
			if strings.Contains(lower, claim) {
				t.Errorf("%s reason asserts %q, but the matrix classifies "+
					"stream_disabled regardless of the flag: %q", tool, claim, r)
			}
		}
		// The remedy points at the MCP-side flag.
		if !strings.Contains(strings.ToLower(m), "mcp_debug_stream") {
			t.Errorf("%s remedy should name MCP_DEBUG_STREAM, got %q", tool, m)
		}
		if m == "" {
			t.Errorf("%s remedy should be non-empty", tool)
		}
	}
}

// The setting-disabled reason must not claim the operator closed the
// gate. parseRuntimeStateEnabled returns false for three different
// causes — enabled:false, a missing runtimeState key, and an
// unreadable /settings body — and the probe cannot tell them apart.
func TestCapabilityGuidance_SettingDisabledDoesNotClaimAClosedGate(t *testing.T) {
	for _, tool := range []string{"get_flows_state", "set_flows_state"} {
		r, _ := capabilityGuidance(tool, CapSettingDisabled, RuntimeProbe{})
		lower := strings.ToLower(r)
		if strings.Contains(lower, "is false") || strings.Contains(lower, "gate is closed") {
			t.Errorf("%s reason asserts a cause the probe cannot confirm: %q", tool, r)
		}
		// The setting key is camelCase in the real Node-RED payload;
		// compare case-insensitively.
		if !strings.Contains(strings.ToLower(r), strings.ToLower("runtimeState.enabled")) {
			t.Errorf("%s reason should name the setting it reads, got %q", tool, r)
		}
	}
}

// Version-too-low remedy must name the real minimum. set_context's
// minimum is 5.0.0, get_diagnostics' is 3.1.0 — the remedy must
// follow.
func TestCapabilityGuidance_VersionTooLowRemedyNamesMin(t *testing.T) {
	p := RuntimeProbe{NodeRedVersion: v(3, 0, 0)}
	if _, m := capabilityGuidance("set_context", CapVersionTooLow, p); !strings.Contains(m, "5.0.0") {
		t.Errorf("set_context remedy should name 5.0.0, got %q", m)
	}
	if _, m := capabilityGuidance("get_diagnostics", CapVersionTooLow, p); !strings.Contains(m, "3.1.0") {
		t.Errorf("get_diagnostics remedy should name 3.1.0, got %q", m)
	}
}

// Unknown tool name: even when the matrix contains a tool not in
// nodered_min_version_for, the guidance function must not panic and
// must return SOMETHING honest. The version case uses the
// tool-agnostic unknown reason; the matrix does not assert this
// (noderedCapabilityMatrix only classifies tools it knows) but the
// handler is a separate loop and could be wrong.
func TestCapabilityGuidance_UnknownToolName(t *testing.T) {
	p := RuntimeProbe{NodeRedVersion: v(3, 0, 0)}
	r, _ := capabilityGuidance("not_a_real_tool", CapVersionTooLow, p)
	// With no entry in nodered_min_version_for, we cannot name a
	// specific minimum. The honest reason is that we do not know
	// the minimum for this tool.
	if r == "" {
		t.Error("unknown tool should still get a non-empty reason (the matrix owes the operator an explanation)")
	}
}
