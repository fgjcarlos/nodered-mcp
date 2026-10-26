package nodered

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
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
	// A subflow definition carries its own nodes, so it is judged like any
	// other document: a forbidden type nested inside it is refused before
	// the global configuration is read.
	if err := c.deniedNodeType(prepared); err != nil {
		return nil, err
	}
	// Issue #334: a subflow definition is a flow document too, so it
	// shares the guard the tab write path applies. Its inner nodes have
	// their own wires, and a wire that names a node which is not in the
	// definition never delivers; a node whose z names another tab is
	// filed there by the runtime, and the subflow comes out empty.
	var newID string
	_ = json.Unmarshal(prepared, &struct {
		ID *string `json:"id"`
	}{ID: &newID})
	if err := validateFlowWires(RawFlow(prepared)); err != nil {
		return nil, err
	}
	if err := validateZRefsInFlow(RawFlow(prepared), newID); err != nil {
		return nil, err
	}
	if err := validateSubflowPorts(prepared); err != nil {
		return nil, err
	}

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
	// A subflow definition carries its own nodes, so it is judged like any
	// other document: a forbidden type nested inside it is refused before
	// the global configuration is read.
	if err := c.deniedNodeType(prepared); err != nil {
		return err
	}
	// Force the id in the body to match the path argument. The editor
	// does the same: the id is a path component, not a body field.
	//
	// The check comes before the guard below: a body whose id does not
	// match the path id is a typo, and the guard would report it as a
	// topology problem instead.
	if !idMatchesBody(prepared, id) {
		return fmt.Errorf("subflow id in body does not match the path id %q", id)
	}
	// Issue #334: the definition shares the guard the tab write path
	// applies — the same wires rule and the same owner rule — so a
	// subflow cannot be written with a topology the tab path would
	// refuse.
	if err := validateFlowWires(RawFlow(prepared)); err != nil {
		return err
	}
	if err := validateZRefsInFlow(RawFlow(prepared), id); err != nil {
		return err
	}
	if err := validateSubflowPorts(prepared); err != nil {
		return err
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

// Owner kinds reported for the place a subflow instance lives.
const (
	// SubflowOwnerTab is a flow tab: the common case.
	SubflowOwnerTab = "tab"
	// SubflowOwnerSubflow is another subflow definition: the instance is
	// nested inside it.
	SubflowOwnerSubflow = "subflow"
	// SubflowOwnerGlobal is the global configuration: a node with no "z",
	// which Node-RED files at global scope.
	SubflowOwnerGlobal = "global"
	// SubflowOwnerUnknown is an owner the runtime configuration does not
	// name: a dangling "z", or a "z" that is not a string.
	SubflowOwnerUnknown = "unknown"
)

// SubflowReference points at one place where a subflow definition is
// still in use. The only thing that keeps a definition alive is an
// instance node — a node whose type is "subflow:<id>" — because the
// runtime resolves that type at deploy time.
type SubflowReference struct {
	// Owner is the id of the tab or of the subflow definition that
	// holds the instance node. Empty when the owner is unknown.
	Owner string
	// OwnerKind is one of SubflowOwnerTab, SubflowOwnerSubflow,
	// SubflowOwnerGlobal or SubflowOwnerUnknown.
	OwnerKind string
	// NodeID is the id of the instance node itself. Empty when the node
	// carries no usable id.
	NodeID string
}

// describe renders the reference as one operator-readable line.
func (r SubflowReference) describe() string {
	node := fmt.Sprintf("instance node %q", r.NodeID)
	if r.NodeID == "" {
		node = "an instance node with an unreadable id"
	}
	switch r.OwnerKind {
	case SubflowOwnerTab:
		return fmt.Sprintf("%s in tab %q", node, r.Owner)
	case SubflowOwnerSubflow:
		return fmt.Sprintf("%s in subflow %q", node, r.Owner)
	case SubflowOwnerGlobal:
		return fmt.Sprintf("%s in the global configuration", node)
	default:
		if r.Owner == "" {
			return fmt.Sprintf("%s in an owner that cannot be named", node)
		}
		return fmt.Sprintf("%s in an unrecognised owner %q", node, r.Owner)
	}
}

// SubflowInUseError is returned when a subflow definition cannot be
// deleted because the runtime still holds instance nodes pointing at
// it.
//
// Deleting the definition anyway is the failure this error exists to
// prevent: the instance nodes keep their "subflow:<id>" type, the
// runtime can no longer resolve it, and the next start logs
// "Waiting for missing types to be registered" and the affected flows
// never deploy. The editor lets the operator do exactly that, which
// is why the behaviour was worth closing here.
//
// The error carries every reference at once: an operator who has to
// delete one instance, retry, and discover the next one has a much
// worse time than one who is handed the full list up front.
type SubflowInUseError struct {
	ID   string
	Refs []SubflowReference
}

func (e *SubflowInUseError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "subflow %q is still in use and was not deleted: %d reference(s) must be removed first", e.ID, len(e.Refs))
	for _, ref := range e.Refs {
		fmt.Fprintf(&b, "\n  - %s", ref.describe())
	}
	b.WriteString("\nremove the instance nodes first: delete_node for an instance in a flow tab, " +
		"update_subflow for an instance nested in another subflow definition. " +
		"The definition itself was left untouched")
	return b.String()
}

