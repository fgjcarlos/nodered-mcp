package nodered

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Granular edits on a single flow tab.
//
// Without these, changing one property means round-tripping an entire tab
// through a model: read it, reproduce every node verbatim, send it back. Each
// pass is a chance to drop a field it did not recognise or to rewrite a wires
// array by hand. These functions touch only what was asked for and leave every
// other byte of every other node exactly as it was.
//
// They all work on the document GET /flow/:id returns — {"id","label","nodes"}
// — and they all return a new document rather than mutating in place.

// editFlow reads a tab, applies one granular edit, and writes it back.
//
// The write goes through UpdateFlow, so every edit inherits the same guardrails
// as a hand-written one: the wires are validated and a backup is taken before
// anything reaches the runtime.
//
// This is read-modify-write against a live instance. Two concurrent editors
// still race — the loser's change is overwritten — but the window is one round
// trip rather than however long a model takes to rewrite a whole tab.
func (c *Client) editFlow(ctx context.Context, flowID string, edit func(RawFlow) (RawFlow, error)) error {
	defer c.writeGuard()()
	if flowID == "" {
		return errors.New("flow id is required")
	}
	current, err := c.GetFlow(ctx, flowID)
	if err != nil {
		return err
	}
	next, err := edit(current)
	if err != nil {
		return err
	}
	// updateFlowLocked is the no-mutex twin of UpdateFlow; we already hold
	// writeMu here and a public call would deadlock on the non-reentrant
	// sync.Mutex.
	return c.updateFlowLocked(ctx, flowID, next)
}

// AddNode appends a node to a flow tab.
func (c *Client) AddNode(ctx context.Context, flowID string, node json.RawMessage) error {
	return c.editFlow(ctx, flowID, func(f RawFlow) (RawFlow, error) {
		return AddNodeToFlow(f, node)
	})
}

// UpdateNode merges properties into one node, leaving the rest of it intact.
func (c *Client) UpdateNode(ctx context.Context, flowID, nodeID string, patch map[string]json.RawMessage) error {
	return c.editFlow(ctx, flowID, func(f RawFlow) (RawFlow, error) {
		return UpdateNodeInFlow(f, nodeID, patch)
	})
}

// DeleteNode removes a node and any wires pointing at it.
func (c *Client) DeleteNode(ctx context.Context, flowID, nodeID string) error {
	return c.editFlow(ctx, flowID, func(f RawFlow) (RawFlow, error) {
		return DeleteNodeFromFlow(f, nodeID)
	})
}

// ConnectNodes wires one node's output port to another node.
func (c *Client) ConnectNodes(ctx context.Context, flowID, fromID string, port int, toID string) error {
	return c.editFlow(ctx, flowID, func(f RawFlow) (RawFlow, error) {
		return ConnectNodesInFlow(f, fromID, port, toID)
	})
}

// flowDoc is a tab decoded just far enough to reach the objects it holds.
//
// Node-RED keeps a tab's contents in two arrays and decides between them by one
// rule, in runtime/lib/flows/util.js: an object carrying both x and y canvas
// coordinates goes into "nodes", anything else into "configs". Shared brokers,
// server definitions and credential holders therefore live in "configs" even
// though they belong to the tab. Editing has to honour that split, or a node
// filed into the wrong array vanishes from the canvas.
//
// Every other tab property stays in Extra as raw bytes and is written back
// untouched.
type flowDoc struct {
	Extra   map[string]json.RawMessage
	Nodes   []map[string]json.RawMessage
	Configs []map[string]json.RawMessage
}

// decodeFlow splits a tab document into its two object collections and
// everything else.
func decodeFlow(raw RawFlow) (*flowDoc, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("not a valid flow document: %w", err)
	}
	doc := &flowDoc{Extra: top}

	for key, into := range map[string]*[]map[string]json.RawMessage{
		"nodes":   &doc.Nodes,
		"configs": &doc.Configs,
	} {
		raw, ok := top[key]
		if !ok {
			// Both keys are optional: Node-RED omits configs entirely when a
			// flow has none, and an empty tab has no nodes.
			continue
		}
		if err := json.Unmarshal(raw, into); err != nil {
			return nil, fmt.Errorf("flow document has an unreadable %s array: %w", key, err)
		}
		delete(doc.Extra, key)
	}
	return doc, nil
}

// encode rebuilds the tab document from its parts.
func (d *flowDoc) encode() (RawFlow, error) {
	out := make(map[string]json.RawMessage, len(d.Extra)+2)
	for k, v := range d.Extra {
		out[k] = v
	}

	nodes, err := json.Marshal(d.Nodes)
	if err != nil {
		return nil, fmt.Errorf("re-encoding nodes: %w", err)
	}
	out["nodes"] = nodes

	// Node-RED drops the key when there are no config nodes; emitting an empty
	// array would show up as a difference on every write.
	if len(d.Configs) > 0 {
		configs, err := json.Marshal(d.Configs)
		if err != nil {
			return nil, fmt.Errorf("re-encoding configs: %w", err)
		}
		out["configs"] = configs
	}

	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("re-encoding flow: %w", err)
	}
	return encoded, nil
}

