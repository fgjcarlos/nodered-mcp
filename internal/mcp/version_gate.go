package mcp

import (
	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// nodered_min_version_for maps a tool name to the Node-RED version
// that first shipped the behaviour the tool relies on. The map is
// the single source of truth — registerTools reads it to append
// "requires NR >= X.Y" to each tool's description, the startup
// banner reads it to log which tools are unavailable on the
// running Node-RED, and get_runtime_info (issue #168) reads it to
// build the capability matrix.
//
// Tools with no entry work on every Node-RED 1.x onwards and do
// not need an annotation. Keep this map short — only the tools
// with a *real* minimum version live here. The full audit list
// is in docs/audit-2026-08-03.md §4.1-§4.3.
//
// Entries must be sorted by version (newest first) so the table
// reads as a story: a tool required NR 5.0 first, an earlier
// tool only needed 3.1.
var nodered_min_version_for = map[string]nodered.Version{
	"set_context":     nodered.Version{Major: 5, Minor: 0, Patch: 0, Known: true, Raw: "5.0.0"}, // helper inject body trick
	"inject_node":     nodered.Version{Major: 5, Minor: 0, Patch: 0, Known: true, Raw: "5.0.0"}, // __user_inject_props__ payload override
	"get_diagnostics": nodered.Version{Major: 3, Minor: 1, Patch: 0, Known: true, Raw: "3.1.0"}, // /diagnostics endpoint added
}

// MinVersionFor returns the minimum Node-RED version a tool
// requires, or the zero Version (Known == false) if the tool has
// no minimum and works on every supported Node-RED release.
func MinVersionFor(toolName string) nodered.Version {
	if v, ok := nodered_min_version_for[toolName]; ok {
		return v
	}
	return nodered.Version{}
}

// MinVersionForKnown is a convenience that returns (version, ok)
// so callers can distinguish "no minimum" from "minimum is the
// zero Version" if they ever need to.
func MinVersionForKnown(toolName string) (nodered.Version, bool) {
	v, ok := nodered_min_version_for[toolName]
	return v, ok
}

// MinVersionAnnotation returns the human-readable annotation
// appended to a tool description, e.g. " (requires NR ≥ 5.0.0)".
// Returns "" if the tool has no minimum.
func MinVersionAnnotation(toolName string) string {
	v := MinVersionFor(toolName)
	if !v.Known {
		return ""
	}
	return " (requires NR \u2265 " + v.String() + ")"
}