// flowItem is one entry of a flat flow configuration, decoded with the
// tolerance the reference scan needs.
//
// A field that is present but not a JSON string is *unknown*, not
// absent: the node still exists and the runtime still resolves its
// type. Decoding the whole entry into a struct of string fields would
// instead drop the entire entry on the first non-string value, which
// would hide a real reference — the exact failure this check exists to
// prevent.
type flowItem struct {
	ID     string
	IDOK   bool
	Type   string
	TypeOK bool
	Z      string
	ZOK    bool
	// ZPresent tells an absent "z" from a "z" that is not a string.
	// The two mean different things: Node-RED files a node with no "z"
	// at global scope, while a malformed "z" names no owner at all.
	ZPresent bool
}

// decodeFlowItem reads the fields the reference scan needs out of one
// flat flow entry. A non-object entry yields an item with nothing
// known: the runtime's own missing-type scan only walks node objects,
// so there is nothing to match there either.
func decodeFlowItem(raw json.RawMessage) flowItem {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return flowItem{}
	}
	it := flowItem{}
	if v, ok := m["id"]; ok {
		it.ID, it.IDOK = rawString(v)
	}
	if v, ok := m["type"]; ok {
		it.Type, it.TypeOK = jsTypeString(v)
	}
	if v, ok := m["z"]; ok {
		it.ZPresent = true
		it.Z, it.ZOK = rawString(v)
	}
	return it
}

// rawString reads a JSON string, reporting whether the value was
// actually a string.
func rawString(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// jsTypeString returns the string the Node-RED runtime compares against
// /^subflow:(.+)$/ for a node's "type" field.
//
// The runtime does not require the type to be a string: it coerces the
// value with String() before matching, and String(["subflow:x"]) is
// "subflow:x". A one-element array type therefore IS a reference to a
// subflow, and treating a non-string type as "not an instance" would
// leave the guard fail-open for that shape.
//
// The coercion follows JavaScript's rules for the JSON value types:
// null is "null", booleans are "true" and "false", an array is its
// elements joined with "," (with null and undefined as empty strings),
// and an object is "[object Object]".
//
// ok is false when the value cannot be coerced at all. The caller then
// treats the node as a possible reference: "cannot be checked" is not
// "no references".
func jsTypeString(raw json.RawMessage) (string, bool) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", false
	}
	return jsString(v), true
}

// jsString reproduces JavaScript's String() for the JSON value types.
func jsString(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		if t {
			return "true"
		}
		return "false"
	case string:
		return t
	case float64:
		return jsNumber(t)
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			if e == nil {
				// Array.prototype.join maps null and undefined to
				// the empty string.
				continue
			}
			parts[i] = jsString(e)
		}
		return strings.Join(parts, ",")
	case map[string]any:
		return "[object Object]"
	}
	return ""
}

