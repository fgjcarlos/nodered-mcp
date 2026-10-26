package nodered

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Validation of the effective configuration — the document a full
// deployment would install.
//
// A full deployment (POST /flows with the "full" deployment type)
// replaces the runtime's whole flow configuration: the tabs, the nodes
// inside them, the subflow definitions and the nodes inside those. The
// runtime parses that array the same way every time
// (@node-red/runtime/lib/flows/util.js, parseConfig): a "tab" entry
// opens a tab, a "subflow" entry opens a subflow definition, a node with
// canvas coordinates belongs to the tab or the subflow its "z" names, a
// node without them is a configuration node, and a node whose "z" names
// nothing is filed in a tab the runtime invents on the spot.
//
// The rules below are the ones a full deployment can break, and each of
// them is a reference that does not resolve:
//
//   - a wire that names a node outside the owner (the tab or the subflow)
//     never delivers: the runtime resolves wire targets inside the owner,
//     so the message is dropped;
//   - a node whose "z" names no tab and no subflow lands in a phantom
//     tab, so the node runs somewhere nobody asked for;
//   - two entries with the same id: the runtime keys every node by its
//     id, so the last entry wins and the earlier one disappears without
//     a word — and a wire that named the dropped node now points at the
//     wrong node;
//   - an instance of a subflow whose definition is not in the
//     configuration is a missing node type, and a runtime with a missing
//     node type refuses to start the flows at all.
//
// Two topologies are deliberately NOT treated as errors, because the
// runtime owns them and they are legitimate:
//
//   - link nodes. The runtime recomputes the wires of a "link out" node
//     from the "links" arrays, so a wire of a link out that points
//     nowhere is a value the runtime overwrites, not a dangling wire. A
//     link in may point at a link out in another tab: that is what a
//     link is for.
//   - subflow instances. An instance is an ordinary node of its tab, and
//     its wires are checked against that tab like any other node's.
//
// The reference scan for subflow instances follows the runtime's own
// comparison: /^subflow:(.+)$/ applied to the type after the JavaScript
// String() coercion (see jsTypeString), so a type of ["subflow:x"] is
// the reference it looks like.

// Issue kinds that only the effective configuration can produce.
const (
	// IssueMissingSubflow is an instance of a subflow whose definition is
	// not part of the configuration. Deploying it would leave the runtime
	// with a node type it cannot resolve, and the runtime then refuses to
	// start the flows.
	IssueMissingSubflow FlowIssueKind = "missing_subflow"
	// IssueOrphanNode is a node whose "z" names neither a tab nor a
	// subflow definition in the configuration. The runtime invents a
	// phantom tab for such a node.
	IssueOrphanNode FlowIssueKind = "orphan_node"
	// IssueDanglingSubflowPort is a port of a subflow definition — an
	// "in" or an "out" — whose wire names a node that is not in the
	// definition.
	IssueDanglingSubflowPort FlowIssueKind = "dangling_subflow_port"
)

// ConfigValidationError reports the structural issues that stopped a
// deployment. The write paths return it before they contact the runtime,
// so a configuration with broken references never reaches it.
type ConfigValidationError struct {
	Issues []FlowIssue
}

// Error summarises the issues for a log line or an error message.
func (e *ConfigValidationError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return "the configuration has structural problems and was not deployed"
	}
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		parts = append(parts, issue.Message)
	}
	return fmt.Sprintf("the configuration has %d structural problem(s) and was not deployed: %s",
		len(e.Issues), strings.Join(parts, "; "))
}

// ValidateEffectiveConfig returns every structural issue in the
// configuration a full deployment would install. The empty slice means
// "this is safe to deploy".
//
// The document may be a bare array (the shape GET /flows serves) or the
// {flows:[...],rev:"..."} envelope the runtime serves it in. A document
// that is neither is reported as a single document-level issue.
func ValidateEffectiveConfig(raw RawFlow) []FlowIssue {
	items, ok := effectiveConfigItems(raw)
	if !ok {
		return []FlowIssue{{
			Kind:    "invalid_document",
			Message: "the configuration is not a flow array: expected a JSON array of flow entries",
		}}
	}
	issues := effectiveConfigIssues(items)
	if issues == nil {
		issues = []FlowIssue{}
	}
	return issues
}

