package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// prettyJSON indents raw JSON for display, falling back to the raw bytes if
// it doesn't parse.
func prettyJSON(raw []byte) string {
	if len(raw) == 0 {
		return "{}"
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}

// flowParam reads a "flow" argument as either a JSON-encoded string, a
// flow object directly, or a flat array of tabs and nodes (the shape
// GET /flows returns). All three end up as json.RawMessage the handler
// can hand to the validator. The flat-array case is what validate_flow
// needs to accept the same payload create_flow / update_flow already do
// (issue #413); a flat array passed to create_flow / update_flow still
// falls through to normalizeFlowDoc, which returns a clear "pass the
// flow id" error when the array is supplied without one.

// flowParam reads a "flow" argument as either a JSON-encoded string, a
// flow object directly, or a flat array of tabs and nodes (the shape
// GET /flows returns). All three end up as json.RawMessage the handler
// can hand to the validator. The flat-array case is what validate_flow
// needs to accept the same payload create_flow / update_flow already do
// (issue #413); a flat array passed to create_flow / update_flow still
// falls through to normalizeFlowDoc, which returns a clear "pass the
// flow id" error when the array is supplied without one.
func flowParam(req mcp.CallToolRequest, key string) (json.RawMessage, error) {
	args := req.GetArguments()
	v, ok := args[key]
	if !ok {
		return nil, fmt.Errorf("required argument %q not found", key)
	}
	switch x := v.(type) {
	case string:
		raw := json.RawMessage(x)
		if !json.Valid(raw) {
			return nil, fmt.Errorf("%q must be a JSON-encoded flow document or a flow document passed directly", key)
		}
		return raw, nil
	case map[string]any:
		raw, err := json.Marshal(x)
		if err != nil {
			return nil, fmt.Errorf("encoding %q: %w", key, err)
		}
		return raw, nil
	case []any:
		raw, err := json.Marshal(x)
		if err != nil {
			return nil, fmt.Errorf("encoding %q: %w", key, err)
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("%q must be a JSON-encoded flow document or a flow document passed directly", key)
	}
}

// flowsParam reads a "flows" argument as either a JSON-encoded string or a
// flow array. Same reasoning as flowParam.

// flowsParam reads a "flows" argument as either a JSON-encoded string or a
// flow array. Same reasoning as flowParam.
func flowsParam(req mcp.CallToolRequest, key string) (json.RawMessage, error) {
	args := req.GetArguments()
	v, ok := args[key]
	if !ok {
		return nil, fmt.Errorf("required argument %q not found", key)
	}
	switch x := v.(type) {
	case string:
		raw := json.RawMessage(x)
		if !json.Valid(raw) {
			return nil, fmt.Errorf("%q must be a JSON-encoded flow array or a flow array passed directly", key)
		}
		return raw, nil
	case []any:
		raw, err := json.Marshal(x)
		if err != nil {
			return nil, fmt.Errorf("encoding %q: %w", key, err)
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("%q must be a JSON-encoded flow array or a flow array passed directly", key)
	}
}

// nodeParam reads a "node" argument as either a JSON-encoded string or a
// node object. Mirrors flowParam for symmetry with the rest of the surface.

// nodeParam reads a "node" argument as either a JSON-encoded string or a
// node object. Mirrors flowParam for symmetry with the rest of the surface.
func nodeParam(req mcp.CallToolRequest, key string) (json.RawMessage, error) {
	args := req.GetArguments()
	v, ok := args[key]
	if !ok {
		return nil, fmt.Errorf("required argument %q not found", key)
	}
	switch x := v.(type) {
	case string:
		raw := json.RawMessage(x)
		if !json.Valid(raw) {
			return nil, fmt.Errorf("%q must be a JSON-encoded node object or a node object passed directly", key)
		}
		return raw, nil
	case map[string]any:
		raw, err := json.Marshal(x)
		if err != nil {
			return nil, fmt.Errorf("encoding %q: %w", key, err)
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("%q must be a JSON-encoded node object or a node object passed directly", key)
	}
}

// normalizeFlowDoc turns a raw flow payload into a Node-RED admin-API
// document. If fillNodes is true and the document is a tab object whose
// "nodes" or "configs" array is missing or explicitly null, an empty
// array is substituted so a POST /flow does not bounce with the opaque
// "Cannot read properties of null" runtime error.
//
// The payload is accepted in any of the three shapes Node-RED hands back:
//
//   - nested tab object:  {"id":"abc","label":"x","nodes":[...]}
//   - flat array element: [{"type":"tab","id":"abc","label":"x"},
//     {"type":"inject","id":"n1","z":"abc"}, ...]
//
// The flat shape comes from GET /flows, so update_flow can take the same
// document back without forcing the caller to re-shape it. If the payload is
// a flat array, the tab id to match is taken from flowID (when supplied);
// a nested payload is accepted as-is.

// normalizeFlowDoc turns a raw flow payload into a Node-RED admin-API
// document. If fillNodes is true and the document is a tab object whose
// "nodes" or "configs" array is missing or explicitly null, an empty
// array is substituted so a POST /flow does not bounce with the opaque
// "Cannot read properties of null" runtime error.
//
// The payload is accepted in any of the three shapes Node-RED hands back:
//
//   - nested tab object:  {"id":"abc","label":"x","nodes":[...]}
//   - flat array element: [{"type":"tab","id":"abc","label":"x"},
//     {"type":"inject","id":"n1","z":"abc"}, ...]
//
// The flat shape comes from GET /flows, so update_flow can take the same
// document back without forcing the caller to re-shape it. If the payload is
// a flat array, the tab id to match is taken from flowID (when supplied);
// a nested payload is accepted as-is.
func normalizeFlowDoc(raw json.RawMessage, flowID string, fillNodes bool) (nodered.RawFlow, error) {
	// Try a nested tab object first. A nested doc has either "nodes" or
	// "configs" — the two arrays Node-RED splits a tab into. A flat-array
	// element carries "type":"tab" and never both "nodes" and the rest of a
	// tab object, so the absence of those keys is the disambiguation.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err == nil {
		_, hasNodes := doc["nodes"]
		_, hasConfigs := doc["configs"]
		if hasNodes || hasConfigs || !looksLikeFlatArrayElement(doc) {
			if fillNodes {
				if !hasNodes || isJSONNull(doc["nodes"]) {
					doc["nodes"] = json.RawMessage(`[]`)
				}
				// Match the same null-coalescing for configs, but only
				// fill an absent configs when nodes is also absent: a
				// caller who supplied a real nodes array does not want
				// us to invent a configs key they never asked for. The
				// explicit null case (configs:null) is always coalesced,
				// because the runtime rejects null with an opaque error.
				if isJSONNull(doc["configs"]) || (!hasConfigs && !hasNodes) {
					doc["configs"] = json.RawMessage(`[]`)
				}
			}
			out, err := json.Marshal(doc)
			if err != nil {
				return nil, fmt.Errorf("re-encoding flow document: %w", err)
			}
			return nodered.RawFlow(out), nil
		}
	}

	// Flat array shape: pick the tab whose id matches flowID, then collect
	// every node whose z references it.
	var flat []json.RawMessage
	if err := json.Unmarshal(raw, &flat); err != nil {
		return nil, fmt.Errorf("flow document is not a JSON object or flow array: %w", err)
	}
	if flowID == "" {
		return nil, fmt.Errorf(
			"flow document is a flat array; pass the flow id so the right tab can be picked out")
	}
	tab, nodes, configs := splitFlatFlow(flat, flowID)
	if tab == nil {
		return nil, fmt.Errorf("no tab with id %q in the supplied flat flow array", flowID)
	}

	// Project into the nested doc shape.
	out := make(map[string]json.RawMessage, len(tab)+2)
	for k, v := range tab {
		// The flat shape includes "type":"tab" and "id":"<flow>" — drop those,
		// the nested shape uses id at the top level and "tab" is implied.
		if k == "type" {
			continue
		}
		if k == "id" {
			continue
		}
		out[k] = v
	}
	if fillNodes && len(nodes) == 0 {
		nodes = []json.RawMessage{}
	}
	encodedNodes, err := json.Marshal(nodes)
	if err != nil {
		return nil, fmt.Errorf("re-encoding nodes: %w", err)
	}
	out["nodes"] = encodedNodes
	if len(configs) > 0 {
		encodedConfigs, err := json.Marshal(configs)
		if err != nil {
			return nil, fmt.Errorf("re-encoding configs: %w", err)
		}
		out["configs"] = encodedConfigs
	}
	out["id"] = json.RawMessage(fmt.Sprintf("%q", flowID))

	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("re-encoding flow document: %w", err)
	}
	return nodered.RawFlow(encoded), nil
}

// splitFlatFlow partitions a flat /flows array into the tab, its nodes, and
// its config nodes, all indexed by id. Returns nil for tab when no match.

// splitFlatFlow partitions a flat /flows array into the tab, its nodes, and
// its config nodes, all indexed by id. Returns nil for tab when no match.
func splitFlatFlow(flat []json.RawMessage, flowID string) (tab map[string]json.RawMessage, nodes, configs []json.RawMessage) {
	for _, item := range flat {
		var meta struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Z    string `json:"z"`
		}
		if err := json.Unmarshal(item, &meta); err != nil {
			continue
		}
		switch {
		case meta.Type == "tab" && meta.ID == flowID:
			var m map[string]json.RawMessage
			if err := json.Unmarshal(item, &m); err == nil {
				tab = m
			}
		case meta.Z == flowID && meta.Type != "subflow":
			// Anything referring to the tab that is not a tab itself is a
			// child node. Config nodes (no x/y) are routed to configs so
			// the same rule Node-RED uses applies to a re-encoded flow.
			if hasXY(item) {
				nodes = append(nodes, item)
			} else {
				configs = append(configs, item)
			}
		}
	}
	return tab, nodes, configs
}

// hasXY reports whether the raw node carries x and y canvas coordinates.

// hasXY reports whether the raw node carries x and y canvas coordinates.
func hasXY(raw json.RawMessage) bool {
	var probe struct {
		X json.RawMessage `json:"x"`
		Y json.RawMessage `json:"y"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return len(probe.X) > 0 && len(probe.Y) > 0
}

// looksLikeFlatArrayElement reports whether a parsed JSON object has the
// shape of one entry inside a flat GET /flows array: an id, a type, and
// no nested-array fields that would mean a full tab doc.

// looksLikeFlatArrayElement reports whether a parsed JSON object has the
// shape of one entry inside a flat GET /flows array: an id, a type, and
// no nested-array fields that would mean a full tab doc.
func looksLikeFlatArrayElement(doc map[string]json.RawMessage) bool {
	_, hasID := doc["id"]
	_, hasType := doc["type"]
	_, hasNodes := doc["nodes"]
	_, hasConfigs := doc["configs"]
	return hasID && hasType && !hasNodes && !hasConfigs
}

// isJSONNull reports whether raw is the JSON literal "null". A
// serializer output of `"nodes":null` is not the same as the key being
// absent: the key exists, but it points at nothing. Node-RED's runtime
// rejects that with an opaque "Cannot read properties of null" error, so
// normalizeFlowDoc treats null the same as missing when filling defaults.

// isJSONNull reports whether raw is the JSON literal "null". A
// serializer output of `"nodes":null` is not the same as the key being
// absent: the key exists, but it points at nothing. Node-RED's runtime
// rejects that with an opaque "Cannot read properties of null" error, so
// normalizeFlowDoc treats null the same as missing when filling defaults.
func isJSONNull(raw json.RawMessage) bool {
	return string(raw) == "null"
}

// normalizeFlowsArray splits a raw flows payload into the per-flow entries
// the admin API expects.

// normalizeFlowsArray splits a raw flows payload into the per-flow entries
// the admin API expects.
func normalizeFlowsArray(raw json.RawMessage) ([]json.RawMessage, error) {
	var flows []json.RawMessage
	if err := json.Unmarshal(raw, &flows); err != nil {
		return nil, fmt.Errorf("flows is not a JSON array: %w", err)
	}
	if len(flows) == 0 {
		return nil, fmt.Errorf("flows must contain at least one flow")
	}
	return flows, nil
}

// findDeniedNodeInFlow walks every node entry of a single nested
// tab document and reports the first one whose type is in the
// configured denylist. Returns (false, "") when the document is
// empty, malformed, or contains no denied node — the caller treats
// "no denied node" the same as "no denylist". Issue #81.
//
// A nested tab doc is {"label":...,"nodes":[...],...}. The
// "nodes" array carries the canvas nodes; "configs" carries
// brokers, credentials, and other shared definitions. The latter
// can also carry shell-executing node types (any "exec"-class
// instance the operator pasted in), so both arrays are walked.

// findDeniedNodeInFlow walks every node entry of a single nested
// tab document and reports the first one whose type is in the
// configured denylist. Returns (false, "") when the document is
// empty, malformed, or contains no denied node — the caller treats
// "no denied node" the same as "no denylist". Issue #81.
//
// A nested tab doc is {"label":...,"nodes":[...],...}. The
// "nodes" array carries the canvas nodes; "configs" carries
// brokers, credentials, and other shared definitions. The latter
// can also carry shell-executing node types (any "exec"-class
// instance the operator pasted in), so both arrays are walked.
func (s *Server) findDeniedNodeInFlow(raw nodered.RawFlow) (denied bool, nodeType string) {
	if s.denylist == nil {
		return false, ""
	}
	var doc struct {
		Nodes   []json.RawMessage `json:"nodes"`
		Configs []json.RawMessage `json:"configs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false, ""
	}
	for _, n := range doc.Nodes {
		if t := readNodeType(n); s.denylist(t) {
			return true, t
		}
	}
	for _, n := range doc.Configs {
		if t := readNodeType(n); s.denylist(t) {
			return true, t
		}
	}
	return false, ""
}

// findDeniedNodeInNode inspects a single node payload (as accepted by
// add_node). Issue #81: a caller can ship an "exec" node directly,
// without wrapping it in a flow document.

// findDeniedNodeInNode inspects a single node payload (as accepted by
// add_node). Issue #81: a caller can ship an "exec" node directly,
// without wrapping it in a flow document.
func (s *Server) findDeniedNodeInNode(raw json.RawMessage) (denied bool, nodeType string) {
	if s.denylist == nil {
		return false, ""
	}
	if t := readNodeType(raw); s.denylist(t) {
		return true, t
	}
	return false, ""
}

// findDeniedNodeInFlowsArray walks every tab of a full /flows
// payload and reports the first denied node type encountered. The
// set_flows shape is a flat array; each element is a tab object or a
// canvas/config node, so we delegate to the same walker the single-
// flow handlers use. Issue #81.

// findDeniedNodeInFlowsArray walks every tab of a full /flows
// payload and reports the first denied node type encountered. The
// set_flows shape is a flat array; each element is a tab object or a
// canvas/config node, so we delegate to the same walker the single-
// flow handlers use. Issue #81.
func (s *Server) findDeniedNodeInFlowsArray(flows []json.RawMessage) (denied bool, nodeType string) {
	if s.denylist == nil {
		return false, ""
	}
	for _, entry := range flows {
		// Each entry can be either a tab doc ({"nodes":[...],"configs":[...]})
		// or a flat /flows element (just a single node). Try the nested
		// shape first: a single node has no "nodes" array, so the walker
		// returns no hit; we then fall back to inspecting the entry as a
		// node directly.
		if d, t := s.findDeniedNodeInFlow(nodered.RawFlow(entry)); d {
			return true, t
		}
		if d, t := s.findDeniedNodeInNode(entry); d {
			return true, t
		}
	}
	return false, ""
}

// readNodeType extracts the "type" field of a node object as a
// string, returning "" when the payload is not a JSON object or has
// no type. Failure-to-parse is treated as "" because the write
// handlers downstream of this check have their own validation; the
// denylist guard only needs to react to types it can name.

// readNodeType extracts the "type" field of a node object as a
// string, returning "" when the payload is not a JSON object or has
// no type. Failure-to-parse is treated as "" because the write
// handlers downstream of this check have their own validation; the
// denylist guard only needs to react to types it can name.
func readNodeType(raw json.RawMessage) string {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ""
	}
	return probe.Type
}

// handleGetSettings returns the Node-RED server settings as JSON.
