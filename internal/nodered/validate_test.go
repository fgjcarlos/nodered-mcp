package nodered

import (
	"encoding/json"
	"strings"
	"testing"
)

// sampleTab comes from edit_test.go via package-level const; it is a clean
// tab the validator should accept unchanged.
func TestValidateFlow_CleanTab(t *testing.T) {
	issues := ValidateFlow(RawFlow(sampleTab))
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues on a clean tab, got %d: %+v", len(issues), issues)
	}
}

func TestValidateFlow_DanglingWire(t *testing.T) {
	bad := `{
	  "id":"tabA","label":"Home",
	  "nodes":[
	    {"id":"n1","type":"inject","z":"tabA","x":100,"y":80,"wires":[["n2","ghost"]]},
	    {"id":"n2","type":"function","z":"tabA","x":260,"y":80,"wires":[]}
	  ]
	}`
	issues := ValidateFlow(RawFlow(bad))

	want := map[string]bool{"ghost": false}
	for _, issue := range issues {
		if issue.Kind == IssueDanglingWire && issue.Target == "ghost" {
			want["ghost"] = true
		}
	}
	for tgt, found := range want {
		if !found {
			t.Errorf("expected a dangling-wire issue targeting %q, got issues: %+v", tgt, issues)
		}
	}
}

func TestValidateFlow_DuplicateID(t *testing.T) {
	bad := `{
	  "id":"tabA","label":"Home",
	  "nodes":[
	    {"id":"n1","type":"inject","z":"tabA","x":100,"y":80,"wires":[]},
	    {"id":"n1","type":"debug","z":"tabA","x":260,"y":80,"wires":[]}
	  ]
	}`
	issues := ValidateFlow(RawFlow(bad))

	found := false
	for _, issue := range issues {
		if issue.Kind == IssueDuplicateID && issue.NodeID == "n1" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a duplicate-id issue for n1, got: %+v", issues)
	}
}

func TestValidateFlow_MissingXY(t *testing.T) {
	bad := `{
	  "id":"tabA","label":"Home",
	  "nodes":[
	    {"id":"n1","type":"debug","z":"tabA","wires":[]}
	  ]
	}`
	issues := ValidateFlow(RawFlow(bad))

	found := false
	for _, issue := range issues {
		if issue.Kind == IssueMissingXY && issue.NodeID == "n1" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a missing-xy issue for n1, got: %+v", issues)
	}
}

func TestValidateFlow_MissingID(t *testing.T) {
	bad := `{
	  "id":"tabA","label":"Home",
	  "nodes":[
	    {"type":"debug","z":"tabA","x":100,"y":80,"wires":[]}
	  ]
	}`
	issues := ValidateFlow(RawFlow(bad))

	found := false
	for _, issue := range issues {
		if issue.Kind == IssueMissingID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a missing-id issue, got: %+v", issues)
	}
}

// Issue kinds stay stable across changes: a model that learns to read
// "dangling_wire" must not have to relearn it after a refactor.
func TestValidateFlow_IssueKindsAreStable(t *testing.T) {
	bad := `{
	  "id":"tabA","label":"Home",
	  "nodes":[
	    {"type":"debug","z":"tabA","x":100,"y":80,"wires":[]},
	    {"id":"x","type":"debug","z":"tabA","x":1,"y":1,"wires":[["ghost"]]},
	    {"id":"x","type":"debug","z":"tabA","x":2,"y":2,"wires":[]}
	  ]
	}`
	issues := ValidateFlow(RawFlow(bad))

	want := map[FlowIssueKind]bool{
		IssueMissingID:    false,
		IssueDanglingWire: false,
		IssueDuplicateID:  false,
	}
	for _, issue := range issues {
		if _, ok := want[issue.Kind]; ok {
			want[issue.Kind] = true
		}
	}
	for kind, found := range want {
		if !found {
			t.Errorf("expected at least one %q issue, got: %+v", kind, issues)
		}
	}
}

func TestValidateFlow_MalformedDocument(t *testing.T) {
	issues := ValidateFlow(RawFlow(`not json at all`))
	if len(issues) != 1 || issues[0].Kind != "invalid_document" {
		t.Errorf("expected one invalid_document issue, got: %+v", issues)
	}
}

// TestValidateFlow_FlagsStringTypedWires is the headline regression for
// issue #415: a node whose "wires" field is a JSON string instead of an
// array of arrays used to fall through a silent `continue` in the wire
// loop, leaving the corruption to be written to the runtime and breaking
// every later edit. The validator must surface an IssueInvalidWires
// entry that names the offending node and the JSON shape it actually
// saw, so the operator can fix it without re-parsing the document.
func TestValidateFlow_FlagsStringTypedWires(t *testing.T) {
	bad := `{
	  "id":"tabA","label":"Home",
	  "nodes":[
	    {"id":"n1","type":"inject","z":"tabA","x":100,"y":80,"wires":"not-an-array"}
	  ]
	}`
	issues := ValidateFlow(RawFlow(bad))

	found := false
	for _, issue := range issues {
		if issue.Kind != IssueInvalidWires {
			continue
		}
		if issue.NodeID != "n1" {
			t.Errorf("expected invalid-wires issue to point at n1, got %q (issues: %+v)", issue.NodeID, issues)
		}
		if !strings.Contains(issue.Message, "string") {
			t.Errorf("expected message to mention the JSON type encountered (string), got %q", issue.Message)
		}
		found = true
	}
	if !found {
		t.Errorf("expected an invalid-wires issue for n1, got: %+v", issues)
	}
}

