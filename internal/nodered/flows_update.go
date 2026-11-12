package nodered

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

type revisionedFlows struct {
	Rev   string            `json:"rev"`
	Flows []json.RawMessage `json:"flows"`
}

func apiV2(r *http.Request) { r.Header.Set("Node-RED-API-Version", "v2") }

// flowSnapshot is the configuration a write is built on: the array, the
// revision it was read at, and the raw body the backup is taken from.
//
// The three belong together. A configuration and a revision read from two
// different responses can describe two different states of the runtime, and
// a deployment built that way carries one state with the revision of the
// other: the runtime accepts it, and whatever changed in between is gone.
type flowSnapshot struct {
	raw   RawFlow
	rev   string
	flows []json.RawMessage
}

// readFlowSnapshot reads the whole configuration and the revision that goes
// with it in one request.
func (c *Client) readFlowSnapshot(ctx context.Context) (*flowSnapshot, error) {
	var raw RawFlow
	if err := c.do(ctx, "GET", "/flows", nil, &raw, apiV2); err != nil {
		return nil, fmt.Errorf("backup: fetching current flows: %w", err)
	}
	var current revisionedFlows
	if err := json.Unmarshal(raw, &current); err != nil || current.Rev == "" || current.Flows == nil {
		return nil, errors.New("updating a flow requires a v2 /flows response with rev and flows")
	}
	return &flowSnapshot{raw: raw, rev: current.Rev, flows: current.Flows}, nil
}

// flowTabNotFound is the answer for a tab the current configuration does not
// hold. The path is the one the caller asked about, not the one that was
// read: the tab is missing from the runtime, wherever it was looked for.
func flowTabNotFound(id string) error {
	return &APIError{StatusCode: http.StatusNotFound, Method: "GET", Path: "/flow/" + id, Body: "flow tab not found in current configuration"}
}

// deployFlowUpdate replaces only the owning tab's entries. PUT /flow/:id
// appends the tab in Node-RED; a revision-checked partial deployment does not.
func (c *Client) deployFlowUpdate(ctx context.Context, id string, flow RawFlow) error {
	snap, err := c.readFlowSnapshot(ctx)
	if err != nil {
		return err
	}
	return c.deployFlowUpdateOn(ctx, id, flow, snap)
}

// deployFlowUpdateOn deploys the tab on the snapshot the document was built
// on: the entries of the tab are replaced in that snapshot's array, and the
// deployment carries that snapshot's revision.
func (c *Client) deployFlowUpdateOn(ctx context.Context, id string, flow RawFlow, snap *flowSnapshot) error {
	next, err := replaceFlowEntries(snap.flows, id, flow)
	if err != nil {
		return err
	}
	if err := validateDeployedTab(next, id, flow); err != nil {
		return err
	}
	// Issue #334: this deploy posts the whole array, so the rules that
	// need the whole configuration are applied to it as well — but only
	// to the entries of the tab being written. The rest of the
	// configuration is the runtime's own state, and a problem there is
	// not the caller's to answer for: refusing it would make the tab
	// impossible to repair.
	if err := validateTabEntries(next, id); err != nil {
		return err
	}
	if _, err := c.saveFlowSnapshot(snap.raw); err != nil {
		return err
	}
	return c.do(ctx, "POST", "/flows", revisionedFlows{Rev: snap.rev, Flows: next}, nil, apiV2, func(r *http.Request) {
		r.Header.Set("Node-RED-Deployment-Type", "flows")
	})
}

// flowEntry rejects ambiguous flat configurations rather than deploying a
// truncated interpretation. Unknown fields stay raw throughout the merge.
func flowEntry(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || nodeID(m) == "" || nodeType(m) == "" {
		return nil, errors.New("flow entries require non-empty id and type fields")
	}
	return m, nil
}

func indexFlowEntries(items []json.RawMessage) (map[string]map[string]json.RawMessage, error) {
	index := make(map[string]map[string]json.RawMessage, len(items))
	for _, raw := range items {
		m, err := flowEntry(raw)
		if err != nil {
			return nil, err
		}
		id := nodeID(m)
		if _, ok := index[id]; ok {
			return nil, fmt.Errorf("duplicate flow entry id %q", id)
		}
		index[id] = m
	}
	return index, nil
}

