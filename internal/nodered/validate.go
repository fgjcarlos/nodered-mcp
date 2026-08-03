package nodered

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Structural checks over a single flow tab document.
//
// validate_flow runs these against an in-memory document the MCP has not yet
// written to the runtime, so a "would this break?" answer costs zero HTTP and
// zero state. Each issue is one small thing an operator can fix; the caller
// (the MCP tool) formats them as a list, the model reads the list, and the
// next edit avoids the same mistakes.
//
// The checks duplicate what AddNode / UpdateNode / ConnectNodes already refuse
// on the write path; that is deliberate — the whole point is to surface the
// rejection BEFORE the write, not to invent new rules. Anything a write would
// accept must be reported as a non-issue here.

// FlowIssueKind classifies a structural problem found in a flow document.
type FlowIssueKind string

const (
	// IssueMissingID is a node with no "id" field. Wires reference nodes by id,
	// so an id-less node is unreachable from anywhere — typical of an LLM that
	// emitted a partial node object.
	IssueMissingID FlowIssueKind = "missing_id"
	// IssueDuplicateID is two nodes in the same tab sharing an id. Node-RED
	// accepts the duplicate and then dispatches based on ordering, which is a
	// bug that surfaces far from its cause.
	IssueDuplicateID FlowIssueKind = "duplicate_id"
	// IssueMissingXY is a node without canvas coordinates. Node-RED files such
	// a node in the configs collection, where it is never deployed — the flow
	// looks fine and does nothing.
	IssueMissingXY FlowIssueKind = "missing_xy"
	// IssueDanglingWire is a wire whose target id does not resolve to any
	// node in the same document. Node-RED accepts these in silence and the
	// flow simply never delivers to the missing endpoint.
	IssueDanglingWire FlowIssueKind = "dangling_wire"
	// IssueInvalidWires is a node whose "wires" field is not the shape
	// Node-RED expects (an array of arrays of target ids). Common when an
	// LLM hands back a single string or a number. The previous validator
	// silently skipped such nodes, which let the corruption be written to
	// the runtime and break every later edit through the MCP.
	IssueInvalidWires FlowIssueKind = "invalid_wires"
	// IssueBadZ is a node whose "z" field references neither the owning
	// tab nor any existing node in this document. Node-RED silently
	// rewrites a bad z to the owning tab id, which loses the wire the
	// model intended (issue #99). Reported on the validate_flow tool and
	// rejected by validateFlowWires so update_flow / set_flows fail loud.
	IssueBadZ FlowIssueKind = "bad_z"
)

// FlowIssue describes one structural problem in a flow document. NodeID is the
// id of the offending node when one exists; "" for a document-level issue.
// Target is set on IssueDanglingWire to the unresolved wire target id, so
// callers do not have to re-parse Message to recover the data they need.
// Message is the operator-facing explanation, suitable for surfacing verbatim.
type FlowIssue struct {
	Kind    FlowIssueKind `json:"kind"`
	NodeID  string        `json:"nodeId,omitempty"`
	Target  string        `json:"target,omitempty"`
	Message string        `json:"message"`
}

// ValidateFlow returns every structural issue it finds in a flow document,
// in document order. The empty slice means "this is safe to write". The
// function never errors on the document itself; a malformed document is
// reported as a single document-level issue and whatever else can be collected.
//
// The function is read-only: it takes the same RawFlow bytes the write path
// takes, walks them once, and returns. Reuses the existing decodeFlow /
// locate / hasCanvasPosition helpers so the rules stay in lock-step with the
// write-path guards.
//
// Note on x/y: only nodes in the "nodes" collection (those carrying canvas
// coordinates) are required to have x/y. Nodes in "configs" deliberately
// lack them — that is how Node-RED decides between the two collections in
// the first place. Flagging a config node as missing_xy would mark every
// real-world tab as broken, so the check is scoped to nodes.
func ValidateFlow(flow RawFlow) []FlowIssue {
	doc, err := decodeFlow(flow)
	if err != nil {
		return []FlowIssue{{
			Kind:    "invalid_document",
			Message: err.Error(),
		}}
	}

	var issues []FlowIssue
	// seen[id] = true once a node has been recorded. The first occurrence
	// is accepted silently; the second trips IssueDuplicateID. Both
	// collections contribute to "seen" so a config and a canvas node cannot
	// share an id either.
	seen := make(map[string]bool, len(doc.Nodes)+len(doc.Configs))

	for _, node := range doc.Nodes {
		if issue := checkNode(node, seen, true); issue != nil {
			issues = append(issues, *issue)
		}
		if issue := checkZRef(node, doc); issue != nil {
			issues = append(issues, *issue)
		}
	}
	for _, node := range doc.Configs {
		if issue := checkNode(node, seen, false); issue != nil {
			issues = append(issues, *issue)
		}
		if issue := checkZRef(node, doc); issue != nil {
			issues = append(issues, *issue)
		}
	}

	// Wires are validated against the union of both collections.
	ids := make(map[string]bool, len(seen))
	for id := range seen {
		ids[id] = true
	}
	issues = append(issues, validateWires(doc.Nodes, doc.Configs, ids)...)

	if issues == nil {
		issues = []FlowIssue{}
	}
	return issues
}