// TestValidateFlow_FlagsOtherWrongShapes keeps the other branches of
// jsonTypeOf honest: a number/object/boolean in the wires slot is the
// same kind of corruption and must be reported the same way.
func TestValidateFlow_FlagsOtherWrongShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"number", `{"id":"t","nodes":[{"id":"n1","type":"t","x":1,"y":1,"wires":42}]}`},
		{"object", `{"id":"t","nodes":[{"id":"n1","type":"t","x":1,"y":1,"wires":{"a":"b"}}]}`},
		{"boolean", `{"id":"t","nodes":[{"id":"n1","type":"t","x":1,"y":1,"wires":true}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := ValidateFlow(RawFlow(tc.body))
			found := false
			for _, issue := range issues {
				if issue.Kind == IssueInvalidWires && issue.NodeID == "n1" {
					found = true
				}
			}
			if !found {
				t.Errorf("expected an invalid-wires issue for n1, got: %+v", issues)
			}
		})
	}
}

// TestValidateFlow_AcceptsProperlyShapedWires is the regression pin: a
// clean tab with proper wire arrays must continue to produce no issues,
// otherwise the new check is over-eager and rejects real flows.
func TestValidateFlow_AcceptsProperlyShapedWires(t *testing.T) {
	good := `{
	  "id":"tabA","label":"Home",
	  "nodes":[
	    {"id":"n1","type":"inject","z":"tabA","x":100,"y":80,"wires":[["n2"]]},
	    {"id":"n2","type":"function","z":"tabA","x":260,"y":80,"func":"return msg;","outputs":1,"wires":[["n3"]]},
	    {"id":"n3","type":"debug","z":"tabA","x":420,"y":80,"wires":[]}
	  ],
	  "configs":[
	    {"id":"b1","type":"mqtt-broker","z":"tabA","name":"local","broker":"localhost","port":"1883"}
	  ]
	}`
	issues := ValidateFlow(RawFlow(good))
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues on properly-shaped wires, got %d: %+v", len(issues), issues)
	}
}

// TestValidateFlow_NullWiresIsNotAnIssue guards against the new check
// treating a legitimate "no wires" (the value is JSON null) as a
// rejection. Nodes opt out of wiring by writing wires:null; that is a
// valid shape and must not be flagged.
func TestValidateFlow_NullWiresIsNotAnIssue(t *testing.T) {
	good := `{
	  "id":"tabA","label":"Home",
	  "nodes":[
	    {"id":"n1","type":"inject","z":"tabA","x":100,"y":80,"wires":null}
	  ]
	}`
	issues := ValidateFlow(RawFlow(good))
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues when wires is JSON null, got %d: %+v", len(issues), issues)
	}
}

// The write-path guard now delegates to ValidateFlow. The error string
// callers have always relied on ("node X wires to unknown node Y") must
// survive the refactor.
func TestValidateFlowWires_WritePathStillWorks(t *testing.T) {
	bad := `{
	  "id":"tabA","label":"Home",
	  "nodes":[
	    {"id":"n1","type":"inject","z":"tabA","x":100,"y":80,"wires":[["ghost"]]}
	  ]
	}`
	err := validateFlowWires(RawFlow(bad))
	if err == nil {
		t.Fatal("expected validateFlowWires to refuse a dangling wire, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"n1"`) || !strings.Contains(msg, `"ghost"`) {
		t.Errorf("expected error to name both endpoints, got: %v", err)
	}
}

// Empty issues slice is non-nil so json.Marshal produces "issues":[] and
// not "issues":null. Small detail, but models keying off the field
// notice.
func TestValidateFlow_EmptyIsNonNil(t *testing.T) {
	issues := ValidateFlow(RawFlow(sampleTab))
	if issues == nil {
		t.Fatal("expected non-nil empty slice")
	}
	out, err := json.Marshal(issues)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != "[]" {
		t.Errorf("expected `[]`, got %q", string(out))
	}
}

// SetDisabledInFlow round-trips: a tab read from the runtime, flipped to
// disabled, encoded back. The only field that changed is "disabled".
func TestSetDisabledInFlow_FlipsAndRoundtrips(t *testing.T) {
	got, err := SetDisabledInFlow(RawFlow(sampleTab), true)
	if err != nil {
		t.Fatalf("SetDisabledInFlow(true): %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if doc["disabled"] != true {
		t.Errorf("expected disabled=true, got %v", doc["disabled"])
	}
	// sampleTab's other top-level fields survive the round-trip.
	for _, key := range []string{"id", "label", "nodes", "configs"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("expected %q to survive the round-trip, got: %v", key, doc)
		}
	}

	// Re-enable: disabled becomes false.
	got, err = SetDisabledInFlow(RawFlow(sampleTab), false)
	if err != nil {
		t.Fatalf("SetDisabledInFlow(false): %v", err)
	}
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if doc["disabled"] != false {
		t.Errorf("expected disabled=false, got %v", doc["disabled"])
	}
}

// SetDisabledInFlow on a malformed document returns an error rather than
// silently producing a half-tab.
func TestSetDisabledInFlow_MalformedDocument(t *testing.T) {
	if _, err := SetDisabledInFlow(RawFlow(`not json`), true); err == nil {
		t.Fatal("expected an error on malformed input, got nil")
	}
}