// flattenFlowUpdate converts the nested tab (and any submitted subflows) into
// replacement entries. Collection members without z inherit their owner as in
// the single-flow API; subflow in/out/env and other metadata are kept intact.
func flattenFlowUpdate(raw RawFlow, id, kind string) ([]json.RawMessage, map[string]bool, error) {
	doc, err := decodeFlow(raw)
	if err != nil {
		return nil, nil, err
	}
	if doc.Extra == nil {
		return nil, nil, errors.New("flow document must be a JSON object")
	}
	if bodyID := nodeID(doc.Extra); bodyID != "" && bodyID != id {
		return nil, nil, errors.New("flow id in body does not match the path id")
	}
	owners := map[string]bool{id: true}
	var subflows []json.RawMessage
	if err := json.Unmarshal(doc.Extra["subflows"], &subflows); doc.Extra["subflows"] != nil && err != nil {
		return nil, nil, fmt.Errorf("unreadable subflows array: %w", err)
	}
	delete(doc.Extra, "subflows")
	doc.Extra["id"], _ = json.Marshal(id)
	doc.Extra["type"], _ = json.Marshal(kind)
	top, _ := json.Marshal(doc.Extra)
	entries := []json.RawMessage{top}
	for _, node := range append(doc.Nodes, doc.Configs...) {
		if node == nil {
			return nil, nil, errors.New("flow nodes and configs must be objects")
		}
		if stringField(node, "z") == "" {
			node["z"], _ = json.Marshal(id)
		}
		encoded, _ := json.Marshal(node)
		entries = append(entries, encoded)
	}
	for _, sf := range subflows {
		prepared, err := prepareSubflowForWrite(sf)
		if err != nil {
			return nil, nil, err
		}
		var meta map[string]json.RawMessage
		_ = json.Unmarshal(prepared, &meta)
		nested, nestedOwners, err := flattenFlowUpdate(RawFlow(prepared), nodeID(meta), "subflow")
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, nested...)
		for owner := range nestedOwners {
			owners[owner] = true
		}
	}
	return entries, owners, nil
}

func replaceFlowEntries(items []json.RawMessage, id string, flow RawFlow) ([]json.RawMessage, error) {
	existing, err := indexFlowEntries(items)
	if err != nil {
		return nil, err
	}
	if nodeType(existing[id]) != "tab" {
		return nil, flowTabNotFound(id)
	}
	submitted, owners, err := flattenFlowUpdate(flow, id, "tab")
	if err != nil {
		return nil, err
	}
	replacements, err := indexFlowEntries(submitted)
	if err != nil {
		return nil, err
	}
	if err := validateReplacementOwners(existing, replacements, owners); err != nil {
		return nil, err
	}
	next := make([]json.RawMessage, 0, len(items)+len(submitted))
	for _, raw := range items {
		var m map[string]json.RawMessage
		_ = json.Unmarshal(raw, &m)
		key := nodeID(m)
		if replacement, ok := replacements[key]; ok {
			next = append(next, overlayFlowEntry(raw, m, replacement))
			delete(replacements, key)
		} else if !owners[stringField(m, "z")] {
			next = append(next, raw)
		}
	}
	for _, raw := range submitted {
		var m map[string]json.RawMessage
		_ = json.Unmarshal(raw, &m)
		if _, ok := replacements[nodeID(m)]; ok {
			next = append(next, raw)
		}
	}
	return next, nil
}

// Nested GET can omit tab metadata and credentials. Preserve those omissions,
// but replace operational node properties: restoring an omitted wires/repeat/etc.
// field would change the submitted behavior, potentially after validation.
func overlayFlowEntry(raw json.RawMessage, existing, replacement map[string]json.RawMessage) json.RawMessage {
	before, _ := json.Marshal(existing)
	if nodeType(existing) == "tab" {
		for key, value := range replacement {
			existing[key] = value
		}
		replacement = existing
	} else if _, supplied := replacement["credentials"]; !supplied {
		if credentials, hidden := existing["credentials"]; hidden {
			replacement["credentials"] = credentials
		}
	}
	after, _ := json.Marshal(replacement)
	if bytes.Equal(before, after) {
		return raw
	}
	return after
}

// Validate the actual replacement nodes, including nodes whose z references
// another node in the submitted tab. Filtering only z == id would omit those
// valid members. Unrelated existing tabs are deliberately not revalidated.
func validateDeployedTab(entries []json.RawMessage, id string, submitted RawFlow) error {
	actual, err := indexFlowEntries(entries)
	if err != nil {
		return err
	}
	doc, err := decodeFlow(submitted)
	if err != nil {
		return err
	}
	for _, collection := range []*[]map[string]json.RawMessage{&doc.Nodes, &doc.Configs} {
		for i, node := range *collection {
			(*collection)[i] = actual[nodeID(node)]
		}
	}
	tab, err := doc.encode()
	if err != nil {
		return err
	}
	if err := validateFlowWires(tab); err != nil {
		return err
	}
	return validateZRefsInFlow(tab, id)
}

func validateReplacementOwners(existing, replacements map[string]map[string]json.RawMessage, owners map[string]bool) error {
	for id, replacement := range replacements {
		old, ok := existing[id]
		if !ok {
			continue
		}
		if owners[stringField(old, "z")] {
			continue
		}
		if owners[id] && nodeType(old) == nodeType(replacement) {
			continue
		}
		return fmt.Errorf("flow entry %q belongs outside the updated flow", id)
	}
	return nil
}
