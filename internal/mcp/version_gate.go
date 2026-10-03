package mcp

import (
	"fmt"

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

// RefuseForVersion is the single point that translates "is this
// tool allowed to run against the running Node-RED?" into a
// (refuse, msg) pair. It is the runtime enforcement companion to
// the description-level annotation produced by MinVersionAnnotation.
//
// The policy is explicit, not inferred:
//
//   - tool has no minimum: never refuse. A tool absent from
//     nodered_min_version_for works on every supported NR; an old
//     detected version is not evidence of incompatibility.
//
//   - tool has a minimum AND running version is known AND
//     running >= minimum: do not refuse.
//
//   - tool has a minimum AND running version is known AND
//     running < minimum: refuse, return an actionable message
//     that names the required minimum and the detected version
//     so the operator can decide whether to upgrade NR or
//     downgrade the call site.
//
//   - tool has a minimum AND running version is NOT known:
//     do NOT refuse. /settings is frequently authenticated, slow
//     or absent; treating "could not tell" as "old NR" would
//     break a runtime that actually supports the tool. The
//     success path is responsible for telling the caller the
//     call is not backed by a capability check (see
//     unknownVersionNotice).
//
// ponytail: this is the smallest decision surface that
// satisfies the four cases above without inventing a successful
// capability check. A per-tool successful probe (e.g. an
// admin endpoint NR exposes only on 5.x) would let the
// unknown branch also refuse, but that probe is explicitly
// out of scope and would not change the behaviour of the
// three other branches.
func RefuseForVersion(toolName string, running nodered.Version) (refuse bool, msg string) {
	min, ok := MinVersionForKnown(toolName)
	if !ok {
		// No minimum registered: the tool works on every
		// supported NR, so no running version — known or
		// not — is grounds for refusal.
		return false, ""
	}
	if !running.Known {
		// Policy: do not refuse on absence of evidence. The
		// success path appends unknownVersionNotice so the
		// caller knows no gate was applied.
		return false, ""
	}
	if running.AtLeast(min.Major, min.Minor, min.Patch) {
		return false, ""
	}
	return true, fmt.Sprintf(
		"Node-RED %s is below the %s minimum required by %s; upgrade Node-RED to at least %s",
		running.String(), min.String(), toolName, min.String(),
	)
}

// UnknownVersionNotice is the suffix appended to a success
// result when the call proceeded without a version gate (the
// running version was not detected). It says what the issue
// asks for: the call succeeded but the caller should know no
// guarantee was applied. Kept short so it fits inside the
// existing success message; tightened wording would only
// matter if a model started to misread it.
const UnknownVersionNotice = " (Node-RED version was not detected; this call was not verified against the minimum version)"