// jsNumber formats a JSON number the way JavaScript's Number::toString
// does.
//
// The runtime compares a node's type against /^subflow:(.+)$/ after
// coercing it with String(), and a number can reach that comparison
// inside an array type. The rendering therefore has to match
// JavaScript's exactly: Go's own formatting says "1e-07" where
// JavaScript says "1e-7", and that difference is enough for the guard
// to miss a reference.
//
// The algorithm is the one in the ECMAScript specification: take the
// shortest decimal digits that round-trip, then render them as a plain
// decimal or in exponential form depending on where the decimal point
// falls.
func jsNumber(f float64) string {
	if f == 0 {
		// String(-0) is "0" in JavaScript.
		return "0"
	}
	if f < 0 {
		return "-" + jsNumber(-f)
	}
	if math.IsInf(f, 1) {
		return "Infinity"
	}
	// The shortest round-tripping digits, in the form "d.ddde±XX".
	sci := strconv.FormatFloat(f, 'e', -1, 64)
	mantissa, expPart, _ := strings.Cut(sci, "e")
	exp, err := strconv.Atoi(expPart)
	if err != nil {
		return sci
	}
	digits := strings.Replace(mantissa, ".", "", 1)
	k := len(digits)
	// n is where the decimal point falls relative to the digits: the
	// value is digits × 10^(n-k).
	n := exp + 1
	switch {
	case k <= n && n <= 21:
		return digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return "0." + strings.Repeat("0", -n) + digits
	default:
		exponent := n - 1
		sign := "+"
		if exponent < 0 {
			sign = "-"
			exponent = -exponent
		}
		if k == 1 {
			return digits + "e" + sign + strconv.Itoa(exponent)
		}
		return digits[:1] + "." + digits[1:] + "e" + sign + strconv.Itoa(exponent)
	}
}

// subflowFlowItems extracts the flow array from a /flows body.
//
// It is deliberately stricter than FlowArray: the reference scan has to
// fail closed, and FlowArray answers "no items" for a body it does not
// recognise — an unreadable configuration would then look like a
// configuration without instances, which is the one answer the guard
// must never give.
func subflowFlowItems(raw json.RawMessage) ([]json.RawMessage, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		if arr == nil {
			// "null" decodes into a nil slice without an error.
			return nil, errors.New("the flow configuration is null")
		}
		return arr, nil
	}
	var env struct {
		Flows []json.RawMessage `json:"flows"`
	}
	if err := json.Unmarshal(raw, &env); err == nil {
		if env.Flows == nil {
			return nil, errors.New("the flow configuration carries no flows array")
		}
		return env.Flows, nil
	}
	return nil, errors.New("the flow configuration is neither a JSON array nor a flows envelope")
}

// subflowDefinitionMembers returns the member objects of a subflow
// definition: the "nodes" collection and the "configs" collection.
//
// A Node-RED subflow carries both, and an instance can live in either —
// a subflow instance with no canvas coordinates is filed in "configs",
// and the runtime resolves its type just the same.
//
// A definition with neither key yields an empty slice. A member
// collection that is not an array of objects is an error rather than an
// empty slice: the references of such a definition cannot be checked,
// and treating "cannot be checked" as "no references" would silently
// reopen the hole this check closes.
func subflowDefinitionMembers(def json.RawMessage) ([]map[string]json.RawMessage, error) {
	var m struct {
		Nodes   []map[string]json.RawMessage `json:"nodes"`
		Configs []map[string]json.RawMessage `json:"configs"`
	}
	if err := json.Unmarshal(def, &m); err != nil {
		return nil, fmt.Errorf("unreadable member collections: %w", err)
	}
	members := make([]map[string]json.RawMessage, 0, len(m.Nodes)+len(m.Configs))
	members = append(members, m.Nodes...)
	members = append(members, m.Configs...)
	return members, nil
}