// checkNode returns the first issue found in node, or nil. Records the id
// in seen so the duplicate check has the picture of the document by the time
// it runs. requireXY controls whether a missing x/y is reported — true for
// canvas nodes (those that must carry canvas coordinates), false for config
// nodes (which deliberately lack them).
func checkNode(node map[string]json.RawMessage, seen map[string]bool, requireXY bool) *FlowIssue {
	id := nodeID(node)
	if id == "" {
		return &FlowIssue{
			Kind:    IssueMissingID,
			Message: "a node has no id; wires reference nodes by id and an id-less node is unreachable",
		}
	}
	if seen[id] {
		return &FlowIssue{
			Kind:    IssueDuplicateID,
			NodeID:  id,
			Message: "two nodes share id \"" + id + "\" — Node-RED accepts the duplicate and dispatches based on ordering, which produces a flow whose behaviour depends on which copy arrives first",
		}
	}
	seen[id] = true

	if requireXY && !hasCanvasPosition(node) {
		return &FlowIssue{
			Kind:    IssueMissingXY,
			NodeID:  id,
			Message: "node has no x/y canvas coordinates; Node-RED files it into configs and the runtime will not deploy it (a typical inject node carries x:140,y:140)",
		}
	}
	return nil
}

func wireDanglingMessage(src string, port int, tgt string) string {
	if src == "" {
		return "a wire on port " + strconv.Itoa(port) + " targets unknown node \"" + tgt + "\""
	}
	return "node \"" + src + "\" wires port " + strconv.Itoa(port) + " to unknown node \"" + tgt + "\"; Node-RED accepts dangling wires silently and the flow never delivers to that endpoint"
}

// validateWires walks every node in nodes+configs, reports wires whose
// shape decodeWires rejects (IssueInvalidWires) and wires that point at
// ids absent from the union of both collections (IssueDanglingWire).
// Returns the list of issues; never nil. Extracted from ValidateFlow
// so each function stays under the complexity-15 threshold (issue #73).
func validateWires(nodes, configs []map[string]json.RawMessage, ids map[string]bool) []FlowIssue {
	var issues []FlowIssue
	for _, collection := range [][]map[string]json.RawMessage{nodes, configs} {
		for _, node := range collection {
			rawWires, ok := node["wires"]
			if !ok {
				continue
			}
			wires, err := decodeWires(node)
			if err != nil {
				// decodeWires refused the shape (e.g. wires is a JSON
				// string instead of an array of arrays). Surfacing the
				// problem here is the whole point of validate_flow:
				// the previous behaviour was a silent `continue`, which
				// let the corrupted node be written and broke every
				// later edit.
				id := nodeID(node)
				issues = append(issues, FlowIssue{
					Kind:    IssueInvalidWires,
					NodeID:  id,
					Message: invalidWiresMessage(id, rawWires),
				})
				continue
			}
			if wires == nil {
				continue
			}
			src := nodeID(node)
			for port, targets := range wires {
				for _, tgt := range targets {
					if !ids[tgt] {
						issues = append(issues, FlowIssue{
							Kind:    IssueDanglingWire,
							NodeID:  src,
							Target:  tgt,
							Message: wireDanglingMessage(src, port, tgt),
						})
					}
				}
			}
		}
	}
	return issues
}