// hasCanvasPosition reports whether a node carries the x/y coordinates that
// decide which collection Node-RED files it under.
func hasCanvasPosition(node map[string]json.RawMessage) bool {
	_, hasX := node["x"]
	_, hasY := node["y"]
	return hasX && hasY
}

// nodeID reads a node's id, or "" when it has none.
func nodeID(node map[string]json.RawMessage) string {
	raw, ok := node["id"]
	if !ok {
		return ""
	}
	var id string
	if json.Unmarshal(raw, &id) != nil {
		return ""
	}
	return id
}

// locate finds a node by id in either collection, returning the collection it
// lives in and its position. The collection is returned as a pointer so callers
// can remove from it. found is false when no such id exists.
func (d *flowDoc) locate(id string) (collection *[]map[string]json.RawMessage, index int, found bool) {
	for _, c := range []*[]map[string]json.RawMessage{&d.Nodes, &d.Configs} {
		for i, n := range *c {
			if nodeID(n) == id {
				return c, i, true
			}
		}
	}
	return nil, 0, false
}

// exists reports whether any object in the tab already uses this id.
func (d *flowDoc) exists(id string) bool {
	_, _, found := d.locate(id)
	return found
}

// AddNodeToFlow appends a node to a tab. The node is stored as supplied; only
// its id and references are inspected, to reject a collision or a write that
// would leave the runtime with dangling config-node references.
func AddNodeToFlow(flow RawFlow, node json.RawMessage) (RawFlow, error) {
	doc, err := decodeFlow(flow)
	if err != nil {
		return nil, err
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(node, &decoded); err != nil {
		return nil, fmt.Errorf("node is not a valid JSON object: %w", err)
	}
	id := nodeID(decoded)
	if id == "" {
		return nil, fmt.Errorf("node needs a non-empty id: wires reference nodes by id")
	}
	// Node-RED accepts duplicate ids and then behaves according to ordering,
	// which is a bug that surfaces far from its cause. Check both collections:
	// an id shared with a config node collides just as badly.
	if doc.exists(id) {
		return nil, fmt.Errorf("a node with id %q already exists in this flow", id)
	}
	// A node without x/y lands in the "configs" collection, where Node-RED
	// never indexes it as a deployable canvas node — inject nodes placed
	// there never fire, and wires pointing at them look correct yet route
	// nowhere. Reject explicitly so the caller fixes the payload rather
	// than chasing a flow that "looks fine but does nothing".
	if !hasCanvasPosition(decoded) {
		return nil, fmt.Errorf(
			"node %q (%s) is missing x/y canvas coordinates; add them before calling add_node "+
				"(a typical inject node carries x:140,y:140 — Node-RED files anything without "+
				"coords into the configs collection and the runtime will not deploy it)",
			id, nodeType(decoded),
		)
	}
	// The runtime crashes with `Cannot read properties of undefined (reading
	// 'wires')` if a node references a z that does not resolve — a config
	// node referenced from a canvas node, or a canvas node pointing at an
	// unknown tab. Verify the z either is the owning tab or names an existing
	// node in this doc.
	if err := validateZRef(doc, stringField(decoded, "z")); err != nil {
		return nil, fmt.Errorf("node %q (%s): %w", id, nodeType(decoded), err)
	}

	doc.Nodes = append(doc.Nodes, decoded)
	return doc.encode()
}

// validateZRef returns nil if z resolves to the owning tab or to an existing
// node in this document. Returning a non-nil error fails the write loud
// rather than letting Node-RED silently rewrite a bad z to the owning tab id
// (which loses the wire the model intended — issue #99).
//
// Used by the add / update write paths to surface the bad z before the
// runtime accepts it. The read-only validate_flow tool reuses the same rule
// via checkZRef so the validator and the write path agree.
func validateZRef(doc *flowDoc, z string) error {
	if z == "" {
		return nil
	}
	if tabID := stringField(doc.Extra, "id"); z == tabID {
		return nil
	}
	if doc.exists(z) {
		return nil
	}
	return fmt.Errorf(
		"references z=%q which is neither the owning tab nor an existing "+
			"node in this flow: Node-RED's runtime will crash on deploy with "+
			"'Cannot read properties of undefined (reading wires)' if this is written",
		z,
	)
}

// nodeType reads a node's type field, returning "" when absent or unreadable.
func nodeType(node map[string]json.RawMessage) string {
	var t string
	if err := json.Unmarshal(node["type"], &t); err != nil {
		return ""
	}
	return t
}

// stringField reads a top-level string field, returning "" when absent.
func stringField(m map[string]json.RawMessage, key string) string {
	var s string
	if err := json.Unmarshal(m[key], &s); err != nil {
		return ""
	}
	return s
}

// UpdateNodeInFlow merges patch into one node's properties. Keys present in
// patch are replaced; every other property — including ones this package knows
// nothing about — is left byte-for-byte intact.
func UpdateNodeInFlow(flow RawFlow, id string, patch map[string]json.RawMessage) (RawFlow, error) {
	if id == "" {
		return nil, fmt.Errorf("node id is required")
	}
	if len(patch) == 0 {
		return nil, fmt.Errorf("nothing to update: the patch is empty")
	}
	// Renaming a node orphans every wire that points at it, and the damage
	// shows up as a flow that silently stops running.
	if _, ok := patch["id"]; ok {
		return nil, fmt.Errorf("a node's id cannot be changed: wires reference it")
	}

	doc, err := decodeFlow(flow)
	if err != nil {
		return nil, err
	}
	collection, i, found := doc.locate(id)
	if !found {
		return nil, fmt.Errorf("no node with id %q in this flow", id)
	}

	for k, v := range patch {
		(*collection)[i][k] = v
	}
	// A patch may rewrite z to a value that does not name the owning tab
	// or any existing node in this flow. Node-RED silently rewrites such
	// z to the owning tab id, which loses the wire the model intended —
	// issue #99. Same check AddNodeToFlow runs on the new-node path.
	if err := validateZRef(doc, stringField((*collection)[i], "z")); err != nil {
		return nil, fmt.Errorf("node %q (%s): %w", id, nodeType((*collection)[i]), err)
	}
	return doc.encode()
}

// DeleteNodeFromFlow removes a node and every wire pointing at it. Leaving the
// wires behind would produce exactly the dangling references the write path
// rejects — and that Node-RED itself accepts in silence.
func DeleteNodeFromFlow(flow RawFlow, id string) (RawFlow, error) {
	if id == "" {
		return nil, fmt.Errorf("node id is required")
	}
	doc, err := decodeFlow(flow)
	if err != nil {
		return nil, err
	}
	collection, i, found := doc.locate(id)
	if !found {
		return nil, fmt.Errorf("no node with id %q in this flow", id)
	}

	*collection = append((*collection)[:i], (*collection)[i+1:]...)

	// Only canvas nodes carry wires, but sweeping both collections costs
	// nothing and cannot miss a reference.
	for _, node := range append(append([]map[string]json.RawMessage{}, doc.Nodes...), doc.Configs...) {
		wires, err := decodeWires(node)
		if err != nil || wires == nil {
			continue
		}
		changed := false
		for p, port := range wires {
			kept := port[:0:0]
			for _, target := range port {
				if target == id {
					changed = true
					continue
				}
				kept = append(kept, target)
			}
			wires[p] = kept
		}
		if changed {
			if err := setWires(node, wires); err != nil {
				return nil, err
			}
		}
	}
	return doc.encode()
}

// ConnectNodesInFlow wires an output port of one node to another. The port is
// appended to, not replaced, and the wires array grows if the port index is
// beyond its current length — a switch node's later outputs do not exist in
// the array until something uses them.
func ConnectNodesInFlow(flow RawFlow, fromID string, port int, toID string) (RawFlow, error) {
	if fromID == "" || toID == "" {
		return nil, fmt.Errorf("both a source and a target node id are required")
	}
	// Wiring a node to itself is an instant infinite loop at runtime: the
	// source fires, the wire routes the message back to the same source,
	// which fires again, and Node-RED's event loop saturates on the first
	// inject. Refuse it explicitly so the caller fixes the topology rather
	// than discovering it as a runtime that hangs on deploy.
	if fromID == toID {
		return nil, fmt.Errorf("cannot wire node %q to itself: creates an infinite message loop", fromID)
	}
	if port < 0 || port > 999 {
		return nil, fmt.Errorf("port %d is out of range (0-999); most nodes have 1 output and switch nodes have at most a few", port)
	}

	doc, err := decodeFlow(flow)
	if err != nil {
		return nil, err
	}
	fromCollection, from, found := doc.locate(fromID)
	if !found {
		return nil, fmt.Errorf("no node with id %q in this flow", fromID)
	}
	if !doc.exists(toID) {
		return nil, fmt.Errorf("cannot wire to %q: no node with that id in this flow", toID)
	}

	wires, err := decodeWires((*fromCollection)[from])
	if err != nil {
		return nil, err
	}
	for len(wires) <= port {
		wires = append(wires, []string{})
	}
	// Node-RED tolerates a duplicated target but then delivers the message
	// twice, which reads as a flow that fires everything twice.
	for _, existing := range wires[port] {
		if existing == toID {
			return doc.encode()
		}
	}
	wires[port] = append(wires[port], toID)

	if err := setWires((*fromCollection)[from], wires); err != nil {
		return nil, err
	}
	return doc.encode()
}

// decodeWires reads a node's wires array. A node without wires (a config node,
// for instance) yields nil, which callers treat as "nothing to do".
func decodeWires(node map[string]json.RawMessage) ([][]string, error) {
	raw, ok := node["wires"]
	if !ok {
		return nil, nil
	}
	var wires [][]string
	if err := json.Unmarshal(raw, &wires); err != nil {
		return nil, fmt.Errorf("node %q has an unreadable wires array: %w", nodeID(node), err)
	}
	return wires, nil
}

func setWires(node map[string]json.RawMessage, wires [][]string) error {
	encoded, err := json.Marshal(wires)
	if err != nil {
		return fmt.Errorf("re-encoding wires: %w", err)
	}
	node["wires"] = encoded
	return nil
}