// effectiveConfigItems returns the flat entries of a configuration
// document. It accepts the bare array and the {flows:[...]} envelope, and
// reports false for anything else — including a null document, which
// decodes into an empty array and would otherwise read as "a
// configuration with no entries".
func effectiveConfigItems(raw RawFlow) ([]json.RawMessage, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, false
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, true
	}
	var env struct {
		Flows []json.RawMessage `json:"flows"`
	}
	if err := json.Unmarshal(raw, &env); err == nil {
		return env.Flows, true
	}
	return nil, false
}

// validateEffectiveConfig refuses a configuration whose references do not
// resolve. The write paths call it before they snapshot anything and
// before they contact the runtime.
func validateEffectiveConfig(items []json.RawMessage) error {
	issues := effectiveConfigIssues(items)
	if len(issues) == 0 {
		return nil
	}
	return &ConfigValidationError{Issues: issues}
}

// effectiveConfigIssues runs the effective-configuration rules over the
// flat entries of a configuration.
func effectiveConfigIssues(items []json.RawMessage) []FlowIssue {
	var issues []FlowIssue
	owners, subflows := indexOwners(items)
	issues = append(issues, duplicateIDIssues(items)...)
	issues = append(issues, ownerRuleIssues(items, owners)...)
	issues = append(issues, orphanNodeIssues(items, owners)...)
	issues = append(issues, missingSubflowIssues(items, subflows)...)
	return issues
}

// ownerRuleIssues runs the rules that are judged per owner — a tab or a
// subflow definition — over every owner of the configuration.
func ownerRuleIssues(items []json.RawMessage, owners []ownerRef) []FlowIssue {
	var issues []FlowIssue
	for _, owner := range owners {
		doc, ok := ownerDocument(items, owner.id)
		if !ok {
			continue
		}
		issues = append(issues, ownerIssues(doc)...)
		if owner.kind == "subflow" {
			issues = append(issues, flatSubflowPortIssues(owner.id, items)...)
		}
	}
	return issues
}

// ownerRef is a tab or a subflow definition in the configuration.
type ownerRef struct {
	id   string
	kind string
}

// indexOwners lists the tabs and the subflow definitions of the
// configuration, in document order, together with the set of subflow
// definition ids.
//
// The runtime tells a tab from a subflow definition with a strict
// comparison (n.type === 'tab'), so the type is read as a plain string
// here too: a type that only looks like a tab after a coercion is not
// one for the runtime either.
func indexOwners(items []json.RawMessage) ([]ownerRef, map[string]bool) {
	var owners []ownerRef
	subflows := map[string]bool{}
	for _, item := range items {
		entry := decodeConfigEntry(item)
		switch entry.Type {
		case "tab":
			owners = append(owners, ownerRef{id: entry.ID, kind: "tab"})
		case "subflow":
			owners = append(owners, ownerRef{id: entry.ID, kind: "subflow"})
			subflows[entry.ID] = true
		}
	}
	return owners, subflows
}

// duplicateIDIssues reports the entries that share an id. The runtime
// keys every node by its id, so the last entry with an id wins and the
// earlier ones are dropped without a word.
func duplicateIDIssues(items []json.RawMessage) []FlowIssue {
	var issues []FlowIssue
	seen := map[string]bool{}
	for _, item := range items {
		entry := decodeConfigEntry(item)
		if !entry.HasID || entry.ID == "" {
			continue
		}
		if seen[entry.ID] {
			issues = append(issues, FlowIssue{
				Kind:    IssueDuplicateID,
				NodeID:  entry.ID,
				Message: fmt.Sprintf("two configuration entries share id %q; the runtime keeps the last one and drops the other without a word, so a wire that named the dropped node now points at the wrong node", entry.ID),
			})
		}
		seen[entry.ID] = true
	}
	return issues
}

