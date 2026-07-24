package nodered

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
func (c *Client) GetFlow(ctx context.Context, id string) (RawFlow, error) {
	if id == "" {
		return nil, errors.New("flow id is required")
	}
	var raw RawFlow
	if err := c.do(ctx, "GET", "/flow/"+id, nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// CreateFlow creates a new flow tab. The flow document is opaque JSON, sent
// verbatim; Node-RED assigns the ID and returns the created flow. Wires are
// validated first, then a backup of the current config is taken (fail-closed:
// no write if backup fails).
func (c *Client) CreateFlow(ctx context.Context, flow RawFlow) (RawFlow, error) {
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
func (c *Client) UpdateFlow(ctx context.Context, id string, flow RawFlow) error {
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
func (c *Client) InjectNode(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("inject node id is required")
	}
	return c.do(ctx, "POST", "/inject/"+id, nil, nil)
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

// validateFlowWires checks that every wire in a flow document points at a node
// that exists within the same document. Node-RED accepts dangling wire targets
// silently, leaving broken connections — this catches an LLM referencing a
// node that isn't there before the write reaches the runtime.
//
// It expects a single flow-tab object ({"label":..,"nodes":[..]}), the shape
// used by POST /flow and PUT /flow/:id. Config-node references (properties like
// an MQTT node's "broker") are NOT validated: those can legitimately live
// outside a single flow document.
func validateFlowWires(raw RawFlow) error {
	var doc struct {
		Nodes []struct {
			ID    string     `json:"id"`
			Type  string     `json:"type"`
			Wires [][]string `json:"wires"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("not a valid flow document: %w", err)
	}

	ids := make(map[string]bool, len(doc.Nodes))
	for _, n := range doc.Nodes {
		if n.ID != "" {
			ids[n.ID] = true
		}
	}
	for _, n := range doc.Nodes {
		for _, port := range n.Wires {
			for _, target := range port {
				if !ids[target] {
					return fmt.Errorf("node %q (%s) wires to unknown node %q", n.ID, n.Type, target)
				}
			}
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
