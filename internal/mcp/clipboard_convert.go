package mcp

// Conversion between the Node-RED editor clipboard and the documents the
// admin API speaks (issue #337).
//
// The editor clipboard is a flat array: the tab is one element
// ({"type":"tab"}), and each node, config node and subflow definition it
// depends on is another element, linked by id and z. POST /flow takes a
// nested tab document instead ({...tab, nodes:[...], configs:[...]}) and
// refuses any member id the instance already has; it never remaps them.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// clipboardItem is one decoded clipboard element: its raw bytes plus the
// three fields that place it.
type clipboardItem struct {
	raw  json.RawMessage
	id   string
	typ  string
	z    string
	isXY bool
}

// clipboardImport is a clipboard partitioned for POST /flow.
type clipboardImport struct {
	tab      map[string]json.RawMessage
	nodes    []clipboardItem // tab members with canvas coordinates
	configs  []clipboardItem // tab-scoped config nodes
	globals  []clipboardItem // config nodes with no z
	subflows []string        // subflow definitions the clipboard carries
	types    []string        // every node type the clipboard carries
}

// decodeClipboardItems reads the clipboard array into placed items. Every
// element must be an object with an id; a null element is named as such.
func decodeClipboardItems(raw json.RawMessage) ([]clipboardItem, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("clipboard is empty")
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("clipboard is not a JSON array: %w", err)
	}
	if len(arr) == 0 {
		return nil, fmt.Errorf("clipboard array is empty")
	}
	if len(arr) > 1 && allNestedTabs(arr) {
		// Earlier releases refused a multi-tab nested paste by array length.
		return nil, errManyTabs(len(arr))
	}
	if len(arr) == 1 {
		legacy, ok, err := flattenNestedTab(arr[0])
		if err != nil {
			return nil, err
		}
		if ok {
			arr = legacy
		}
	}
	items := make([]clipboardItem, 0, len(arr))
	for i, el := range arr {
		if isJSONNull(bytes.TrimSpace(el)) {
			return nil, fmt.Errorf("clipboard element %d is null; expected a node object", i)
		}
		item, ok := placeItem(el)
		if !ok {
			return nil, fmt.Errorf("clipboard element %d is not a JSON object", i)
		}
		if item.id == "" {
			return nil, fmt.Errorf("clipboard element %d has no id", i)
		}
		items = append(items, item)
	}
	return items, nil
}

// placeItem decodes the fields that place one flat element.
func placeItem(raw json.RawMessage) (clipboardItem, bool) {
	var meta struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Z    string `json:"z"`
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return clipboardItem{}, false
	}
	_ = json.Unmarshal(raw, &meta)
	return clipboardItem{raw: raw, id: meta.ID, typ: meta.Type, z: meta.Z, isXY: hasXY(raw)}, true
}

// flattenNestedTab accepts the nested single-tab shape earlier releases of
// export_flow produced ([{...tab, nodes:[...]}]) and turns it into flat
// elements. ok is false when el is not that shape.
func flattenNestedTab(el json.RawMessage) ([]json.RawMessage, bool, error) {
	doc, ok := nestedTab(el)
	if !ok {
		return nil, false, nil
	}
	var tabID string
	_ = json.Unmarshal(doc["id"], &tabID)
	if tabID == "" {
		tabID = newNodeID(map[string]bool{})
	}
	out := []json.RawMessage{}
	for _, key := range []string{"nodes", "configs"} {
		var members []json.RawMessage
		if v := doc[key]; len(v) > 0 && !isJSONNull(v) {
			if err := json.Unmarshal(v, &members); err != nil {
				return nil, true, fmt.Errorf("clipboard tab: %q is not an array", key)
			}
		}
		for _, m := range members {
			placed, err := withZ(m, tabID)
			if err != nil {
				return nil, true, err
			}
			out = append(out, placed)
		}
		delete(doc, key)
	}
	delete(doc, "subflows")
	doc["id"], _ = json.Marshal(tabID)
	doc["type"] = json.RawMessage(`"tab"`)
	tab, err := json.Marshal(doc)
	if err != nil {
		return nil, true, err
	}
	return append([]json.RawMessage{tab}, out...), true, nil
}