// ownerIssues runs the tab-level rules over one owner's document.
//
// The duplicate-id rule is skipped here: the whole-configuration pass
// reports it, and it also covers the cross-owner case a per-document
// rule cannot see.
func ownerIssues(doc RawFlow) []FlowIssue {
	var out []FlowIssue
	for _, issue := range ValidateFlow(doc) {
		if issue.Kind == IssueDuplicateID {
			continue
		}
		out = append(out, issue)
	}
	return out
}

// orphanNodeIssues reports the nodes whose "z" names no tab and no
// subflow definition in the configuration. The runtime invents a tab for
// such a node (parseConfig creates one for an unknown z), so the node
// runs outside the flow that is being deployed.
func orphanNodeIssues(items []json.RawMessage, owners []ownerRef) []FlowIssue {
	known := map[string]bool{}
	for _, owner := range owners {
		known[owner.id] = true
	}
	var issues []FlowIssue
	for _, item := range items {
		entry := decodeConfigEntry(item)
		switch entry.Type {
		case "tab", "subflow", "group":
			continue
		}
		if !entry.HasZ {
			// No z at all: the runtime files the node at global scope.
			continue
		}
		if !entry.ZIsString {
			issues = append(issues, orphanNodeIssue(entry, ""))
			continue
		}
		if entry.Z == "" || known[entry.Z] {
			continue
		}
		issues = append(issues, orphanNodeIssue(entry, entry.Z))
	}
	return issues
}

// orphanNodeIssue builds the issue for a node that belongs to no owner.
func orphanNodeIssue(entry configEntry, z string) FlowIssue {
	return FlowIssue{
		Kind:    IssueOrphanNode,
		NodeID:  entry.ID,
		Target:  z,
		Message: fmt.Sprintf("node %q references z=%q, which is neither a tab nor a subflow definition in this configuration; the runtime invents a tab for it, so the node runs outside the flow you are deploying", entry.ID, z),
	}
}

// missingSubflowIssues reports the instances of a subflow whose
// definition is not in the configuration. A full deployment replaces the
// whole configuration, so such an instance would be left with a node type
// the runtime cannot resolve — and a runtime with a missing node type
// refuses to start the flows at all.
func missingSubflowIssues(items []json.RawMessage, subflows map[string]bool) []FlowIssue {
	var issues []FlowIssue
	for _, item := range items {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(item, &m); err != nil {
			continue
		}
		raw, ok := m["type"]
		if !ok {
			continue
		}
		id, ok := subflowInstanceID(raw)
		if !ok || subflows[id] {
			continue
		}
		entry := decodeConfigEntry(item)
		issues = append(issues, FlowIssue{
			Kind:    IssueMissingSubflow,
			NodeID:  entry.ID,
			Target:  id,
			Message: fmt.Sprintf("node %q is an instance of subflow %q, but this configuration carries no definition for it; a full deployment would leave the runtime with a node type it cannot resolve, and it then refuses to start the flows", entry.ID, id),
		})
	}
	return issues
}

// subflowInstanceID returns the subflow id a node's type refers to, the
// way the runtime reads it: the type is coerced with the JavaScript
// String() and matched against /^subflow:(.+)$/ (see jsTypeString).
func subflowInstanceID(raw json.RawMessage) (string, bool) {
	typ, ok := jsTypeString(raw)
	if !ok {
		return "", false
	}
	if !strings.HasPrefix(typ, "subflow:") {
		return "", false
	}
	id := strings.TrimPrefix(typ, "subflow:")
	if id == "" {
		return "", false
	}
	return id, true
}

