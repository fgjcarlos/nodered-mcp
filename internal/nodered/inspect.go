package nodered

import (
	"encoding/json"
	"strings"
)

// This file holds pure inspection helpers over a raw flow configuration. They
// exist for one reason: a real Node-RED instance returns a flows document far
// too large to hand to a model verbatim. A few hundred nodes is tens of
// thousands of tokens, and the context is spent before any work can be done.
//
// Nothing here talks HTTP. Everything takes the bytes GET /flows returned and
// answers a narrower question about them.

// FlowSummary describes one flow tab or subflow without any of its node bodies.
type FlowSummary struct {
	ID       string `json:"id"`
	Label    string `json:"label,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
	// NodeCount is how many nodes this container owns.
	NodeCount int `json:"nodeCount"`
	// NodeTypes counts the node types present, e.g. {"inject":1,"debug":2}.
	// It is what makes the summary actionable: enough to decide which tab is
	// worth fetching in full, without fetching any of them.
	NodeTypes map[string]int `json:"nodeTypes,omitempty"`
}

// FlowsOverview is the compact view of an entire Node-RED configuration.
type FlowsOverview struct {
	Tabs     []FlowSummary `json:"tabs"`
	Subflows []FlowSummary `json:"subflows,omitempty"`
	// ConfigNodes counts nodes owned by no tab or subflow — shared brokers,
	// credentials, server definitions. They are easy to forget and often the
	// reason an otherwise correct flow does not run.
	ConfigNodes int `json:"configNodes"`
	TotalNodes  int `json:"totalNodes"`
}

// NodeMatch is one node found by SearchFlows, together with the container it
// belongs to. The node is returned verbatim: a hit is exactly the thing the
// caller wanted to see, so trimming it would defeat the search.
type NodeMatch struct {
	// FlowID and FlowLabel are empty for config nodes, which belong to no
	// tab or subflow.
	FlowID    string          `json:"flowId,omitempty"`
	FlowLabel string          `json:"flowLabel,omitempty"`
	Node      json.RawMessage `json:"node"`
}

// nodeMeta is the small, universal part of a node. Every Node-RED object has
// id and type; ordinary nodes also carry z (their owning tab or subflow).
// Everything else is node-specific and stays untouched in the raw bytes.
type nodeMeta struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Z        string `json:"z"`
	Label    string `json:"label"`
	Name     string `json:"name"`
	Disabled bool   `json:"disabled"`
}

// containers indexes the tabs and subflows in a flow document by ID, so a
// node's "z" can be resolved to a human-readable owner in one pass.
func containers(items []json.RawMessage) (byID map[string]nodeMeta, tabs, subflows []nodeMeta) {
	byID = make(map[string]nodeMeta, len(items))
	for _, item := range items {
		var m nodeMeta
		if json.Unmarshal(item, &m) != nil {
			continue
		}
		switch m.Type {
		case "tab":
			byID[m.ID] = m
			tabs = append(tabs, m)
		case "subflow":
			// Subflows label themselves with "name" rather than "label".
			if m.Label == "" {
				m.Label = m.Name
			}
			byID[m.ID] = m
			subflows = append(subflows, m)
		}
	}
	return byID, tabs, subflows
}

// SummarizeFlows condenses a raw flow configuration into per-container counts.
// It tolerates both the bare-array (API v1) and {"rev":..,"flows":[..]} (v2)
// shapes, and returns an empty overview for anything it cannot parse.
func SummarizeFlows(raw RawFlow) FlowsOverview {
	items := extractFlowArray(raw)
	byID, tabs, subflows := containers(items)

	// Accumulate per container, keyed by ID, then project onto the ordered
	// tab/subflow lists so output order follows the document.
	counts := make(map[string]map[string]int, len(byID))
	total := 0
	configNodes := 0

	for _, item := range items {
		var m nodeMeta
		if json.Unmarshal(item, &m) != nil {
			continue
		}
		if m.Type == "tab" || m.Type == "subflow" {
			continue
		}
		total++
		// No z, or a z pointing at nothing we know: not owned by a container.
		if _, ok := byID[m.Z]; m.Z == "" || !ok {
			configNodes++
			continue
		}
		if counts[m.Z] == nil {
			counts[m.Z] = make(map[string]int)
		}
		counts[m.Z][m.Type]++
	}

	summarize := func(c nodeMeta) FlowSummary {
		s := FlowSummary{ID: c.ID, Label: c.Label, Disabled: c.Disabled, NodeTypes: counts[c.ID]}
		for _, n := range counts[c.ID] {
			s.NodeCount += n
		}
		return s
	}

	out := FlowsOverview{ConfigNodes: configNodes, TotalNodes: total}
	for _, t := range tabs {
		out.Tabs = append(out.Tabs, summarize(t))
	}
	for _, sf := range subflows {
		out.Subflows = append(out.Subflows, summarize(sf))
	}
	return out
}

// SearchFlows finds nodes matching a free-text query, a node type, or both.
//
// The query is matched case-insensitively against each node's raw JSON. That
// is deliberate: Node-RED nodes are schemaless, so the interesting value is
// just as likely to be an MQTT topic, an HTTP url, or a line inside a function
// body as it is to be the node's name. Matching the whole node finds all of
// them without this package having to know any node type.
//
// An empty query and an empty nodeType match every node, which makes
// SearchFlows(raw, "", "", n) a cheap way to page through a configuration.
//
// It returns at most limit matches plus the total number that matched, so a
// caller can tell truncation from exhaustion.
func SearchFlows(raw RawFlow, query, nodeType string, limit int) (matches []NodeMatch, total int) {
	if limit <= 0 {
		limit = 20
	}
	items := extractFlowArray(raw)
	byID, _, _ := containers(items)
	needle := strings.ToLower(query)

	for _, item := range items {
		var m nodeMeta
		if json.Unmarshal(item, &m) != nil {
			continue
		}
		// Containers are not nodes; returning them would be noise.
		if m.Type == "tab" || m.Type == "subflow" {
			continue
		}
		if nodeType != "" && strings.ToLower(m.Type) != strings.ToLower(nodeType) {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(string(item)), needle) {
			continue
		}

		total++
		if len(matches) >= limit {
			// Keep counting: the total is what tells the caller it was truncated.
			continue
		}
		match := NodeMatch{Node: item}
		if owner, ok := byID[m.Z]; ok {
			match.FlowID = owner.ID
			match.FlowLabel = owner.Label
		}
		matches = append(matches, match)
	}
	return matches, total
}
