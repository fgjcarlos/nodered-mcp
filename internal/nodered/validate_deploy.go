package nodered

import (
	"encoding/json"
	"fmt"
	"strconv"
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
//   - a wire that names no node of the owner it belongs to. This is the
//     repository's rule, shared with the tab write path, and it is a rule
//     rather than a fact about the runtime: the runtime does resolve a
//     wire across tabs (Flow.getNode falls back to the parent and the
//     global lookup scans every active flow), so such a wire delivers.
//     The editor never creates one, and both write paths refuse it, so
//     the configuration an operator installs and the configuration they
//     can then edit are judged by the same rule. A deployment of a
//     configuration that carries one is still possible: it is what the
//     explicit force argument of set_flows is for.
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
//     from the "links" array, so a wire of a link out that points
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
	// IssueInvalidLinks is a link node whose "links" field is not an
	// array. The runtime iterates that field with forEach when it parses
	// the configuration, so a truthy value that is not an array stops the
	// deploy with a TypeError.
	IssueInvalidLinks FlowIssueKind = "invalid_links"
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

// effectiveConfigIssues runs the effective-configuration rules over the
// flat entries of a configuration.
func effectiveConfigIssues(items []json.RawMessage) []FlowIssue {
	return newConfigIndex(items).issues()
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

// validateTabEntries judges the entries of one tab in the context of the
// whole configuration. It is what a write of a single tab needs: the
// entries of that tab are the caller's, and the rest of the
// configuration is the runtime's.
//
// The rule is the one that needs the whole configuration to judge: an
// instance of a subflow whose definition is not there. The runtime would
// install a node type it cannot resolve and leave the flows stopped. The
// other whole-configuration rules are not applied here: an id that is
// already used is caught by the pre-existing owner check of the write
// path, and a problem in another tab is not the caller's to answer for.
func validateTabEntries(items []json.RawMessage, tabID string) error {
	ix := newConfigIndex(items)
	var issues []FlowIssue
	for _, entry := range ix.entries {
		if entry.Z != tabID {
			continue
		}
		if entry.Type == "link in" || entry.Type == "link out" {
			issues = append(issues, ix.linksShapeIssue(entry)...)
		}
		if id, ok := subflowInstanceType(entry.TypeJS); ok && !ix.subflows[id] {
			issues = append(issues, missingSubflowIssue(entry, id))
		}
	}
	if len(issues) == 0 {
		return nil
	}
	return &ConfigValidationError{Issues: issues}
}

// configEntry is one flat entry of a configuration, read the way the
// runtime reads it: a string field is a string, and a field of another
// type is not that field at all.
type configEntry struct {
	Index   int
	ID      string
	HasID   bool
	Type    string
	HasType bool
	// TypeJS is the type the runtime compares against, after the
	// JavaScript String() coercion (see jsTypeString). The runtime
	// matches a node against /^subflow:(.+)$/ with that value, so a
	// type of ["subflow:x"] is the reference it looks like.
	TypeJS    string
	Z         string
	HasZ      bool
	ZIsString bool
	// ZRaw is the raw "z" value, kept so a message can quote what it
	// actually is when the value is not a string.
	ZRaw json.RawMessage
}

// decodeConfigEntry reads the id, the type and the owner reference of one
// flat configuration entry.
func decodeConfigEntry(raw json.RawMessage, index int) configEntry {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return configEntry{Index: index}
	}
	entry := configEntry{Index: index}
	if v, ok := m["id"]; ok {
		entry.HasID = true
		entry.ID, _ = rawString(v)
	}
	if v, ok := m["type"]; ok {
		entry.HasType = true
		entry.Type, _ = rawString(v)
		entry.TypeJS, _ = jsTypeString(v)
	}
	if v, ok := m["z"]; ok {
		entry.HasZ = true
		entry.ZRaw = v
		entry.Z, entry.ZIsString = rawString(v)
	}
	return entry
}

// ownerRef is a tab or a subflow definition in the configuration.
type ownerRef struct {
	id   string
	kind string
}

// configIndex is a configuration read once: the owners it declares, the
// entries by id, and the members of every owner.
//
// The index exists because the rules ask the same questions of the same
// entries many times. Reading the array once and answering from the index
// keeps the validation linear in the size of the configuration: a
// configuration of ten thousand entries used to cost seconds.
type configIndex struct {
	raws     []json.RawMessage
	entries  []configEntry
	owners   []ownerRef
	subflows map[string]bool
	members  map[string][]int
}

// newConfigIndex reads a configuration once.
func newConfigIndex(items []json.RawMessage) *configIndex {
	ix := &configIndex{
		subflows: map[string]bool{},
		members:  map[string][]int{},
	}
	for i, item := range items {
		entry := decodeConfigEntry(item, i)
		ix.raws = append(ix.raws, item)
		ix.entries = append(ix.entries, entry)
		switch entry.Type {
		case "tab", "subflow":
			// The runtime tells a tab from a subflow definition with a
			// strict comparison, and it keys its own maps by id, so two
			// owners with the same id are one owner: the duplicate is
			// reported by the duplicate-id rule, not twice over.
			if _, seen := ix.ownerIndex(entry.ID); !seen {
				ix.owners = append(ix.owners, ownerRef{id: entry.ID, kind: entry.Type})
			}
			if entry.Type == "subflow" {
				ix.subflows[entry.ID] = true
			}
			continue
		}
		if entry.Z != "" {
			ix.members[entry.Z] = append(ix.members[entry.Z], entry.Index)
		}
	}
	return ix
}

// ownerIndex returns the position of an owner in the index.
func (ix *configIndex) ownerIndex(id string) (int, bool) {
	for i, owner := range ix.owners {
		if owner.id == id {
			return i, true
		}
	}
	return 0, false
}

// issues runs every effective-configuration rule over the configuration.
func (ix *configIndex) issues() []FlowIssue {
	var issues []FlowIssue
	issues = append(issues, ix.duplicateIDIssues()...)
	issues = append(issues, ix.ownerRuleIssues()...)
	issues = append(issues, ix.orphanNodeIssues()...)
	issues = append(issues, ix.missingSubflowIssues()...)
	issues = append(issues, ix.linksShapeIssues()...)
	return issues
}

// linksShapeIssues reports the link nodes whose "links" field is not an
// array.
func (ix *configIndex) linksShapeIssues() []FlowIssue {
	var issues []FlowIssue
	for _, entry := range ix.entries {
		if entry.Type != "link in" && entry.Type != "link out" {
			continue
		}
		issues = append(issues, ix.linksShapeIssue(entry)...)
	}
	return issues
}

// duplicateIDIssues reports the entries that share an id. The runtime
// keys every node by its id, so the last entry with an id wins and the
// earlier ones are dropped without a word.
func (ix *configIndex) duplicateIDIssues() []FlowIssue {
	var issues []FlowIssue
	seen := map[string]bool{}
	for _, entry := range ix.entries {
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

// ownerRuleIssues runs the rules that are judged per owner — a tab or a
// subflow definition — over every owner of the configuration.
func (ix *configIndex) ownerRuleIssues() []FlowIssue {
	var issues []FlowIssue
	for _, owner := range ix.owners {
		doc, ok := ix.ownerDocument(owner.id)
		if !ok {
			continue
		}
		issues = append(issues, ownerIssues(doc)...)
		if owner.kind == "subflow" {
			issues = append(issues, ix.subflowPortIssues(owner.id)...)
		}
	}
	return issues
}

// ownerDocument builds the nested document of one owner — a tab or a
// subflow definition — out of the flat configuration.
//
// The runtime splits the entries the same way: an entry belongs to the
// tab or the subflow its "z" names, and the entries that carry canvas
// coordinates are the ones that run. The rest are configuration nodes.
func (ix *configIndex) ownerDocument(ownerID string) (RawFlow, bool) {
	nodes := []json.RawMessage{}
	configs := []json.RawMessage{}
	for _, index := range ix.members[ownerID] {
		item := withoutLinkOutWires(ix.raw(index))
		if hasCanvasCoords(item) {
			nodes = append(nodes, item)
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

// raw returns the entry at the given index.
func (ix *configIndex) raw(index int) json.RawMessage {
	return ix.raws[index]
}

// orphanNodeIssues reports the nodes whose "z" names no tab and no
// subflow definition in the configuration. The runtime invents a tab for
// such a node (parseConfig creates one for an unknown z), so the node
// runs outside the flow that is being deployed.
func (ix *configIndex) orphanNodeIssues() []FlowIssue {
	known := map[string]bool{}
	for _, owner := range ix.owners {
		known[owner.id] = true
	}
	var issues []FlowIssue
	for _, entry := range ix.entries {
		switch entry.Type {
		case "tab", "subflow", "group":
			continue
		}
		if issue := ix.ownerlessNodeIssue(entry, known); issue != nil {
			issues = append(issues, *issue)
		}
	}
	return issues
}

// ownerlessNodeIssue returns the issue for a node that belongs to no
// owner, or nil when the node names one.
func (ix *configIndex) ownerlessNodeIssue(entry configEntry, known map[string]bool) *FlowIssue {
	if entry.HasZ && entry.ZIsString && entry.Z != "" {
		if known[entry.Z] {
			return nil
		}
		issue := orphanNodeIssue(entry, entry.ZRaw)
		return &issue
	}
	// No z, an empty one, or one that is not a string. A configuration
	// node (no canvas coordinates) is filed at global scope by the
	// runtime, which is legal. A canvas node is not: the runtime looks
	// its container up by the z, finds none, and drops the node — it
	// never runs, and the flow looks fine and does nothing.
	if !hasCanvasCoords(ix.raw(entry.Index)) {
		return nil
	}
	issue := orphanNodeIssue(entry, entry.ZRaw)
	return &issue
}

// orphanNodeIssue builds the issue for a node that belongs to no owner.
//
// The runtime looks a node's container up by its z, and there are two
// ways to name none: a z that names nothing (the runtime invents a tab
// for the node, so it runs outside the flow that is being deployed) and
// no z at all on a canvas node (the runtime drops the node, so it never
// runs). The message says which of the two it is.
func orphanNodeIssue(entry configEntry, z json.RawMessage) FlowIssue {
	symptom := "the runtime invents a tab for it, so the node runs outside the flow you are deploying"
	if entry.Z == "" {
		symptom = "the runtime drops it, so the node never runs and the flow looks fine and does nothing"
	}
	owner := fmt.Sprintf("references z=%s", jsonValueText(z))
	if !entry.HasZ {
		owner = "has no z"
	}
	return FlowIssue{
		Kind:    IssueOrphanNode,
		NodeID:  entry.ID,
		Target:  entry.Z,
		Message: fmt.Sprintf("node %q %s, which is neither a tab nor a subflow definition in this configuration; %s", entry.ID, owner, symptom),
	}
}

// missingSubflowIssues reports the instances of a subflow whose
// definition is not in the configuration. A full deployment replaces the
// whole configuration, so such an instance would be left with a node type
// the runtime cannot resolve — and a runtime with a missing node type
// refuses to start the flows at all.
func (ix *configIndex) missingSubflowIssues() []FlowIssue {
	var issues []FlowIssue
	for _, entry := range ix.entries {
		id, ok := subflowInstanceType(entry.TypeJS)
		if !ok || ix.subflows[id] {
			continue
		}
		issues = append(issues, missingSubflowIssue(entry, id))
	}
	return issues
}

// missingSubflowIssue builds the issue for an instance whose definition
// is not in the configuration.
func missingSubflowIssue(entry configEntry, id string) FlowIssue {
	return FlowIssue{
		Kind:    IssueMissingSubflow,
		NodeID:  entry.ID,
		Target:  id,
		Message: fmt.Sprintf("node %q is an instance of subflow %q, but this configuration carries no definition for it; a full deployment would leave the runtime with a node type it cannot resolve, and it then refuses to start the flows", entry.ID, id),
	}
}

// linksShapeIssue reports a link node whose "links" field is not an
// array. The runtime iterates that field with forEach when it parses the
// configuration, so a truthy value that is not an array stops the deploy
// with a TypeError.
func (ix *configIndex) linksShapeIssue(entry configEntry) []FlowIssue {
	raw, ok := ix.field(entry.Index, "links")
	if !ok {
		return nil
	}
	if !jsTruthy(raw) {
		// The runtime tests the field for truthiness, so a null or an
		// empty string leaves the node alone.
		return nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		return nil
	}
	return []FlowIssue{{
		Kind:    IssueInvalidLinks,
		NodeID:  entry.ID,
		Message: fmt.Sprintf("node %q is a link node whose links field is not an array; the runtime iterates that field when it parses the configuration and the deploy stops with a TypeError", entry.ID),
	}}
}

// field returns a raw field of the entry at the given index.
func (ix *configIndex) field(index int, name string) (json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(ix.raws[index], &m); err != nil {
		return nil, false
	}
	v, ok := m[name]
	return v, ok
}

// subflowPortIssues checks the ports of a subflow definition, whose
// members are the entries that name it as their owner.
func (ix *configIndex) subflowPortIssues(defID string) []FlowIssue {
	members := map[string]bool{}
	for _, index := range ix.members[defID] {
		if id := ix.entries[index].ID; id != "" {
			members[id] = true
		}
	}
	return subflowPortIssues(defID, members, ix.ownerRaw(defID))
}

// ownerRaw returns the entry that declares the owner.
func (ix *configIndex) ownerRaw(ownerID string) json.RawMessage {
	for _, entry := range ix.entries {
		if (entry.Type == "tab" || entry.Type == "subflow") && entry.ID == ownerID {
			return ix.raw(entry.Index)
		}
	}
	return nil
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

// danglingSubflowPortIssue builds the issue for a port wire that names
// a node which is not in the definition.
func danglingSubflowPortIssue(defID, kind, target string) FlowIssue {
	what := "an \"in\" port"
	symptom := "the runtime throws when it starts an instance of this subflow"
	if kind == "out" {
		what = "an \"out\" port"
		symptom = "the runtime logs \"Unknown node referenced inside subflow\" and the output never fires"
	}
	if target == "" {
		return FlowIssue{
			Kind:    IssueDanglingSubflowPort,
			Target:  defID,
			Message: fmt.Sprintf("subflow %q has %s whose wire names no node; %s, and the deployment that installed it had already answered with success", defID, what, symptom),
		}
	}
	return FlowIssue{
		Kind:    IssueDanglingSubflowPort,
		Target:  target,
		Message: fmt.Sprintf("subflow %q has %s wired to %q, which is not a node of that subflow; %s, and the deployment that installed it had already answered with success", defID, what, target, symptom),
	}
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

// ownerIssues runs the tab-level rules over one owner's document.
//
// The duplicate-id rule is skipped here: the whole-configuration pass
// reports it, and it also covers the cross-owner case a per-document
// rule cannot see.
func ownerIssues(doc RawFlow) []FlowIssue {
	var out []FlowIssue
	for _, issue := range ValidateFlow(doc) {
		switch issue.Kind {
		case IssueDuplicateID, IssueInvalidLinks:
			// Reported once, by the whole-configuration pass: it covers
			// the cross-owner case a per-document rule cannot see.
			continue
		}
		out = append(out, issue)
	}
	return out
}

// subflowInstanceType returns the subflow id a node's type refers to, the
// way the runtime reads it: the type is coerced with the JavaScript
// String() and matched against /^subflow:(.+)$/ (see jsTypeString).
func subflowInstanceType(typ string) (string, bool) {
	if !strings.HasPrefix(typ, "subflow:") {
		return "", false
	}
	id := strings.TrimPrefix(typ, "subflow:")
	if id == "" {
		return "", false
	}
	return id, true
}

// withoutLinkOutWires returns the node with its "wires" removed when the
// runtime would recompute them.
//
// The runtime owns the wires of a "link out" node: when the node carries a
// "links" array, parseConfig replaces its wires with the link targets
// (@node-red/runtime/lib/flows/util.js). A wire of a link out that
// points nowhere is a value the runtime overwrites, not a dangling wire,
// and checking it would refuse legitimate link topologies.
//
// The test is the runtime's: it checks the field for truthiness, so a
// "links" field of null (or of any other falsy value) leaves the wires
// alone and they are checked like any other node's.
func withoutLinkOutWires(item json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(item, &m); err != nil {
		return item
	}
	typ, ok := rawString(m["type"])
	if !ok || typ != "link out" {
		return item
	}
	links, ok := m["links"]
	if !ok || !jsTruthy(links) {
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

// jsTruthy reports the JavaScript truthiness of a JSON value. It is how
// the runtime tests the "links" field of a link node: an empty array is
// truthy, a null is not.
func jsTruthy(raw json.RawMessage) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t != ""
	default:
		return true
	}
}

// jsonValueText renders a raw JSON value for a message: a string is
// quoted, anything else is shown as it is.
func jsonValueText(raw json.RawMessage) string {
	if s, ok := rawString(raw); ok {
		return strconv.Quote(s)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "null"
	}
	return trimmed
}
