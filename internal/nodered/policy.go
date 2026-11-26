package nodered

import (
	"encoding/json"
	"fmt"
	"strings"
)

// NodePolicy reports whether a node type may be deployed. It returns true
// for an allowed type. A nil policy allows every node type.
//
// The predicate receives the original type name and decides how to compare
// it. The policy answers one question — "may this node type
// be deployed?" — and knows nothing about flows, subflows or the admin API;
// the traversal below decides which node types a payload carries.
type NodePolicy func(nodeType string) bool

// DeniedNodeError reports that a write was refused because the effective
// configuration carried a forbidden node type.
//
// The error names the offending type so the caller can render an
// operator-facing message; the caller decides the wording, because only it
// knows which environment variable carries the policy.
type DeniedNodeError struct {
	// NodeType is the forbidden node type that caused the refusal.
	NodeType string
	// Subflow is the id of the subflow definition that carries NodeType
	// when the refusal came from a subflow instance; empty otherwise.
	Subflow string
}

func (e *DeniedNodeError) Error() string {
	if e.Subflow != "" {
		return fmt.Sprintf("node type %q (inside subflow %q) is denied by the configured node policy", e.NodeType, e.Subflow)
	}
	return fmt.Sprintf("node type %q is denied by the configured node policy", e.NodeType)
}

// SetNodePolicy installs the policy consulted before the client deploys
// anything. A nil policy allows every node type.
//
// The client holds the policy because it is the only layer that knows the
// effective configuration a write would deploy: the patched node after
// update_node, the flattened document after a flow update, the subflow
// definitions a global write replaces. Checking there covers every write
// path with one rule instead of one rule per tool.
func (c *Client) SetNodePolicy(policy NodePolicy) {
	c.policy = policy
}

// deniedNodeType returns the error a write path reports when doc carries a
// forbidden node type.
func (c *Client) deniedNodeType(doc json.RawMessage) error {
	if c.policy == nil || len(doc) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(doc, &value); err != nil {
		return nil
	}
	if nodeType, denied := deniedTypeInValue(c.policy, value); denied {
		return &DeniedNodeError{NodeType: nodeType}
	}
	return nil
}

// deniedNodeTypeInFlows returns the error a write path reports when a flat
// /flows array carries a forbidden node type.
func (c *Client) deniedNodeTypeInFlows(flows []json.RawMessage) error {
	if c.policy == nil {
		return nil
	}
	for _, entry := range flows {
		var value any
		if err := json.Unmarshal(entry, &value); err != nil {
			continue
		}
		if nodeType, denied := deniedTypeInValue(c.policy, value); denied {
			return &DeniedNodeError{NodeType: nodeType}
		}
	}
	return nil
}

// deniedNodeTypeForType returns the error a write path reports when a
// single node type is forbidden.
func (c *Client) deniedNodeTypeForType(nodeType string) error {
	if c.policy == nil || nodeType == "" || c.policy(nodeType) {
		return nil
	}
	return &DeniedNodeError{NodeType: nodeType}
}

// deniedNodeTypeOfNode returns the error a write path reports when the
// node with the given id in doc carries a forbidden node type.
//
// The node is judged as a whole, recursively: a node whose own properties
// carry a member collection is exactly the shape the deployment would
// carry, so a forbidden type nested in it counts too. It is the check that
// sees a node as it will be deployed, after a patch has been merged into it.
// A subflow instance is also judged by the definition it runs, resolved in
// effective (see deniedSubflowInstance).
func (c *Client) deniedNodeTypeOfNode(doc RawFlow, id string, effective []json.RawMessage) error {
	if c.policy == nil || id == "" {
		return nil
	}
	node, found := nodeRawByID(doc, id)
	if !found {
		return nil
	}
	if err := c.deniedNodeType(node); err != nil {
		return err
	}
	return c.deniedSubflowInstance(node, effective)
}

// deniedSubflowInstance returns the error a write path reports when doc
// carries a subflow instance whose definition — directly or through nested
// instances — holds a forbidden node type.
//
// An instance node's own type ("subflow:<id>") says nothing about what it
// runs, so it is judged by its definition. Definitions are resolved from
// doc itself and from effective, the configuration the write would deploy
// (flat /flows entries or nested definitions; members may be filed either
// way). Only instances are judged here: the direct types of doc are the
// other checks' business. An instance whose definition is not found is left
// to the reference validation.
func (c *Client) deniedSubflowInstance(doc json.RawMessage, effective []json.RawMessage) error {
	if c.policy == nil || len(doc) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(doc, &value); err != nil {
		return nil
	}
	values := []any{value}
	for _, entry := range effective {
		var v any
		if json.Unmarshal(entry, &v) == nil {
			values = append(values, v)
		}
	}
	defs := indexSubflowDefinitions(values)
	if nodeType, subflow, denied := deniedTypeIn(c.policy, defs, map[string]bool{}, value, false); denied {
		return &DeniedNodeError{NodeType: nodeType, Subflow: subflow}
	}
	return nil
}

