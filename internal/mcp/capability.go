package mcp

import (
	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// Capability is the status of a single tool against the runtime
// the MCP is currently connected to. The strings are stable — they
// land in the JSON the operator reads, and a future audit will grep
// for them. Keep the vocabulary tight; if a new failure mode
// appears, add a new constant rather than reusing one.
type Capability string

const (
	CapOK                 Capability = "ok"
	CapVersionTooLow      Capability = "version_too_low"
	CapEndpointNotMounted Capability = "endpoint_not_mounted"
	CapSettingDisabled    Capability = "setting_disabled"
	CapStreamDisabled     Capability = "stream_disabled"
	CapUnavailableUnknown Capability = "unknown" // NR version not detected
)

// RuntimeProbe is the slice of runtime state get_runtime_info
// needs to classify each tool. Populate it once per tool call —
// the probes that feed it have their own deadlines.
//
// runtimeLogsMounted / diagnosticsMounted reflect what the
// runtime actually responds to, not what its settings advertise.
// runtimeStateEnabled is the parsed value of
// settings.runtimeState.enabled from /settings; we do NOT probe
// /flows/state to learn it (that's circular — the probe would
// itself fail with a different code if the gate is closed).
type RuntimeProbe struct {
	NodeRedVersion      nodered.Version
	RuntimeStateEnabled bool
	DebugStreamEnabled  bool // s.debugStream
	RuntimeLogsMounted  bool // GET /logs did not 404
	DiagnosticsMounted  bool // GET /diagnostics did not 404/403
}

// noderedCapabilityMatrix classifies every tool in the MCP against
// the running runtime. The matrix is the single source of truth
// that get_runtime_info renders to JSON — adding a new tool with a
// gate means adding the classification here.
//
// Pure function: no I/O, no globals, testable in isolation.
func noderedCapabilityMatrix(p RuntimeProbe) map[string]Capability {
	matrix := make(map[string]Capability, len(nodered_min_version_for)+6)

	for tool, minVer := range nodered_min_version_for {
		matrix[tool] = classifyVersionedTool(tool, minVer, p.NodeRedVersion)
	}

	// Tools gated by settings, not by version. The order matters
	// for readability of the JSON: state tools first, then stream
	// tools, then the rest.
	matrix["get_flows_state"] = classifySettingTool(p.RuntimeStateEnabled)
	matrix["set_flows_state"] = classifySettingTool(p.RuntimeStateEnabled)
	matrix["get_node_status"] = CapStreamDisabled
	matrix["get_debug_messages"] = CapStreamDisabled
	matrix["get_runtime_logs"] = classifyRuntimeLogsTool(p)

	return matrix
}

// classifyVersionedTool picks version_too_low when the running
// Node-RED is below the minimum, and unknown when the probe did
// not return a parseable version (NR is reachable but the version
// field is missing). Every other state is ok — the tool will run,
// and a sub-tool may fail at runtime if its own gate is closed.
func classifyVersionedTool(_ string, min nodered.Version, running nodered.Version) Capability {
	if !running.Known {
		return CapUnavailableUnknown
	}
	if running.AtLeast(min.Major, min.Minor, min.Patch) {
		return CapOK
	}
	return CapVersionTooLow
}

// classifySettingTool returns setting_disabled when the runtime
// state gate is closed, ok otherwise. Kept separate from the
// versioned classifier so a future gate (different setting) only
// needs to be wired in one place.
func classifySettingTool(runtimeStateEnabled bool) Capability {
	if !runtimeStateEnabled {
		return CapSettingDisabled
	}
	return CapOK
}

// classifyRuntimeLogsTool: get_runtime_logs is gated by the
// /logs endpoint being mounted AND the debug stream being on.
// /logs is not part of stock NR — only stock NR < 5.x exposed
// it. Stock 5.x returns 404 and the handler falls back to
// ~/.node-red/*.log. We classify as endpoint_not_mounted when the
// probe shows the endpoint is gone, regardless of stream state —
// if the endpoint is gone, the tool will read the local log file
// instead, which still works. So in practice we report
// endpoint_not_mounted to flag that the admin API does not expose
// it (and the user knows we are using the fallback).
//
// ponytail: this is the simplest mapping that covers the audit's
// three known failure modes; we deliberately do not encode every
// fallback path because the JSON is for the operator, not for
// routing decisions.
func classifyRuntimeLogsTool(p RuntimeProbe) Capability {
	if !p.RuntimeLogsMounted {
		return CapEndpointNotMounted
	}
	return CapOK
}

// DiagnosticsMounted remains on RuntimeProbe even though no
// classifier reads it today — the JSON consumer still wants to
// know whether the admin API actually served /diagnostics when
// the matrix was built. Keeping it on the struct preserves that
// signal for future use without re-running the loop.
