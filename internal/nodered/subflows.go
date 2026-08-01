package nodered

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// rand is the package-level reader for crypto-grade random bytes.
// Aliased to make the import obvious; using crypto/rand directly
// would be a one-character difference that hides the security
// intent of this generator.
var rand = cryptorand.Reader

// Subflow CRUD over the Node-RED admin API.
//
// The runtime's admin API does not expose subflow definitions through
// /flow/:id. Verified against node-red@5.0.1: GET, PUT and DELETE on
// /flow/<subflow-id> all 404 because subflows live in
// activeFlowConfig.subflows, not activeFlowConfig.flows (where getFlow/
// updateFlow/removeFlow look). POST /flow ignores any "type":"subflow"
// field on the body — the runtime's addFlow always sets type:"tab" on
// the new tab node.
//
// The only path that works is PUT /flow/global with a body of the form
// {"subflows":[...], "configs":[...]} (configs at global scope are
// preserved by echoing them back). The runtime's updateFlow('global')
// replaces the entire subflows array and flattens each subflow's
// nodes/configs into the flat config. Existing tabs and their z
// references are preserved.
//
// Every mutating helper here is therefore a fetch–modify–PUT: it reads
// /flow/global, mutates the subflows array, and writes it back. The
// read path lives in fetchGlobalSubflows; the write path is the
// shared putGlobalSubflows helper. Both take writeMu via the public
// methods so concurrent subflow edits do not race (same pattern as
// CreateFlow/UpdateFlow/DeleteFlow on tabs).
//
// The create/update calls also validate the body lightly: the
// subflows array must be well-formed and every subflow must carry a
// non-empty id. Anything else is a caller bug and gets caught before
// a backup is taken.

// SubflowList is a thin alias for []RawFlow so call sites that read
// the response stay readable ("subflows" rather than "an opaque blob
// that happens to be a subflow list").
type SubflowList []RawFlow

