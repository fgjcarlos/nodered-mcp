package nodered

import (
	"encoding/json"
	"reflect"
)

// Comparing two flow configurations, so a change can be reviewed before it is
// kept — or understood after something broke. Backups are already taken before
// every write, which means the interesting comparison is usually a snapshot
// against the live configuration.

// FlowChange identifies one object that differs between two configurations,
// with enough context to act on it without fetching anything else.
type FlowChange struct {
	ID   string `json:"id"`
	Type string `json:"type,omitempty"`
	// Flow is the owning tab, empty for tabs and config nodes.
	Flow string `json:"flow,omitempty"`
	// Label is carried for tabs, whose name is the only way to recognise them.
	Label string `json:"label,omitempty"`
}

// FlowsDiff is the result of comparing two flow configurations.
type FlowsDiff struct {
	Added   []FlowChange `json:"added,omitempty"`
	Removed []FlowChange `json:"removed,omitempty"`
	Changed []FlowChange `json:"changed,omitempty"`
}

// Empty reports whether the two configurations are equivalent.
func (d FlowsDiff) Empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// Total counts every difference found.
func (d FlowsDiff) Total() int {
	return len(d.Added) + len(d.Removed) + len(d.Changed)
}

// indexByID maps every object in a flow configuration by its id, preserving
// document order in the returned slice of ids.
func indexByID(raw RawFlow) (map[string]json.RawMessage, []string) {
	byID := make(map[string]json.RawMessage)
	var order []string
	for _, item := range extractFlowArray(raw) {
		var meta nodeMeta
		if json.Unmarshal(item, &meta) != nil || meta.ID == "" {
			continue
		}
		if _, seen := byID[meta.ID]; !seen {
			order = append(order, meta.ID)
		}
		byID[meta.ID] = item
	}
	return byID, order
}

// describe builds the human-facing summary of one object.
func describe(item json.RawMessage) FlowChange {
	var meta nodeMeta
	_ = json.Unmarshal(item, &meta)
	label := meta.Label
	if label == "" {
		label = meta.Name
	}
	c := FlowChange{ID: meta.ID, Type: meta.Type, Flow: meta.Z}
	// Only carry a label where it identifies the object; for ordinary nodes the
	// type and owning tab are more useful and the name is often empty anyway.
	if meta.Type == "tab" || meta.Type == "subflow" {
		c.Label = label
	}
	return c
}

// DiffFlows compares two flow configurations object by object, matching on id.
//
// Both the bare-array and {"rev":..,"flows":[..]} shapes are accepted, and
// comparison is semantic rather than textual: a document whose keys were
// reordered by a round trip is not a change. Anything unparseable yields an
// empty diff rather than a false alarm.
func DiffFlows(before, after RawFlow) FlowsDiff {
	oldByID, oldOrder := indexByID(before)
	newByID, newOrder := indexByID(after)

	var diff FlowsDiff

	// Walk the old document in order so removals read in a familiar sequence.
	for _, id := range oldOrder {
		newItem, stillThere := newByID[id]
		if !stillThere {
			diff.Removed = append(diff.Removed, describe(oldByID[id]))
			continue
		}
		if !sameJSON(oldByID[id], newItem) {
			// Describe the new version: that is the state to reason about.
			diff.Changed = append(diff.Changed, describe(newItem))
		}
	}
	for _, id := range newOrder {
		if _, existed := oldByID[id]; !existed {
			diff.Added = append(diff.Added, describe(newByID[id]))
		}
	}
	return diff
}

// sameJSON compares two JSON documents by value, so key order and insignificant
// whitespace do not count as differences.
func sameJSON(a, b json.RawMessage) bool {
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}