// nestedTab decodes el when it is an object with the nested tab shape.
func nestedTab(el json.RawMessage) (map[string]json.RawMessage, bool) {
	var doc map[string]json.RawMessage
	if json.Unmarshal(el, &doc) != nil || doc == nil {
		return nil, false
	}
	return doc, isNestedTab(doc)
}

// isNestedTab is the nested tab rule: the object has a nodes or configs
// member, or it has no type (a flat element always has one).
func isNestedTab(doc map[string]json.RawMessage) bool {
	_, hasNodes := doc["nodes"]
	_, hasConfigs := doc["configs"]
	_, hasType := doc["type"]
	return hasNodes || hasConfigs || !hasType
}

// allNestedTabs reports whether every element has the nested tab shape.
func allNestedTabs(arr []json.RawMessage) bool {
	for _, el := range arr {
		if _, ok := nestedTab(el); !ok {
			return false
		}
	}
	return true
}

func errManyTabs(n int) error {
	return fmt.Errorf(
		"clipboard has %d tabs; import_flow only accepts a single tab — split the import by hand if you want them on separate tabs",
		n,
	)
}

// withZ sets z on a nested member so it places like a flat element.
func withZ(raw json.RawMessage, z string) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, fmt.Errorf("clipboard tab member is not a JSON object: %s", raw)
	}
	m["z"], _ = json.Marshal(z)
	return json.Marshal(m)
}

// parseClipboard partitions a clipboard around its single tab. It contacts
// nothing, so a malformed clipboard is refused before any request.
func parseClipboard(raw json.RawMessage) (*clipboardImport, error) {
	items, err := decodeClipboardItems(raw)
	if err != nil {
		return nil, err
	}
	doc := &clipboardImport{}
	subflowIDs := map[string]bool{}
	var tabs []clipboardItem
	for _, it := range items {
		doc.types = append(doc.types, it.typ)
		switch it.typ {
		case "tab":
			tabs = append(tabs, it)
		case "subflow":
			subflowIDs[it.id] = true
			doc.subflows = append(doc.subflows, it.id)
		}
	}
	switch {
	case len(tabs) == 0:
		return nil, fmt.Errorf(`clipboard has no tab; import_flow creates one flow tab and needs exactly one element with "type":"tab"`)
	case len(tabs) > 1:
		return nil, errManyTabs(len(tabs))
	}
	tab := tabs[0]
	if err := json.Unmarshal(tab.raw, &doc.tab); err != nil {
		return nil, fmt.Errorf("clipboard tab: %w", err)
	}
	for _, it := range items {
		if err := doc.place(it, tab.id, subflowIDs); err != nil {
			return nil, err
		}
	}
	return doc, nil
}

// place files one non-tab element by its z.
func (doc *clipboardImport) place(it clipboardItem, tabID string, subflowIDs map[string]bool) error {
	switch {
	case it.typ == "tab" || it.typ == "subflow" || subflowIDs[it.z]:
		// The tab itself, or a subflow definition and its members: the
		// definition must already exist on the target (see resolve).
	case it.z == tabID && it.isXY:
		doc.nodes = append(doc.nodes, it)
	case it.z == tabID:
		doc.configs = append(doc.configs, it)
	case it.z == "" && it.isXY:
		// Only config nodes live outside a tab; a canvas node without z
		// would otherwise be filed as one and could be dropped as reused.
		return fmt.Errorf("clipboard element %q (type %q) has canvas coordinates but no z; set its z to the tab id %q", it.id, it.typ, tabID)
	case it.z == "":
		doc.globals = append(doc.globals, it)
	default:
		return fmt.Errorf("clipboard element %q has z %q, which is neither the tab nor a subflow in the clipboard", it.id, it.z)
	}
	return nil
}