// checkZRef returns an IssueBadZ if node's z field references neither the
// owning tab nor any existing node in this document. A node with no z (an
// empty string) is treated as valid — flows that explicitly clear z are
// served by the rest of the rules. Mirrors the check the add / update
// write paths run on edits so the validator and the write path agree
// (issue #99).
func checkZRef(node map[string]json.RawMessage, doc *flowDoc) *FlowIssue {
	z := stringField(node, "z")
	if z == "" {
		return nil
	}
	if tabID := stringField(doc.Extra, "id"); z == tabID {
		return nil
	}
	if doc.exists(z) {
		return nil
	}
	return &FlowIssue{
		Kind:    IssueBadZ,
		NodeID:  nodeID(node),
		Message: badZMessage(nodeID(node), z),
	}
}

func badZMessage(id, z string) string {
	if id == "" {
		return fmt.Sprintf("a node references z=%q which is neither the owning tab nor an existing node in this flow: Node-RED's runtime will crash on deploy with 'Cannot read properties of undefined (reading wires)' if this is written", z)
	}
	return fmt.Sprintf("node %q references z=%q which is neither the owning tab nor an existing node in this flow: Node-RED's runtime will crash on deploy with 'Cannot read properties of undefined (reading wires)' if this is written", id, z)
}

// ValidateFlows validates every tab in raw and aggregates the issues.
//
// raw is one of:
//
//   - a single nested tab doc (the shape ValidateFlow already accepts);
//   - a flat array of tabs and nodes (the shape GET /flows returns).
//
// Issue #413: create_flow / update_flow already route their input through
// normalizeFlowDoc, which accepts a flat-array payload and splits it into
// per-tab documents. validate_flow used to call ValidateFlow on the raw
// bytes, so the same flat-array payload that the write tools accept was
// rejected here as invalid_document. This function closes that gap by
// iterating the tabs in a flat array and validating each one.
//
// An empty array, or a flat array with no "type":"tab" element, falls
// through to ValidateFlow, which reports a single invalid_document issue.
// That keeps the "this is not a flow document" answer for genuinely
// broken input.
func ValidateFlows(raw RawFlow) []FlowIssue {
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		// Not a JSON array; treat as a single tab doc.
		return ValidateFlow(raw)
	}
	var tabIDs []string
	for _, item := range arr {
		var meta struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(item, &meta); err == nil && meta.Type == "tab" && meta.ID != "" {
			tabIDs = append(tabIDs, meta.ID)
		}
	}
	if len(tabIDs) == 0 {
		// A flat array with no tabs is not a valid flow document; let
		// ValidateFlow surface that as a single invalid_document issue.
		return ValidateFlow(raw)
	}
	var all []FlowIssue
	for _, id := range tabIDs {
		nested, ok := synthesizeFlowFromFlat(raw, id)
		if !ok {
			all = append(all, FlowIssue{
				Kind:    "invalid_document",
				Message: "no tab with id \"" + id + "\" in the supplied flat flow array",
			})
			continue
		}
		all = append(all, ValidateFlow(nested)...)
	}
	if all == nil {
		all = []FlowIssue{}
	}
	return all
}

// jsonTypeOf reports the JSON shape a raw value carries — "object", "array",
// "string", "number", "boolean", "null", or "empty". Used to surface a useful
// message when a node's wires field is the wrong shape (the most common case
// is a single string like "not-an-array" produced by an LLM). It only inspects
// the first non-whitespace byte, which is enough to disambiguate the JSON
// grammar without paying for a full decode.
func jsonTypeOf(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if len(s) == 0 {
		return "empty"
	}
	switch s[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	default:
		return "number"
	}
}

// invalidWiresMessage formats a FlowIssue message for a node whose wires
// field is the wrong shape. Names the node id when one is present, and
// always reports the actual JSON type encountered so the operator can fix
// it without re-parsing the document.
func invalidWiresMessage(id string, raw json.RawMessage) string {
	got := jsonTypeOf(raw)
	if id == "" {
		return fmt.Sprintf("a node has invalid wires (expected array of arrays, got %s); Node-RED will reject the whole flow at deploy time", got)
	}
	return fmt.Sprintf("node %q has invalid wires (expected array of arrays, got %s); Node-RED will reject the whole flow at deploy time", id, got)
}