// ownerDocument builds the nested document of one owner — a tab or a
// subflow definition — out of the flat configuration.
//
// The runtime splits the entries the same way: an entry belongs to the
// tab or the subflow its "z" names, and the entries that carry canvas
// coordinates are the ones that run. The rest are configuration nodes.
func ownerDocument(items []json.RawMessage, ownerID string) (RawFlow, bool) {
	nodes := []json.RawMessage{}
	configs := []json.RawMessage{}
	for _, item := range items {
		entry := decodeConfigEntry(item)
		if entry.Z != ownerID {
			continue
		}
		switch entry.Type {
		case "tab", "subflow":
			continue
		}
		if hasCanvasCoords(item) {
			nodes = append(nodes, withoutLinkOutWires(item))
			continue
		}
		configs = append(configs, item)
	}
	id, err := json.Marshal(ownerID)
	if err != nil {
		return nil, false
	}
	doc := map[string]json.RawMessage{"id": id}
	nodesJSON, err := json.Marshal(nodes)
	if err != nil {
		return nil, false
	}
	doc["nodes"] = nodesJSON
	if len(configs) > 0 {
		configsJSON, err := json.Marshal(configs)
		if err != nil {
			return nil, false
		}
		doc["configs"] = configsJSON
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, false
	}
	return RawFlow(out), true
}

// withoutLinkOutWires returns the node with its "wires" removed when the
// runtime would recompute them.
//
// The runtime owns the wires of a "link out" node: when the node carries
// a "links" array, parseConfig replaces its wires with the link targets
// (@node-red/runtime/lib/flows/util.js). A wire of a link out that
// points nowhere is a value the runtime overwrites, not a dangling wire,
// and checking it would refuse legitimate link topologies.
func withoutLinkOutWires(item json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(item, &m); err != nil {
		return item
	}
	typ, ok := rawString(m["type"])
	if !ok || typ != "link out" {
		return item
	}
	if _, ok := m["links"]; !ok {
		return item
	}
	if _, ok := m["wires"]; !ok {
		return item
	}
	delete(m, "wires")
	out, err := json.Marshal(m)
	if err != nil {
		return item
	}
	return out
}

// flatSubflowPortIssues checks the ports of a subflow definition in a
// flat configuration, where the members of the definition are the entries
// that name it as their owner.
func flatSubflowPortIssues(defID string, items []json.RawMessage) []FlowIssue {
	members := map[string]bool{}
	var def json.RawMessage
	for _, item := range items {
		entry := decodeConfigEntry(item)
		if entry.Type == "subflow" && entry.ID == defID {
			def = item
			continue
		}
		if entry.Z == defID && entry.ID != "" {
			members[entry.ID] = true
		}
	}
	if def == nil {
		return nil
	}
	return subflowPortIssues(defID, members, def)
}

// subflowDefinitionPortIssues checks the ports of a nested subflow
// definition against its own members. The nested shape is what the
// subflow write path carries: the definition holds its nodes, its
// configs and its groups in arrays.
func subflowDefinitionPortIssues(def json.RawMessage) []FlowIssue {
	var meta struct {
		ID     string                       `json:"id"`
		Groups []map[string]json.RawMessage `json:"groups"`
	}
	if err := json.Unmarshal(def, &meta); err != nil {
		return nil
	}
	members, err := subflowDefinitionMembers(def)
	if err != nil {
		return nil
	}
	ids := map[string]bool{}
	for _, member := range members {
		if id := nodeID(member); id != "" {
			ids[id] = true
		}
	}
	// The groups of a definition are members too: the runtime resolves
	// the ports against the groups as well.
	for _, group := range meta.Groups {
		if id := nodeID(group); id != "" {
			ids[id] = true
		}
	}
	return subflowPortIssues(meta.ID, ids, def)
}

