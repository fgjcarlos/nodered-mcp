package nodered

import (
	"encoding/json"
	"strconv"
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
	}
	for _, node := range doc.Configs {
		if issue := checkNode(node, seen, false); issue != nil {
			issues = append(issues, *issue)
		}
	}

	// Wires are validated against the union of both collections.
	ids := make(map[string]bool, len(seen))
	for id := range seen {
		ids[id] = true
	}
	for _, collection := range [][]map[string]json.RawMessage{doc.Nodes, doc.Configs} {
		for _, node := range collection {
			wires, err := decodeWires(node)
			if err != nil || wires == nil {
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
