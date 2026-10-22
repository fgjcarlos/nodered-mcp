package mcp

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// discoverContextHelper treats labels as a search hint, never as proof of
// identity. Only an isolated tab with the exact supported executable shape
// may receive a context payload. Edited/partial tabs are left untouched.
func discoverContextHelper(raw nodered.RawFlow) (*setContextHelper, map[string]bool, error) {
	items := nodered.FlowArray(raw)
	if items == nil {
		return nil, nil, fmt.Errorf("discovering helper: unrecognizable flow configuration")
	}
	used := make(map[string]bool, len(items))
	nodes := make([]map[string]any, 0, len(items))
	for _, item := range items {
		var node map[string]any
		if err := json.Unmarshal(item, &node); err != nil || node == nil {
			return nil, nil, fmt.Errorf("discovering helper: invalid flow entry")
		}
		id, ok := node["id"].(string)
		if !ok || id == "" || used[id] {
			return nil, nil, fmt.Errorf("discovering helper: missing or duplicate flow entry ID")
		}
		used[id] = true
		nodes = append(nodes, node)
	}
	for _, tab := range nodes {
		if tab["type"] != "tab" || tab["label"] != setContextHelperFlowLabel || tab["disabled"] == true {
			continue
		}
		// Tab-level environment can change function execution, so do not adopt it.
		if env, ok := tab["env"]; ok && !emptyHelperValue(env) {
			continue
		}
		members := []map[string]any{}
		for _, node := range nodes {
			if node["z"] == tab["id"] {
				members = append(members, node)
			}
		}
		if len(members) != 2 {
			continue
		}
		var fn, inj map[string]any
		for _, node := range members {
			if node["type"] == "function" {
				fn = node
			}
			if node["type"] == "inject" {
				inj = node
			}
		}
		if fn == nil || inj == nil {
			continue
		}
		functionID, injectID := fn["id"].(string), inj["id"].(string)
		var expected struct {
			Nodes []map[string]any `json:"nodes"`
		}
		_ = json.Unmarshal(contextHelperDocument(functionID, injectID), &expected)
		if !matchesHelperNode(fn, expected.Nodes[0]) || !matchesHelperNode(inj, expected.Nodes[1]) {
			continue
		}
		return newContextHelper(tab["id"].(string), functionID, injectID, used), used, nil
	}
	return nil, used, nil
}

func newContextHelper(flowID, functionID, injectID string, used map[string]bool) *setContextHelper {
	return &setContextHelper{
		flowID: flowID, functionID: functionID, injectID: injectID,
		flowAliasAllowed:     flowID == setContextHelperFlowID || !used[setContextHelperFlowID],
		functionAliasAllowed: functionID == setContextHelperFunctionID || !used[setContextHelperFunctionID],
	}
}

func emptyHelperValue(v any) bool {
	if v == nil {
		return true
	}
	switch value := v.(type) {
	case string:
		return value == ""
	case []any:
		return len(value) == 0
	}
	return false
}

// Ignore editor presentation only. Unknown operational properties fail closed;
// in particular initialize/finalize/libs, scheduling and outgoing wiring must
// not be smuggled in under a familiar name or node ID.
func matchesHelperNode(actual, expected map[string]any) bool {
	for key, value := range actual {
		switch key {
		case "id", "z", "x", "y", "info", "icon", "color", "l":
			continue
		case "d":
			if value != false {
				return false
			}
			continue
		case "timeout":
			if value != "0" && value != float64(0) {
				return false
			}
			continue
		}
		wanted, known := expected[key]
		if !known {
			return false
		}
		if key == "wires" && actual["type"] == "function" {
			if !noHelperOutputs(value) {
				return false
			}
			continue
		}
		if !reflect.DeepEqual(value, wanted) {
			return false
		}
	}
	for key, wanted := range expected {
		switch key {
		case "id", "z", "x", "y", "noerr", "onceDelay", "payloadType":
			continue
		}
		if _, ok := actual[key]; !ok && !emptyHelperValue(wanted) && wanted != false {
			return false
		}
	}
	return true
}

func noHelperOutputs(value any) bool {
	outputs, ok := value.([]any)
	if !ok {
		return false
	}
	for _, output := range outputs {
		if !emptyHelperValue(output) {
			return false
		}
	}
	return true
}