// subflowOwnerKinds maps the id of every tab and subflow definition
// in a flat flow configuration to its kind. It is what tells an
// instance node living in a tab apart from one living inside another
// subflow definition — both carry the same "subflow:<id>" type.
//
// Tabs and subflow definitions live in separate id maps inside the
// runtime (flow.flows and flow.subflows), so the same id can name both.
// The runtime resolves the owner of a node tab-first
// (flow.flows[n.z] || flow.subflows[n.z]), so a tab shadows a subflow
// definition with the same id. Tabs are therefore recorded last, and
// the answer does not depend on the order of the flat array.
func subflowOwnerKinds(items []json.RawMessage) map[string]string {
	kinds := make(map[string]string)
	for _, item := range items {
		it := decodeFlowItem(item)
		if !it.IDOK || it.Type != "subflow" {
			continue
		}
		kinds[it.ID] = SubflowOwnerSubflow
	}
	for _, item := range items {
		it := decodeFlowItem(item)
		if !it.IDOK || it.Type != "tab" {
			continue
		}
		kinds[it.ID] = SubflowOwnerTab
	}
	return kinds
}

// subflowReferences finds every place the runtime still uses the
// subflow definition id.
//
// Two shapes have to be covered:
//
//   - an instance node in a flow tab, listed in GET /flows;
//   - an instance node nested inside another subflow's definition,
//     listed in the "nodes" or "configs" collection of that definition.
//
// GET /flows carries both shapes flattened: Node-RED keeps every node
// of the configuration in one flat array, and a node nested in a
// subflow definition appears there with "z" set to the subflow's id.
// The definition scan is the second witness for the nested shape, and
// it is the one that can tell a nested instance from a tab instance
// when the flat array does not name the owner.
//
// definitions is the set of subflow definitions the caller already
// read for the delete itself, so the nested scan costs no extra
// request.
func (c *Client) subflowReferences(ctx context.Context, id string, definitions SubflowList) ([]SubflowReference, error) {
	wantType := "subflow:" + id
	var refs []SubflowReference
	seen := make(map[string]bool)

	add := func(ref SubflowReference) {
		// The owner kind is deliberately not part of the key: the same
		// instance can be seen by both scans, and reporting it twice
		// with contradictory owners ("in subflow sf_b" and "in tab
		// sf_b") is worse than reporting it once.
		key := ref.Owner + "|" + ref.NodeID
		if seen[key] {
			return
		}
		seen[key] = true
		refs = append(refs, ref)
	}

	// The members of the definition being deleted. An instance inside
	// it goes away with the definition, and the runtime does not
	// report a missing type for it — so those node ids are excluded
	// from the flat scan below.
	//
	// The exclusion is by node id, not by owner. Tabs and subflow
	// definitions live in separate id maps inside the runtime, so a tab
	// can carry the same id as the definition: an instance whose owner
	// is that id may belong to the tab, and skipping it would drop a
	// real reference. The member list read from /flow/global is the
	// membership the runtime itself resolved, so it is the one list
	// that answers "is this node inside the definition".
	selfMembers := make(map[string]bool)

	// Instances nested inside other subflow definitions.
	for _, def := range definitions {
		defItem := decodeFlowItem(def)
		members, err := subflowDefinitionMembers(def)
		if err != nil {
			// Name the definition that cannot be read, and the one
			// that was asked for: without both, the operator cannot
			// tell which definition to repair.
			return nil, fmt.Errorf("subflow definition %q cannot be checked for references to %q: %w", defItem.ID, id, err)
		}
		if defItem.ID == id {
			// The definition being deleted is not a reference to
			// itself.
			for _, member := range members {
				if memberID := nodeID(member); memberID != "" {
					selfMembers[memberID] = true
				}
			}
			continue
		}
		for _, member := range members {
			if !memberIsInstance(member, wantType) {
				continue
			}
			add(SubflowReference{
				Owner:     defItem.ID,
				OwnerKind: SubflowOwnerSubflow,
				NodeID:    nodeID(member),
			})
		}
	}

	// Instances in flow tabs. GET /flows carries the whole active
	// configuration, tabs and their nodes included.
	flat, err := c.ListFlows(ctx)
	if err != nil {
		return nil, err
	}
	items, err := subflowFlowItems(flat)
	if err != nil {
		return nil, fmt.Errorf("the flow configuration could not be read: %w", err)
	}
	kinds := subflowOwnerKinds(items)
	for _, item := range items {
		it := decodeFlowItem(item)
		if !it.TypeOK {
			// The type could not be read at all, so the node cannot
			// be classified. "Cannot be checked" is not "no
			// references": the delete is refused.
			return nil, fmt.Errorf("a node in the flow configuration has an unreadable type, so its references to %q could not be checked", id)
		}
		if it.Type != wantType {
			continue
		}
		if it.IDOK && selfMembers[it.ID] {
			// The instance lives inside the definition being
			// deleted. It goes away with it, and the runtime does
			// not report a missing type for it.
			continue
		}
		owner, kind := it.Z, kinds[it.Z]
		switch {
		case !it.ZPresent:
			// A node with no "z" is filed at global scope.
			owner, kind = "", SubflowOwnerGlobal
		case !it.ZOK:
			// The owner field is not a string, so the owner cannot be
			// named. The instance is still real.
			owner, kind = "", SubflowOwnerUnknown
		case kind == "":
			// The owner is not a tab or a subflow definition the
			// configuration names. The instance still exists, so
			// the owner is reported as unknown rather than
			// guessed.
			kind = SubflowOwnerUnknown
		}
		add(SubflowReference{Owner: owner, OwnerKind: kind, NodeID: it.ID})
	}
	return refs, nil
}

