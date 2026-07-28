package nodered

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

// ListFlows returns the full active flow configuration exactly as the admin
// API returns it, as opaque JSON. The shape depends on the Node-RED API
// version negotiated: a bare array (v1) or a {"rev":..,"flows":[..]} envelope
// (v2). We keep it opaque so nothing is ever lost — callers that need to
// inspect it use FlowTabCount / extractFlowArray.
func (c *Client) ListFlows(ctx context.Context) (RawFlow, error) {
	var raw RawFlow
	if err := c.do(ctx, "GET", "/flows", nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// GetFlow returns a single flow tab by ID as opaque JSON.
//
// Node-RED only serves GET /flow/:id once the runtime has indexed the tab,
// which it does lazily when the editor opens it. Newly deployed tabs 404
// here even though they are present in GET /flows. When that happens we
// fall back to /flows and synthesize the nested shape ({id, label, nodes,
// configs}) from the flat array — the same shape the indexed endpoint
// returns — so the caller does not have to special-case the freshly-deployed
// state. If the id is not in /flows either, the original 404 is returned.
func (c *Client) GetFlow(ctx context.Context, id string) (RawFlow, error) {
	if id == "" {
		return nil, errors.New("flow id is required")
	}
	var raw RawFlow
	err := c.do(ctx, "GET", "/flow/"+id, nil, &raw)
	if err == nil {
		return raw, nil
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		return nil, err
	}
	// ponytail: single-tab fallback path; not worth a dedicated method.
	// Add one when /flows grows large enough that the filter is noticeable.
	flat, listErr := c.ListFlows(ctx)
	if listErr != nil {
		return nil, err
	}
	synth, ok := synthesizeFlowFromFlat(flat, id)
	if !ok {
		return nil, err
	}
	return synth, nil
}

// synthesizeFlowFromFlat rebuilds the nested tab doc ({id, label, nodes,
// configs}) for the tab with the given id out of a GET /flows array.
// Returns (nil, false) when no such tab exists in the array.
func synthesizeFlowFromFlat(flat RawFlow, id string) (RawFlow, bool) {
	items := extractFlowArray(flat)
	var tabRaw, nodeRaws, configRaws []json.RawMessage
	for _, item := range items {
		var meta struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Z    string `json:"z"`
		}
		if err := json.Unmarshal(item, &meta); err != nil {
			continue
		}
		switch {
		case meta.Type == "tab" && meta.ID == id:
			tabRaw = append(tabRaw, item)
		case meta.Z == id && meta.Type != "subflow":
			if hasCanvasCoords(item) {
				nodeRaws = append(nodeRaws, item)
			} else {
				configRaws = append(configRaws, item)
			}
		}
	}
	if len(tabRaw) == 0 {
		return nil, false
	}
	var tab map[string]json.RawMessage
	if err := json.Unmarshal(tabRaw[0], &tab); err != nil {
		return nil, false
	}
	// Drop the markers that belong to the flat shape, not the nested one.
	delete(tab, "type")
	delete(tab, "id")
	if nodeRaws == nil {
		nodeRaws = []json.RawMessage{}
	}
	nodesBytes, err := json.Marshal(nodeRaws)
	if err != nil {
		return nil, false
	}
	tab["nodes"] = nodesBytes
	if len(configRaws) > 0 {
		configsBytes, err := json.Marshal(configRaws)
		if err != nil {
			return nil, false
		}
		tab["configs"] = configsBytes
	}
	tab["id"] = json.RawMessage(strconv.Quote(id))
	out, err := json.Marshal(tab)
	if err != nil {
		return nil, false
	}
	return RawFlow(out), true
}

// hasCanvasCoords reports whether the raw node carries x and y coordinates.
// Mirrors the split edit.go uses to file nodes into the right collection.
func hasCanvasCoords(raw json.RawMessage) bool {
	var probe struct {
		X json.RawMessage `json:"x"`
		Y json.RawMessage `json:"y"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return len(probe.X) > 0 && len(probe.Y) > 0
}

// CreateFlow creates a new flow tab. The flow document is opaque JSON, sent
// verbatim; Node-RED assigns the ID and returns the created flow. Wires are
// validated first, then a backup of the current config is taken (fail-closed:
// no write if backup fails).
func (c *Client) CreateFlow(ctx context.Context, flow RawFlow) (RawFlow, error) {
	defer c.writeGuard()()
	if err := validateFlowWires(flow); err != nil {
		return nil, err
	}
	if _, err := c.snapshotFlows(ctx); err != nil {
		return nil, err
	}
	var resp RawFlow
	if err := c.do(ctx, "POST", "/flow", flow, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// UpdateFlow replaces the contents of an existing flow tab. The flow document
// is opaque JSON, sent verbatim (PUT replaces the whole document). Wires are
// validated first, then a backup of the current config is taken (fail-closed).
//
// Takes writeMu so concurrent mutations on the same client do not race.
// Callers that already hold the lock (e.g. editFlow) must call
// updateFlowLocked instead.
func (c *Client) UpdateFlow(ctx context.Context, id string, flow RawFlow) error {
	defer c.writeGuard()()
	return c.updateFlowLocked(ctx, id, flow)
}

// updateFlowLocked is UpdateFlow without taking writeMu. Internal helpers
// that already hold the lock call this; external callers use UpdateFlow.
func (c *Client) updateFlowLocked(ctx context.Context, id string, flow RawFlow) error {
	if id == "" {
		return errors.New("flow id is required")
	}
	if err := validateFlowWires(flow); err != nil {
		return err
	}
	if _, err := c.snapshotFlows(ctx); err != nil {
		return err
	}
	return c.do(ctx, "PUT", "/flow/"+id, flow, nil)
}

// DeleteFlow removes a flow tab and all its nodes. A backup of the current
// config is taken first (fail-closed) so the delete can be rolled back.
func (c *Client) DeleteFlow(ctx context.Context, id string) error {
	defer c.writeGuard()()
	if id == "" {
		return errors.New("flow id is required")
	}
	if _, err := c.snapshotFlows(ctx); err != nil {
		return err
	}
	return c.do(ctx, "DELETE", "/flow/"+id, nil, nil)
}

// RestoreFlows overwrites the ENTIRE flow config with the contents of a
// previously saved backup (a full deployment). Because this replaces
// everything, it snapshots the current config first — so a restore can itself
// be undone — and only then deploys the backup.
//
// The backup is whatever GET /flows returned when it was taken (bare array or
// {rev,flows} envelope); we extract just the flow array and POST it as a bare
// array so the stale rev never triggers a 409 conflict.
func (c *Client) RestoreFlows(ctx context.Context, backup RawFlow) error {
	defer c.writeGuard()()
	arr := extractFlowArray(backup)
	if arr == nil {
		return errors.New("backup does not contain a recognizable flow array")
	}
	body, err := json.Marshal(arr)
	if err != nil {
		return fmt.Errorf("re-encoding backup: %w", err)
	}
	if _, err := c.snapshotFlows(ctx); err != nil {
		return err
	}
	return c.do(ctx, "POST", "/flows", json.RawMessage(body), nil, func(r *http.Request) {
		r.Header.Set("Node-RED-Deployment-Type", "full")
	})
}

// InjectNode fires the trigger on a node of type "inject" by calling
// POST /inject/:id. This does not change persisted config, so no backup.
//
// Guard: Node-RED's /inject/:id endpoint only acts on type "inject".
// Other node types (comment, debug, link-in) accept the call silently
// and return success without firing anything — the audit of v0.5.12
// (28 Jul 2026) caught exactly this on a comment node. We look up the
// node's type via GET /flows first and refuse if it is not "inject",
// so the operator sees a typed error naming the actual type instead
// of a phantom success.
func (c *Client) InjectNode(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("inject node id is required")
	}
	nodeType, ok, err := c.nodeType(ctx, id)
	if err != nil {
		// Lookup failed (network, parsing, runtime). Surface the
		// lookup error rather than silently firing on a node whose
		// type we could not verify — better to fail loud than to
		// repeat the v0.5.12 false-positive.
		return fmt.Errorf("verifying inject node type: %w", err)
	}
	if !ok {
		// No such node. Pass through to /inject/:id so the runtime's
		// 404 distinguishes "missing" from "wrong type" for the
		// operator (matches the audit's observation that the HTTP
		// path already returns 404 on unknown ids).
		return c.do(ctx, "POST", "/inject/"+id, nil, nil)
	}
	if nodeType != "inject" {
		return fmt.Errorf("node %q is type %q, not \"inject\"; only inject nodes can be fired", id, nodeType)
	}
	return c.do(ctx, "POST", "/inject/"+id, nil, nil)
}

// InjectNodeWithBody fires the trigger on an inject node by calling
// POST /inject/:id with a JSON body. Unlike InjectNode, it does NOT
// re-verify the node's type — the caller is expected to know the type
// (used for the MCP-managed context helper, which is a fixed shape).
//
// The Node-RED 5.x handler at /inject/:id forwards the body to
// node.receive(body) ONLY when the body carries the special
// "__user_inject_props__" field. Without that field the body is
// silently discarded and the inject fires with its configured
// properties only. Callers that want the body to become the inject's
// msg (so a downstream function node can read it) MUST include
// "__user_inject_props__": [] (or any array) in the body — the value
// is the inject's per-call prop override, and an empty array makes
// the inject pass msg through unchanged.
func (c *Client) InjectNodeWithBody(ctx context.Context, id string, body json.RawMessage) error {
	if id == "" {
		return errors.New("inject node id is required")
	}
	if len(body) == 0 {
		return errors.New("inject body is required when using InjectNodeWithBody")
	}
	return c.do(ctx, "POST", "/inject/"+id, body, nil)
}

// nodeType returns the Node-RED type ("inject", "comment", "debug",
// ...) for the node with the given id, plus a boolean for "node
// found". The boolean lets the caller distinguish "missing" (false)
// from "found but not what you wanted" (true, type != expected).
//
// It walks the GET /flows document — same path SearchFlows uses for
// its id-agnostic scan — rather than reading one tab. A node's type
// can be queried cheaply this way, and the result is independent of
// which tab the node lives in. Cost: one extra HTTP GET per
// inject_node call. The audit did not flag latency as a problem, so
// we keep the simple uncached path; cache if a future audit does.
func (c *Client) nodeType(ctx context.Context, id string) (string, bool, error) {
	raw, err := c.ListFlows(ctx)
	if err != nil {
		return "", false, err
	}
	for _, item := range extractFlowArray(raw) {
		var m nodeMeta
		if json.Unmarshal(item, &m) != nil {
			continue
		}
		if m.Type == "tab" || m.Type == "subflow" {
			continue
		}
		if m.ID == id {
			return m.Type, true, nil
		}
	}
	return "", false, nil
}

// FlowTabCount returns how many flow tabs (objects with "type":"tab") appear
// in a raw GET /flows response. It tolerates both the bare-array (API v1) and
// the {"flows":[...]} envelope (API v2). Returns 0 on any parse failure.
func FlowTabCount(raw RawFlow) int {
	n := 0
	for _, item := range extractFlowArray(raw) {
		var meta struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(item, &meta) == nil && meta.Type == "tab" {
			n++
		}
	}
	return n
}

// SetFlowDisabled flips the "disabled" flag on an existing flow tab. The
// write goes through the same editFlow read-modify-write path as the
// granular node tools, so the same guardrails apply (wire validation,
// snapshot backup, write-mutex). The only field changed on the document
// is "disabled"; every other byte — including nodes, configs, env, and
// unknown fields — round-trips untouched.
//
// Setting disabled=true stops the tab from running; the runtime still
// holds the parsed flow in memory, so flipping it back to false does not
// require a deploy. Node-RED's editor exposes the same toggle as the
// checkbox next to each tab in the sidebar.
func (c *Client) SetFlowDisabled(ctx context.Context, id string, disabled bool) error {
	return c.editFlow(ctx, id, func(f RawFlow) (RawFlow, error) {
		return SetDisabledInFlow(f, disabled)
	})
}

// SetDisabledInFlow flips the "disabled" key on a flow document, returning
// the updated document. The document must be a single tab (the shape used
// by POST /flow and PUT /flow/:id).
//
// The "disabled" key is encoded as JSON `true` or `false`. The output is
// always explicit: writing the empty/default value is not free — an operator
// who toggles the flag once does not want a stray schema change later.
func SetDisabledInFlow(flow RawFlow, disabled bool) (RawFlow, error) {
	doc, err := decodeFlow(flow)
	if err != nil {
		return nil, err
	}
	if doc.Extra == nil {
		doc.Extra = map[string]json.RawMessage{}
	}
	encoded, err := json.Marshal(disabled)
	if err != nil {
		return nil, fmt.Errorf("encoding disabled flag: %w", err)
	}
	doc.Extra["disabled"] = encoded
	return doc.encode()
}

// validateFlowWires is the write-path guard: it returns the first structural
// issue that would cause the runtime to behave unexpectedly, so the write
// is refused up front. The full list of issues is reported by ValidateFlow
// for callers that want to show every problem at once (the validate_flow
// tool). Both share the same underlying rules — only the output shape
// differs.
//
// A document the runtime cannot parse at all is also refused; the prior
// implementation returned that error directly from json.Unmarshal.
func validateFlowWires(raw RawFlow) error {
	issues := ValidateFlow(raw)
	for _, issue := range issues {
		switch issue.Kind {
		case IssueDanglingWire:
			return fmt.Errorf("node %q wires to unknown node %q", issue.NodeID, issue.Target)
		case "invalid_document":
			return errors.New(issue.Message)
		}
	}
	return nil
}

// extractFlowArray pulls the flat list of flow objects out of a GET /flows
// response regardless of API-version envelope. Returns nil if neither shape
// parses.
func extractFlowArray(raw RawFlow) []json.RawMessage {
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		return arr
	}
	var env struct {
		Flows []json.RawMessage `json:"flows"`
	}
	if json.Unmarshal(raw, &env) == nil {
		return env.Flows
	}
	return nil
}