// resolve builds the nested POST /flow document against the target's
// current flat configuration and reports how many tab-owned ids collided
// with the target and how many it reminted.
//
// ID policy: subflow definitions the target already has are reused, not
// copied; so is a global config node the target has as a global config
// node of the same type — any other id match is a different object, and the
// config travels with the tab like a missing one. When no tab-owned id exists on the target the
// ids are kept, so a move between instances is lossless. When any does —
// a copy into the same instance — every tab-owned id is reminted and every
// reference to it rewritten, because POST /flow refuses duplicates.
func (doc *clipboardImport) resolve(current []json.RawMessage) (json.RawMessage, remintCount, error) {
	existing := map[string]bool{}
	globals := map[string]string{} // id → type of the target's global config nodes
	subflows := map[string]bool{}
	for _, raw := range current {
		if it, ok := placeItem(raw); ok && it.id != "" {
			existing[it.id] = true
			if it.z == "" && it.typ != "tab" && it.typ != "subflow" {
				globals[it.id] = it.typ
			}
			if it.typ == "subflow" {
				subflows[it.id] = true
			}
		}
	}
	if err := doc.checkSubflows(subflows); err != nil {
		return nil, remintCount{}, err
	}
	configs := doc.configs
	for _, g := range doc.globals {
		if t, ok := globals[g.id]; !ok || t != g.typ {
			// POST /flow scopes every config it creates to the new tab; a
			// colliding id is reminted below.
			configs = append(configs, g)
		}
	}
	owned := append(append([]clipboardItem{}, doc.nodes...), configs...)
	mapping, collided := remintIDs(owned, existing)
	// Config ids the remint replaced, for spotting config references by
	// value in rewriteItems.
	remintedConfigs := map[string]bool{}
	for _, c := range configs {
		remintedConfigs[c.id] = true
	}
	nodes, err := rewriteItems(doc.nodes, mapping, remintedConfigs)
	if err != nil {
		return nil, remintCount{}, err
	}
	cfgs, err := rewriteItems(configs, mapping, remintedConfigs)
	if err != nil {
		return nil, remintCount{}, err
	}
	out := make(map[string]json.RawMessage, len(doc.tab)+2)
	for k, v := range doc.tab {
		if k != "type" {
			out[k] = v
		}
	}
	out["nodes"], _ = json.Marshal(nodes)
	if len(cfgs) > 0 {
		out["configs"], _ = json.Marshal(cfgs)
	}
	encoded, err := json.Marshal(out)
	return encoded, remintCount{collided: collided, reminted: len(mapping)}, err
}

// checkSubflows refuses subflow definitions and instances the target lacks:
// POST /flow cannot create a subflow definition.
func (doc *clipboardImport) checkSubflows(onTarget map[string]bool) error {
	needed := append([]string{}, doc.subflows...)
	for _, n := range doc.nodes {
		if id, ok := strings.CutPrefix(n.typ, "subflow:"); ok {
			needed = append(needed, id)
		}
	}
	for _, id := range needed {
		if !onTarget[id] {
			return fmt.Errorf("clipboard uses subflow %q, which this instance does not have; create it first with create_subflow, then import the tab", id)
		}
	}
	return nil
}

// remintCount is how many tab-owned ids collided with the target and how
// many were reminted as a result (all of them, or none).
type remintCount struct{ collided, reminted int }

// remintIDs returns old → new ids for every owned item when any owned id
// already exists on the target, and nil otherwise, plus the colliding count.
func remintIDs(owned []clipboardItem, existing map[string]bool) (map[string]string, int) {
	collided := 0
	taken := map[string]bool{}
	for id := range existing {
		taken[id] = true
	}
	for _, it := range owned {
		if existing[it.id] {
			collided++
		}
		taken[it.id] = true
	}
	if collided == 0 {
		return nil, 0
	}
	mapping := make(map[string]string, len(owned))
	for _, it := range owned {
		mapping[it.id] = newNodeID(taken)
	}
	return mapping, collided
}

// rewriteItems returns the items' raw bytes with every reference in
// mapping rewritten; with no mapping the bytes are returned untouched.
//
// References are the structural fields — id, wires, links, scope, g and
// a group's nodes — plus config-node properties such as an mqtt node's
// broker. The editor tells a config property from free text via node
// definitions, which the runtime API does not expose here; without them
// an ordinary property counts as a reference only when its value is the
// id of a reminted config node (remintedConfigs). Canvas-node ids never
// appear in a property of stock nodes, so free text naming one — name,
// payload, function bodies — survives the remint (issue #371); free text
// that spells out a reminted config id verbatim is the one false match
// left possible.
func rewriteItems(items []clipboardItem, mapping map[string]string, remintedConfigs map[string]bool) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(items))
	for _, it := range items {
		if len(mapping) == 0 {
			out = append(out, it.raw)
			continue
		}
		v, err := decodeAny(it.raw)
		if err != nil {
			return nil, err
		}
		obj, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("clipboard element %q is not a JSON object", it.id)
		}
		for k, val := range obj {
			switch k {
			case "id", "g":
				if s, ok := val.(string); ok {
					obj[k] = remap(mapping, s)
				}
			case "wires", "links", "scope", "nodes":
				obj[k] = walkStrings(val, func(s string) string { return remap(mapping, s) })
			default:
				if s, ok := val.(string); ok && remintedConfigs[s] {
					obj[k] = mapping[s]
				}
			}
		}
		b, err := json.Marshal(obj)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// remap returns the reminted id for s, or s itself when it is not one.