// memberIsInstance reports whether a member of a subflow definition is
// an instance of the given subflow.
//
// The type is compared the way the runtime compares it: coerced with
// String() and matched against /^subflow:(.+)$/. A member whose type
// cannot be read is treated as an instance — "cannot be checked" is not
// "no references".
func memberIsInstance(member map[string]json.RawMessage, wantType string) bool {
	raw, ok := member["type"]
	if !ok {
		return false
	}
	coerced, ok := jsTypeString(raw)
	if !ok {
		return true
	}
	return coerced == wantType
}

// DeleteSubflow removes a subflow definition. Fails with
// *APIError(404) when the id is not present, matching the way
// DeleteFlow behaves for tabs.
//
// A definition that is still in use is not deleted: when any instance
// node of type "subflow:<id>" is left behind, the runtime can no
// longer resolve that type and the affected flows never deploy. The
// call fails with *SubflowInUseError, which lists every reference
// that has to be removed first.
//
// The check is deliberately not a cascading delete. Removing the
// instances along with the definition is a destructive operation the
// operator has to ask for explicitly, not a side effect of deleting
// a definition.
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
	// The definition is present, but it may still be instantiated. The
	// check runs before the backup and the write, so a refused delete
	// leaves the runtime exactly as it was.
	refs, err := c.subflowReferences(ctx, id, existing)
	if err != nil {
		return err
	}
	if len(refs) > 0 {
		return &SubflowInUseError{ID: id, Refs: refs}
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
	// The instance node's type is "subflow:<id>", and that is a node type
	// an operator can name in the denylist. The type is known before the
	// runtime is read, so the policy is consulted here: refusing after
	// the lookup would contact the runtime for a write that was never
	// going to happen.
	if err := c.deniedNodeTypeForType("subflow:" + subflowID); err != nil {
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
	// The node is judged here, before the runtime is read: the caller's
	// params are merged into it, so a forbidden type nested in them
	// would otherwise travel to the runtime before being noticed.
	if err := c.deniedNodeType(built); err != nil {
		return nil, err
	}
	if _, err := c.GetSubflow(ctx, subflowID); err != nil {
		return nil, err
	}
	if err := c.AddNode(ctx, flowID, built); err != nil {
		return nil, err
	}
	return RawFlow(built), nil
}
