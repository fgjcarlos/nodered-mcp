package nodered

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// FlowWarning is an advisory finding, separate from structural FlowIssues.
type FlowWarning struct {
	Kind    string `json:"kind"`
	NodeID  string `json:"nodeId"`
	Message string `json:"message"`
}

type semanticNode struct {
	id, typ string
	fields  map[string]json.RawMessage
}

// ValidateFlowSemantics checks selected core nodes without consulting a runtime.
// These warnings are only for the validate_flow dry run, not the write guard.
func ValidateFlowSemantics(raw RawFlow) []FlowWarning {
	nodes := semanticNodes(raw)
	var warnings []FlowWarning
	canRespond := possibleHTTPResponders(nodes)
	for _, n := range nodes {
		if count, ok := declaredOutputs(n); ok && jsonTypeOf(n.fields["wires"]) == "array" {
			if wires, err := decodeWires(n.fields); err == nil && len(wires) != count {
				warnings = append(warnings, n.warning("output_count_mismatch", "has wires length %d, expected %d", len(wires), count))
			}
		}
		switch n.typ {
		case "change":
			var rules []map[string]json.RawMessage
			_ = json.Unmarshal(n.fields["rules"], &rules)
			for i, rule := range rules {
				warnings = n.checkPropertyType(warnings, fmt.Sprintf("rule %d pt", i+1), rule["pt"], false)
				if op, _ := rawString(rule["t"]); op == "move" {
					warnings = n.checkPropertyType(warnings, fmt.Sprintf("rule %d tot", i+1), rule["tot"], false)
				}
			}
		case "switch":
			warnings = n.checkPropertyType(warnings, "propertyType", n.fields["propertyType"], true)
		case "http in":
			if !canRespond[n.id] {
				warnings = append(warnings, n.warning("http_response_unreachable", "has no reachable http response in the supplied flow"))
			}
		}
	}
	return warnings
}

func semanticNodes(raw RawFlow) []semanticNode {
	var entries []map[string]json.RawMessage
	flat := jsonTypeOf(raw) == "array"
	if flat {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil
		}
	} else {
		doc, err := decodeFlow(raw)
		if err != nil {
			return nil
		}
		entries = doc.Nodes
	}

	var nodes []semanticNode
	hasTab := !flat
	for _, fields := range entries {
		if id := nodeID(fields); id != "" {
			typ, _ := jsTypeString(fields["type"])
			nodes = append(nodes, semanticNode{id, typ, fields})
			hasTab = hasTab || typ == "tab"
		}
	}
	if !hasTab {
		return nil
	}
	return nodes
}

func (n semanticNode) warning(kind, format string, args ...any) FlowWarning {
	return FlowWarning{Kind: kind, NodeID: n.id, Message: fmt.Sprintf("node %q (%s) ", n.id, n.typ) + fmt.Sprintf(format, args...)}
}

func declaredOutputs(n semanticNode) (int, bool) {
	switch n.typ {
	case "switch":
		var rules []json.RawMessage
		if json.Unmarshal(n.fields["rules"], &rules) == nil && rules != nil {
			return len(rules), true
		}
	case "function", "trigger":
	default:
		return 0, false
	}
	value, ok := rawString(n.fields["outputs"])
	if !ok {
		value = strings.TrimSpace(string(n.fields["outputs"]))
	}
	count, err := strconv.Atoi(value)
	return count, err == nil && count >= 0
}

func (n semanticNode) checkPropertyType(warnings []FlowWarning, field string, raw json.RawMessage, switchProperty bool) []FlowWarning {
	value, ok := rawString(raw)
	if len(raw) == 0 || jsonTypeOf(raw) == "null" || (ok && value == "") {
		return warnings // The runtime supplies the default.
	}
	if value == "msg" || value == "flow" || value == "global" || (switchProperty && (value == "jsonata" || value == "env")) {
		return warnings
	}
	want := "msg, flow or global"
	if switchProperty {
		want = "msg, flow, global, jsonata or env"
	}
	got := jsonTypeOf(raw)
	if ok {
		got = strconv.Quote(value)
	}
	return append(warnings, n.warning("invalid_property_type", "%s must be %s; got %s", field, want, got))
}

// Walk backwards once from responses and inconclusive paths instead of
// walking the same branch from every HTTP In. The visited set breaks cycles.
func possibleHTTPResponders(nodes []semanticNode) map[string]bool {
	types := make(map[string]string, len(nodes))
	for _, n := range nodes {
		types[n.id] = n.typ
	}
	upstream := make(map[string][]string)
	var queue []string
	for _, n := range nodes {
		switch {
		case n.typ == "http response", n.typ == "link call", strings.HasPrefix(n.typ, "subflow:"):
			// shortcut: treat subflow and Link Call paths as inconclusive until their internals are modeled.
			queue = append(queue, n.id)
			continue
		case n.typ == "link out":
			if mode, _ := rawString(n.fields["mode"]); mode == "return" {
				continue
			}
			if linkOutOwnsWires(n.fields) {
				var links []string
				if json.Unmarshal(n.fields["links"], &links) == nil {
					for _, id := range links {
						if typ, exists := types[id]; !exists {
							queue = append(queue, n.id) // A single-tab export can omit the remote target.
						} else if typ == "link in" {
							upstream[id] = append(upstream[id], n.id)
						}
					}
				}
				continue
			}
		}
		wires, _ := decodeWires(n.fields)
		for _, port := range wires {
			for _, id := range port {
				upstream[id] = append(upstream[id], n.id)
			}
		}
	}
	seen := make(map[string]bool)
	for len(queue) != 0 {
		id := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if !seen[id] {
			seen[id] = true
			queue = append(queue, upstream[id]...)
		}
	}
	return seen
}
