package nodered

import "testing"

const diffBefore = `[
  {"id":"tabA","type":"tab","label":"Home"},
  {"id":"n1","type":"inject","z":"tabA","repeat":"5","wires":[["n2"]]},
  {"id":"n2","type":"function","z":"tabA","func":"return msg;","wires":[]},
  {"id":"n3","type":"debug","z":"tabA","wires":[]}
]`

func TestDiffDetectsAddedRemovedAndChanged(t *testing.T) {
	// n1's repeat changes, n3 disappears, n4 appears.
	after := `[
      {"id":"tabA","type":"tab","label":"Home"},
      {"id":"n1","type":"inject","z":"tabA","repeat":"30","wires":[["n2"]]},
      {"id":"n2","type":"function","z":"tabA","func":"return msg;","wires":[]},
      {"id":"n4","type":"mqtt out","z":"tabA","topic":"home/out","wires":[]}
    ]`

	d := DiffFlows(RawFlow(diffBefore), RawFlow(after))

	if len(d.Added) != 1 || d.Added[0].ID != "n4" {
		t.Errorf("expected n4 added, got %+v", d.Added)
	}
	if len(d.Removed) != 1 || d.Removed[0].ID != "n3" {
		t.Errorf("expected n3 removed, got %+v", d.Removed)
	}
	if len(d.Changed) != 1 || d.Changed[0].ID != "n1" {
		t.Errorf("expected n1 changed, got %+v", d.Changed)
	}
	// The owning tab is what makes a diff actionable.
	if d.Added[0].Flow != "tabA" || d.Added[0].Type != "mqtt out" {
		t.Errorf("added entry lacks context: %+v", d.Added[0])
	}
}

func TestDiffOfIdenticalConfigsIsEmpty(t *testing.T) {
	d := DiffFlows(RawFlow(diffBefore), RawFlow(diffBefore))
	if !d.Empty() {
		t.Errorf("identical configs should diff to nothing, got %+v", d)
	}
}

// Key order is not semantic in JSON, and a re-encoded flow routinely comes back
// with its keys in a different order. Reporting that as a change would make
// every diff noise.
func TestDiffIgnoresKeyOrder(t *testing.T) {
	reordered := `[
      {"type":"tab","label":"Home","id":"tabA"},
      {"wires":[["n2"]],"repeat":"5","z":"tabA","type":"inject","id":"n1"},
      {"id":"n2","func":"return msg;","type":"function","z":"tabA","wires":[]},
      {"id":"n3","type":"debug","z":"tabA","wires":[]}
    ]`
	if d := DiffFlows(RawFlow(diffBefore), RawFlow(reordered)); !d.Empty() {
		t.Errorf("key reordering should not register as a change, got %+v", d)
	}
}

func TestDiffHandlesTheEnvelopeShape(t *testing.T) {
	env := RawFlow(`{"rev":"abc","flows":` + diffBefore + `}`)
	if d := DiffFlows(env, RawFlow(diffBefore)); !d.Empty() {
		t.Errorf("the {rev,flows} envelope should be unwrapped before comparing, got %+v", d)
	}
}

func TestDiffReportsTabsToo(t *testing.T) {
	after := `[
      {"id":"tabA","type":"tab","label":"Renamed"},
      {"id":"n1","type":"inject","z":"tabA","repeat":"5","wires":[["n2"]]},
      {"id":"n2","type":"function","z":"tabA","func":"return msg;","wires":[]},
      {"id":"n3","type":"debug","z":"tabA","wires":[]}
    ]`
	d := DiffFlows(RawFlow(diffBefore), RawFlow(after))
	if len(d.Changed) != 1 || d.Changed[0].ID != "tabA" {
		t.Fatalf("expected the renamed tab to be reported, got %+v", d.Changed)
	}
	if d.Changed[0].Label != "Renamed" {
		t.Errorf("expected the new label, got %q", d.Changed[0].Label)
	}
}

func TestDiffOnGarbage(t *testing.T) {
	// Unparseable input must not panic or invent changes.
	if d := DiffFlows(RawFlow(`not json`), RawFlow(`also not json`)); !d.Empty() {
		t.Errorf("garbage should diff to nothing, got %+v", d)
	}
}

func TestDiffAgainstAnEmptyConfig(t *testing.T) {
	d := DiffFlows(RawFlow(`[]`), RawFlow(diffBefore))
	if len(d.Added) != 4 {
		t.Errorf("expected all 4 objects added, got %d", len(d.Added))
	}
	if len(d.Removed) != 0 || len(d.Changed) != 0 {
		t.Errorf("nothing should be removed or changed, got %+v", d)
	}
}