// fetchGlobalSubflows reads the current subflow definitions from the
// runtime. Returns an empty slice when /flow/global has no subflows
// key, which is the common case on a fresh instance.
//
// The endpoint returns the full global config; we extract the
// subflows array and re-marshal each entry so each item in the
// returned slice is a self-contained JSON object a caller can pipe
// back into a write without further massaging.
func (c *Client) fetchGlobalSubflows(ctx context.Context) (SubflowList, []json.RawMessage, error) {
	raw, err := c.getRaw(ctx, "/flow/global")
	if err != nil {
		return nil, nil, err
	}
	// The runtime returns {"id":"global","subflows":[...],"configs":[...]}:
	// extract the two arrays we need. Any other shape is a runtime that
	// drifted from the version we tested against; surface it instead of
	// silently dropping the subflows.
	var envelope struct {
		Subflows []json.RawMessage `json:"subflows"`
		Configs  []json.RawMessage `json:"configs"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, nil, fmt.Errorf("decoding /flow/global envelope: %w (body: %s)", err, string(raw))
	}
	out := make(SubflowList, 0, len(envelope.Subflows))
	for _, sf := range envelope.Subflows {
		out = append(out, RawFlow(sf))
	}
	return out, envelope.Configs, nil
}

// rawSlice marshals a slice of raw messages into a single raw array.
// An empty input becomes "[]" rather than "null" so the runtime does
// not see a missing key.
func rawSlice(items []json.RawMessage) json.RawMessage {
	if items == nil {
		return json.RawMessage(`[]`)
	}
	out, err := json.Marshal(items)
	if err != nil {
		// Items are already-valid raw messages; a marshal failure
		// here means the slice itself is broken. Fall back to "[]"
		// rather than panicking — the subsequent write will fail
		// with a clearer error than a runtime nil deref.
		return json.RawMessage(`[]`)
	}
	return out
}

// prepareSubflowForWrite lightly normalises a subflow definition: it
// must be a JSON object, must carry a non-empty id, and must have
// type="subflow" (the runtime is lenient about the latter but the
// editor requires it; we mirror that so an export/import round-trip
// is lossless).
func prepareSubflowForWrite(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, errors.New("subflow body is required")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("subflow is not a JSON object: %w", err)
	}
	var id string
	if err := json.Unmarshal(m["id"], &id); err != nil || id == "" {
		return nil, errors.New("subflow needs a non-empty id")
	}
	if t := stringField(m, "type"); t != "" && t != "subflow" {
		return nil, fmt.Errorf("subflow type must be \"subflow\" (got %q)", t)
	}
	return raw, nil
}

// ListSubflows returns every subflow definition currently installed
// in the runtime. The result is opaque JSON per item — call sites
// that need to inspect fields decode what they need; we keep the
// shape lossless so anything the editor can read, the MCP can
// reproduce.
func (c *Client) ListSubflows(ctx context.Context) (SubflowList, error) {
	list, _, err := c.fetchGlobalSubflows(ctx)
	return list, err
}

// GetSubflow returns one subflow definition by id. Fails with
// *APIError(404) when no such subflow exists, so the caller can
// distinguish "missing" from "empty list" the same way it does for
// flow tabs.
func (c *Client) GetSubflow(ctx context.Context, id string) (RawFlow, error) {
	if id == "" {
		return nil, errors.New("subflow id is required")
	}
	list, _, err := c.fetchGlobalSubflows(ctx)
	if err != nil {
		return nil, err
	}
	for _, sf := range list {
		var meta struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(sf, &meta); err != nil {
			continue
		}
		if meta.ID == id {
			return sf, nil
		}
	}
	return nil, &APIError{
		StatusCode: http.StatusNotFound,
		Method:     "GET",
		Path:       "/flow/global",
		Body:       fmt.Sprintf("no subflow with id %q", id),
	}
}

// CreateSubflow installs a new subflow definition. The runtime's
// subflows array is replaced wholesale, so we read the current
// definitions, append the new one, and write the lot back. Returns
// the subflow as stored (a copy of what was sent, with id and
// type:"subflow" preserved).
//
// Fails with an error if a subflow with the same id already exists;
// the caller should use UpdateSubflow for that case. Refusing keeps
// "create" idempotent in the operator-friendly direction: re-running
// the same create by mistake is loud, not silent.
func (c *Client) CreateSubflow(ctx context.Context, subflow json.RawMessage) (RawFlow, error) {
	defer c.writeGuard()()
	prepared, err := prepareSubflowForWrite(subflow)
	if err != nil {
		return nil, err
	}
	var newID string
	_ = json.Unmarshal(prepared, &struct {
		ID *string `json:"id"`
	}{ID: &newID})

	existing, configs, err := c.fetchGlobalSubflows(ctx)
	if err != nil {
		return nil, err
	}
	for _, sf := range existing {
		var meta struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(sf, &meta); err == nil && meta.ID == newID {
			return nil, fmt.Errorf("a subflow with id %q already exists; use update_subflow to change it", newID)
		}
	}
	all := make([]json.RawMessage, 0, len(existing)+1)
	for _, sf := range existing {
		all = append(all, json.RawMessage(sf))
	}
	all = append(all, prepared)
	if err := c.putGlobalSubflowsLocked(ctx, all, configs); err != nil {
		return nil, err
	}
	return prepared, nil
}

// UpdateSubflow replaces one subflow definition. Fails with
// *APIError(404) when the id is not present in the current set, so
// the caller can distinguish "missing" from "no change" without
// comparing snapshots.
func (c *Client) UpdateSubflow(ctx context.Context, id string, subflow json.RawMessage) error {
	defer c.writeGuard()()
	if id == "" {
		return errors.New("subflow id is required")
	}
	prepared, err := prepareSubflowForWrite(subflow)
	if err != nil {
		return err
	}
	// Force the id in the body to match the path argument. The editor
	// does the same: the id is a path component, not a body field.
	if !idMatchesBody(prepared, id) {
		return fmt.Errorf("subflow id in body does not match the path id %q", id)
	}

	existing, configs, err := c.fetchGlobalSubflows(ctx)
	if err != nil {
		return err
	}
	found := false
	all := make([]json.RawMessage, 0, len(existing))
	for _, sf := range existing {
		var meta struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(sf, &meta); err != nil {
			continue
		}
		if meta.ID == id {
			all = append(all, prepared)
			found = true
			continue
		}
		all = append(all, json.RawMessage(sf))
	}
	if !found {
		return &APIError{
			StatusCode: http.StatusNotFound,
			Method:     "PUT",
			Path:       "/flow/global",
			Body:       fmt.Sprintf("no subflow with id %q", id),
		}
	}
	return c.putGlobalSubflowsLocked(ctx, all, configs)
}

// DeleteSubflow removes a subflow definition. Fails with
// *APIError(404) when the id is not present, matching the way
// DeleteFlow behaves for tabs.
//
// Note: the runtime does not check that no instance of the subflow
// is in use before removing the definition. The next deploy will
// leave the instance nodes pointing at a missing subflow. We do not
// add a "is anyone using this?" pre-check because the editor does
// not either — the operator is expected to be aware.
func (c *Client) DeleteSubflow(ctx context.Context, id string) error {
	defer c.writeGuard()()
	if id == "" {
		return errors.New("subflow id is required")
	}
	existing, configs, err := c.fetchGlobalSubflows(ctx)
	if err != nil {
		return err
	}
	found := false
	all := make([]json.RawMessage, 0, len(existing))
	for _, sf := range existing {
		var meta struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(sf, &meta); err != nil {
			continue
		}
		if meta.ID == id {
			found = true
			continue
		}
		all = append(all, json.RawMessage(sf))
	}
	if !found {
		return &APIError{
			StatusCode: http.StatusNotFound,
			Method:     "PUT",
			Path:       "/flow/global",
			Body:       fmt.Sprintf("no subflow with id %q", id),
		}
	}
	return c.putGlobalSubflowsLocked(ctx, all, configs)
}

// putGlobalSubflowsLocked is the no-mutex twin of putGlobalSubflows;
// callers that already hold writeMu (CreateSubflow/UpdateSubflow/
// DeleteSubflow) use this so they do not re-enter the mutex.
func (c *Client) putGlobalSubflowsLocked(ctx context.Context, subflows []json.RawMessage, configs []json.RawMessage) error {
	if _, err := c.snapshotFlows(ctx); err != nil {
		return err
	}
	body := map[string]json.RawMessage{
		"subflows": rawSlice(subflows),
	}
	if len(configs) > 0 {
		body["configs"] = rawSlice(configs)
	}
	return c.do(ctx, "PUT", "/flow/global", body, nil)
}

// idMatchesBody reports whether the given subflow payload's id field
// matches id. An absent or empty id in the body counts as a mismatch
// — the caller passed a path id, the body must agree.
func idMatchesBody(raw json.RawMessage, id string) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	return stringField(m, "id") == id
}

// generateInstanceID returns a hex string suitable for a Node-RED
// node id. The runtime does not care about the format as long as it
// is unique within a tab; the editor uses random hex. 16 hex chars
// (8 bytes) gives 64 bits of entropy — enough that two instances
// generated in the same millisecond collide with negligible
// probability and far below the rate at which the runtime
// deduplicates ids anyway.
func generateInstanceID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should not fail on a healthy system; if it
		// does, fall back to a nanosecond timestamp so we still
		// produce something the runtime will accept.
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b[:])
}

// InstantiateSubflow adds a new instance of a subflow into a flow
// tab. The new node's "type" is set to "subflow:<subflowID>" — the
// format the runtime and the editor both expect — and its z is set
// to the owning tab.
//
// nodeID may be empty, in which case the runtime assigns one. A
// caller-provided id is recommended for two reasons: it makes
// connect_nodes immediately usable (its id-collision check needs a
// deterministic id) and it makes the resulting flow easy to refer
// to from later calls.
//
// The optional params object is merged into the instance node as-is:
// anything the caller puts there (name, x, y, wires, env, custom
// properties) lands in the resulting node. The runtime does not
// reject unknown keys on an instance node, so the caller can pass
// instance-level overrides (e.g. env values) without us having to
// know about them.
func (c *Client) InstantiateSubflow(ctx context.Context, flowID, subflowID string, instance json.RawMessage) (RawFlow, error) {
	if flowID == "" {
		return nil, errors.New("flow id is required")
	}
	if subflowID == "" {
		return nil, errors.New("subflow id is required")
	}
	if _, err := c.GetSubflow(ctx, subflowID); err != nil {
		return nil, err
	}
	// Build the instance node. We start from the caller's payload
	// (it may carry name, x, y, wires, env, custom props) and only
	// fill in the fields the runtime requires: id, type, z.
	node := map[string]json.RawMessage{}
	if len(instance) > 0 {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(instance, &m); err != nil {
			return nil, fmt.Errorf("instance is not a JSON object: %w", err)
		}
		for k, v := range m {
			node[k] = v
		}
	}
	if _, ok := node["id"]; !ok {
		node["id"] = json.RawMessage(`""`)
	}
	if _, ok := node["type"]; !ok {
		node["type"] = json.RawMessage(`""`)
	}
	if _, ok := node["z"]; !ok {
		node["z"] = json.RawMessage(`""`)
	}
	// Overwrite the three runtime-required fields with the values
	// the helper knows are correct, ignoring anything the caller
	// may have put in those slots by mistake. (The caller should
	// not have, and we surface no error — but we are not letting
	// an LLM typo "type":"subflow" instead of "subflow:abc" cause
	// a silently-broken instance.)
	node["type"] = json.RawMessage(fmt.Sprintf("%q", "subflow:"+subflowID))
	node["z"] = json.RawMessage(fmt.Sprintf("%q", flowID))
	if stringField(node, "id") == "" {
		// AddNode rejects an empty id (wires reference nodes by
		// id; an empty id would make the node unreferenceable).
		// Generate a random hex id in the Node-RED style so the
		// caller's no-params path still produces a usable node.
		node["id"] = json.RawMessage(fmt.Sprintf("%q", generateInstanceID()))
	}
	// Default canvas coordinates. AddNode refuses a node without
	// x/y — without them, the runtime files the node into the
	// "configs" collection where it never deploys. 140/140 matches
	// the editor's default for a fresh inject node; any caller-
	// supplied x/y wins.
	if _, ok := node["x"]; !ok {
		node["x"] = json.RawMessage(`140`)
	}
	if _, ok := node["y"]; !ok {
		node["y"] = json.RawMessage(`140`)
	}
	built, err := json.Marshal(node)
	if err != nil {
		return nil, fmt.Errorf("encoding instance node: %w", err)
	}
	if err := c.AddNode(ctx, flowID, built); err != nil {
		return nil, err
	}
	return RawFlow(built), nil
}