// subflowPortIssues reports the port wires of a subflow definition that
// name a node which is not in the definition.
//
// The runtime resolves the ports of a subflow against the definition's own
// members when it starts an instance: an "in" port that names a node which
// is not there makes the start throw — "TypeError: Cannot read properties
// of undefined (reading 'id')" in @node-red/runtime/lib/flows/Subflow.js
// — and an "out" port that names one logs "Unknown node referenced inside
// subflow" and the output never fires. The deployment itself answers 204
// in both cases, so the deployment looks like it worked while the flows do
// not start.
//
// The one reference that is legal and is not a member is the definition's
// own id on an "out" port: that is how a subflow wires its input straight
// to its output.
func subflowPortIssues(defID string, members map[string]bool, def json.RawMessage) []FlowIssue {
	var ports struct {
		In  []json.RawMessage `json:"in"`
		Out []json.RawMessage `json:"out"`
	}
	if err := json.Unmarshal(def, &ports); err != nil {
		return nil
	}
	var issues []FlowIssue
	issues = append(issues, portTargetIssues(defID, members, ports.In, "in")...)
	issues = append(issues, portTargetIssues(defID, members, ports.Out, "out")...)
	return issues
}

// portTargetIssues reports the wires of one port list that name a node
// which is not in the definition.
func portTargetIssues(defID string, members map[string]bool, ports []json.RawMessage, kind string) []FlowIssue {
	var issues []FlowIssue
	for _, port := range ports {
		var parsed struct {
			Wires []struct {
				ID json.RawMessage `json:"id"`
			} `json:"wires"`
		}
		if err := json.Unmarshal(port, &parsed); err != nil {
			continue
		}
		for _, wire := range parsed.Wires {
			id, ok := rawString(wire.ID)
			if !ok || id == "" {
				issues = append(issues, danglingSubflowPortIssue(defID, kind, ""))
				continue
			}
			if members[id] {
				continue
			}
			if kind == "out" && id == defID {
				// A subflow can wire its input straight to its output.
				continue
			}
			issues = append(issues, danglingSubflowPortIssue(defID, kind, id))
		}
	}
	return issues
}

// validateSubflowPorts refuses a subflow definition whose ports name a
// node that is not in it. The write path uses it: the runtime throws when
// it starts an instance of such a subflow, and the write that installed it
// had already answered with success.
func validateSubflowPorts(def json.RawMessage) error {
	issues := subflowDefinitionPortIssues(def)
	if len(issues) == 0 {
		return nil
	}
	messages := make([]string, 0, len(issues))
	for _, issue := range issues {
		messages = append(messages, issue.Message)
	}
	return &FlowGuardError{Message: strings.Join(messages, "; ")}
}

// danglingSubflowPortIssue builds the issue for a port wire that names
// a node which is not in the definition.
func danglingSubflowPortIssue(defID, kind, target string) FlowIssue {
	what := "an \"in\" port"
	if kind == "out" {
		what = "an \"out\" port"
	}
	if target == "" {
		return FlowIssue{
			Kind:    IssueDanglingSubflowPort,
			Target:  defID,
			Message: fmt.Sprintf("subflow %q has %s whose wire names no node; the runtime throws when it starts an instance of this subflow, and the deployment that installed it had already answered with success", defID, what),
		}
	}
	return FlowIssue{
		Kind:    IssueDanglingSubflowPort,
		Target:  target,
		Message: fmt.Sprintf("subflow %q has %s wired to %q, which is not a node of that subflow; the runtime throws when it starts an instance of this subflow, and the deployment that installed it had already answered with success", defID, what, target),
	}
}

// configEntry is one flat entry of a configuration, read the way the
// runtime reads it: a string field is a string, and a field of another
// type is not that field at all.
type configEntry struct {
	ID        string
	HasID     bool
	Type      string
	HasType   bool
	Z         string
	HasZ      bool
	ZIsString bool
}

// decodeConfigEntry reads the id, the type and the owner reference of one
// flat configuration entry.
func decodeConfigEntry(raw json.RawMessage) configEntry {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return configEntry{}
	}
	entry := configEntry{}
	if v, ok := m["id"]; ok {
		entry.HasID = true
		entry.ID, _ = rawString(v)
	}
	if v, ok := m["type"]; ok {
		entry.HasType = true
		entry.Type, _ = rawString(v)
	}
	if v, ok := m["z"]; ok {
		entry.HasZ = true
		entry.Z, entry.ZIsString = rawString(v)
	}
	return entry
}
