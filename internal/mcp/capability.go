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

// capabilityGuidance derives a (reason, remedy) pair for a single
// non-ok capability, using only the fields on the same RuntimeProbe
// the matrix already used. ok returns ("", ""): absence is the
// signal. unknown stays explicit — we do not know what would fix
// it, so the remedy is empty rather than guessed.
//
// Pure, no I/O: lives next to the classifiers and is testable the
// same way.
//
// ponytail: guidance is derived only from RuntimeProbe fields, so
// a capability whose cause is not probed (e.g. a future per-tool
// feature flag the matrix already reports) gets a generic-but-honest
// reason rather than a fabricated one. Upgrade path: thread an
// operator-supplied hint map through RuntimeProbe when a real case
// appears, then consult it here.
func capabilityGuidance(tool string, cap Capability, p RuntimeProbe) (reason, remedy string) {
	switch cap {
	case CapOK:
		return "", ""
	case CapUnavailableUnknown:
		return "Node-RED version was not detected; capability is unknown until the runtime's version is visible to the MCP", ""
	case CapVersionTooLow:
		min, ok := MinVersionForKnown(tool)
		if !ok {
			return "Node-RED is below the minimum version required by this tool, and the tool has no registered minimum", "Upgrade Node-RED to a version that supports this tool"
		}
		return "Node-RED " + p.NodeRedVersion.String() + " is below the " + min.String() + " minimum required by " + tool,
			"Upgrade Node-RED to at least " + min.String()
	case CapSettingDisabled:
		// parseRuntimeStateEnabled collapses "enabled: false", "no
		// runtimeState key" and an unreadable /settings body into one
		// false. Only the first is a gate the operator closed, so state
		// the observation and not a verdict. ponytail: the probe cannot
		// currently distinguish the three; a tri-state on RuntimeProbe
		// would be the upgrade.
		return "the runtime-state setting did not read as enabled; settings.runtimeState.enabled was false, absent, or /settings was unreadable",
			"Set settings.runtimeState.enabled to true in settings.js (or via the runtime settings UI) and restart Node-RED"
	case CapStreamDisabled:
		// The matrix assigns stream_disabled to these tools
		// unconditionally — no classifier consults
		// p.DebugStreamEnabled, so the flag's real state is not
		// evidence about the cause. Do not assert a cause the probe
		// never checked; an operator who already set the flag would
		// otherwise follow a remedy that changes nothing.
		return "the /comms debug stream is not available to this tool",
			"Enable it with MCP_DEBUG_STREAM=on (or --debug-stream) and restart the MCP; some Node-RED versions are unstable on this WebSocket"
	case CapEndpointNotMounted:
		if !p.RuntimeLogsMounted {
			return "GET /logs is not mounted on this Node-RED (stock 5.x removed the admin endpoint)",
				"The tool will fall back to the local log file under ~/.node-red/"
		}
		return "the admin endpoint this tool depends on is not mounted on this Node-RED",
			"Re-enable the endpoint on the Node-RED side, or use a tool that does not require it"
	default:
		return "capability " + string(cap) + " is not currently available", ""
	}
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