func remap(mapping map[string]string, s string) string {
	if n, ok := mapping[s]; ok {
		return n
	}
	return s
}

// exportClipboard selects the editor clipboard for one tab out of the flat
// configuration: the tab, its members, and — transitively — the global
// config nodes and subflow definitions (with their members) they refer to,
// in configuration order with the tab first.
func exportClipboard(current []json.RawMessage, tabID string) ([]json.RawMessage, error) {
	idx := indexForExport(current, tabID)
	if idx.tab == nil {
		return nil, fmt.Errorf("flow tab %q not found", tabID)
	}
	include := idx.closure(tabID)
	out := []json.RawMessage{idx.tab}
	for _, raw := range current {
		if it, ok := placeItem(raw); ok && include[it.id] && it.id != tabID {
			out = append(out, raw)
		}
	}
	return out, nil
}

// exportIndex groups a flat configuration around the exported tab.
type exportIndex struct {
	tab     json.RawMessage
	owned   []clipboardItem            // the tab's members
	deps    map[string]clipboardItem   // global config nodes and subflow definitions
	members map[string][]clipboardItem // items by their z, for subflow members
}

func indexForExport(current []json.RawMessage, tabID string) exportIndex {
	idx := exportIndex{deps: map[string]clipboardItem{}, members: map[string][]clipboardItem{}}
	for _, raw := range current {
		it, ok := placeItem(raw)
		switch {
		case !ok:
		case it.typ == "tab" && it.id == tabID:
			idx.tab = raw
		case it.z == tabID:
			idx.owned = append(idx.owned, it)
		case it.typ == "subflow" || (it.z == "" && it.typ != "tab"):
			idx.deps[it.id] = it
		default:
			idx.members[it.z] = append(idx.members[it.z], it)
		}
	}
	return idx
}

// closure returns the ids to export: the tab, its members, and every
// dependency reachable from them; a subflow brings its members along.
func (idx exportIndex) closure(tabID string) map[string]bool {
	include := map[string]bool{tabID: true}
	queue := append([]clipboardItem{}, idx.owned...)
	for _, it := range queue {
		include[it.id] = true
	}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		for _, ref := range itemRefs(it) {
			dep, ok := idx.deps[ref]
			if !ok || include[ref] {
				continue
			}
			include[ref] = true
			queue = append(queue, dep)
			for _, m := range idx.members[ref] {
				include[m.id] = true
				queue = append(queue, m)
			}
		}
	}
	return include
}

// itemRefs lists every string value an item carries, plus the subflow a
// "subflow:<id>" instance refers to.
func itemRefs(it clipboardItem) []string {
	var refs []string
	if id, ok := strings.CutPrefix(it.typ, "subflow:"); ok {
		refs = append(refs, id)
	}
	v, err := decodeAny(it.raw)
	if err != nil {
		return refs
	}
	walkStrings(v, func(s string) string {
		refs = append(refs, s)
		return s
	})
	return refs
}

// decodeAny decodes JSON keeping numbers exact.
func decodeAny(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	err := dec.Decode(&v)
	return v, err
}

// walkStrings replaces every string value in v with fn(value), in place.
func walkStrings(v any, fn func(string) string) any {
	switch x := v.(type) {
	case string:
		return fn(x)
	case map[string]any:
		for k, e := range x {
			x[k] = walkStrings(e, fn)
		}
	case []any:
		for i, e := range x {
			x[i] = walkStrings(e, fn)
		}
	}
	return v
}

// newNodeID mints a Node-RED style id (16 hex characters) not in taken,
// and records it there.
func newNodeID(taken map[string]bool) string {
	for {
		var b [8]byte
		_, _ = rand.Read(b[:]) // crypto/rand.Read never fails since Go 1.24
		id := hex.EncodeToString(b[:])
		if !taken[id] {
			taken[id] = true
			return id
		}
	}
}