// indexSubflowDefinitions maps every subflow definition id found in values
// to what it runs: the definition object itself (its nested "nodes" and
// "configs") and every object whose "z" names it (the flat /flows form).
func indexSubflowDefinitions(values []any) map[string][]any {
	defs := map[string][]any{}
	byOwner := map[string][]any{}
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case []any:
			for _, member := range typed {
				walk(member)
			}
		case map[string]any:
			id, _ := typed["id"].(string)
			if nodeType, _ := typed["type"].(string); nodeType == "subflow" && id != "" {
				defs[id] = append(defs[id], typed)
			}
			if z, _ := typed["z"].(string); z != "" {
				byOwner[z] = append(byOwner[z], typed)
			}
			for _, key := range []string{"nodes", "configs", "subflows"} {
				walk(typed[key])
			}
		}
	}
	walk(values)
	for id := range defs {
		defs[id] = append(defs[id], byOwner[id]...)
	}
	return defs
}

// NodeTypeAllowed reports whether the configured policy permits a node
// type. Callers that build a configuration themselves — the set_context
// helper, for instance — use it to refuse before they deploy anything.
func (c *Client) NodeTypeAllowed(nodeType string) bool {
	return c.policy == nil || c.policy(nodeType)
}

// deniedTypeInValue walks a decoded JSON value and reports the first
// forbidden node type it carries.
//
// The walk follows the admin API's own structure. Every object the payload
// carries is judged by its own "type" — the node type for a node, the
// container kind for a tab or a subflow — and the walk descends into the
// member collections ("nodes", "configs", "subflows"), so a forbidden type
// nested in a subflow definition is found too.
//
// Non-string "type" values, non-object members, malformed JSON and absent
// keys are ignored rather than treated as errors: the policy judges what a
// payload carries, and a payload it cannot read carries no node type this
// check can name.
func deniedTypeInValue(policy NodePolicy, value any) (string, bool) {
	nodeType, _, denied := deniedTypeIn(policy, nil, nil, value, true)
	return nodeType, denied
}

// deniedTypeIn is the walk behind deniedTypeInValue. With definitions, a
// "subflow:<id>" instance is also judged by everything its definition runs,
// recursively; seen holds the definitions already judged, so a cycle of
// nested instances ends. judgeOwn=false skips the direct types of value and
// judges only the definitions its instances run. It also reports the
// innermost subflow that carried the forbidden type.
func deniedTypeIn(policy NodePolicy, definitions map[string][]any, seen map[string]bool, value any, judgeOwn bool) (string, string, bool) {
	switch typed := value.(type) {
	case []any:
		for _, member := range typed {
			if nodeType, subflow, denied := deniedTypeIn(policy, definitions, seen, member, judgeOwn); denied {
				return nodeType, subflow, true
			}
		}
	case map[string]any:
		if nodeType, ok := typed["type"].(string); ok {
			if judgeOwn && !policy(nodeType) {
				return nodeType, "", true
			}
			if id, instance := strings.CutPrefix(nodeType, "subflow:"); instance && definitions != nil && !seen[id] {
				seen[id] = true
				for _, member := range definitions[id] {
					if denied, subflow, found := deniedTypeIn(policy, definitions, seen, member, true); found {
						if subflow == "" {
							subflow = id
						}
						return denied, subflow, true
					}
				}
			}
		}
		for _, key := range []string{"nodes", "configs", "subflows"} {
			if nodeType, subflow, denied := deniedTypeIn(policy, definitions, seen, typed[key], judgeOwn); denied {
				return nodeType, subflow, true
			}
		}
	}
	return "", "", false
}

// nodeRawByID returns the node with the given id in a flow document, and
// whether such a node was found. Both the canvas collection and the
// configuration collection are searched: a shared broker or a credential
// holder is a node too.
func nodeRawByID(doc RawFlow, id string) (json.RawMessage, bool) {
	var body struct {
		Nodes   []json.RawMessage `json:"nodes"`
		Configs []json.RawMessage `json:"configs"`
	}
	if err := json.Unmarshal(doc, &body); err != nil {
		return nil, false
	}
	for _, collection := range [][]json.RawMessage{body.Nodes, body.Configs} {
		for _, raw := range collection {
			var node map[string]json.RawMessage
			if err := json.Unmarshal(raw, &node); err != nil {
				continue
			}
			if nodeID(node) == id {
				return raw, true
			}
		}
	}
	return nil, false
}
