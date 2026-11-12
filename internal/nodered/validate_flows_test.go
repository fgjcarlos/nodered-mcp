package nodered

import (
	"errors"
	"testing"
)

// Regression tests for issue #336: ValidateFlows rebuilt one document per
// tab out of a flat array and validated each in isolation, so an entry
// outside every tab, an id shared across tabs and a node without a type
// all came back as "0 issues".

// findIssue returns the first issue of the given kind about the given node.
func findIssue(issues []FlowIssue, kind FlowIssueKind, nodeID string) (FlowIssue, bool) {
	for _, issue := range issues {
		if issue.Kind == kind && issue.NodeID == nodeID {
			return issue, true
		}
	}
	return FlowIssue{}, false
}

func countKind(issues []FlowIssue, kind FlowIssueKind) int {
	n := 0
	for _, issue := range issues {
		if issue.Kind == kind {
			n++
		}
	}
	return n
}

// A node with an id and coordinates but no type: the runtime files it
// under a missing node type and refuses to start the flows.
func TestValidateFlows_MissingTypeIsReported(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"n1","z":"t1","x":140,"y":140,"wires":[]}
	]`
	issues := ValidateFlows(RawFlow(cfg))
	if len(issues) != 1 {
		t.Fatalf("expected exactly 1 issue, got %d: %+v", len(issues), issues)
	}
	if _, ok := findIssue(issues, IssueMissingType, "n1"); !ok {
		t.Fatalf("expected %s on n1, got %+v", IssueMissingType, issues)
	}
}

// The same id in two different tabs: uniqueness is a property of the
// whole deployment, not of one tab.
func TestValidateFlows_DuplicateIDAcrossTabsIsReported(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"t2","type":"tab","label":"Other"},
	  {"id":"n1","type":"inject","z":"t1","x":140,"y":140,"wires":[]},
	  {"id":"n1","type":"debug","z":"t2","x":300,"y":140,"wires":[]}
	]`
	issues := ValidateFlows(RawFlow(cfg))
	if len(issues) != 1 {
		t.Fatalf("expected exactly 1 issue, got %d: %+v", len(issues), issues)
	}
	if _, ok := findIssue(issues, IssueDuplicateID, "n1"); !ok {
		t.Fatalf("expected %s on n1, got %+v", IssueDuplicateID, issues)
	}
}

// An orphan node whose z names nothing, wired to a target that does not
// exist: it sits outside every tab the per-tab walk visited.
func TestValidateFlows_OrphanNodeAndItsDanglingWireAreReported(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"n1","type":"inject","z":"nowhere","x":140,"y":140,"wires":[["ghost"]]}
	]`
	issues := ValidateFlows(RawFlow(cfg))
	if len(issues) != 2 {
		t.Fatalf("expected exactly 2 issues, got %d: %+v", len(issues), issues)
	}
	orphan, ok := findIssue(issues, IssueOrphanNode, "n1")
	if !ok || orphan.Target != "nowhere" {
		t.Fatalf("expected %s on n1 naming z=nowhere, got %+v", IssueOrphanNode, issues)
	}
	wire, ok := findIssue(issues, IssueDanglingWire, "n1")
	if !ok || wire.Target != "ghost" {
		t.Fatalf("expected %s from n1 to ghost, got %+v", IssueDanglingWire, issues)
	}
}

// A group whose z names nothing has no owner either.
func TestValidateFlows_OrphanGroupIsReported(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"g1","type":"group","z":"nowhere","nodes":[],"x":10,"y":10,"w":100,"h":100}
	]`
	issues := ValidateFlows(RawFlow(cfg))
	if len(issues) != 1 {
		t.Fatalf("expected exactly 1 issue, got %d: %+v", len(issues), issues)
	}
	if _, ok := findIssue(issues, IssueOrphanNode, "g1"); !ok {
		t.Fatalf("expected %s on g1, got %+v", IssueOrphanNode, issues)
	}
}

// The nodes inside a subflow definition are validated too: the per-tab
// walk never visited them.
func TestValidateFlows_SubflowMembersAreValidated(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"sf1","type":"subflow","name":"S","in":[],"out":[]},
	  {"id":"i1","type":"subflow:sf1","z":"t1","x":100,"y":100,"wires":[]},
	  {"id":"n1","type":"function","z":"sf1","x":140,"y":140,"wires":[["ghost"]]}
	]`
	issues := ValidateFlows(RawFlow(cfg))
	if len(issues) != 1 {
		t.Fatalf("expected exactly 1 issue, got %d: %+v", len(issues), issues)
	}
	if wire, ok := findIssue(issues, IssueDanglingWire, "n1"); !ok || wire.Target != "ghost" {
		t.Fatalf("expected %s from n1 to ghost, got %+v", IssueDanglingWire, issues)
	}
}

// validate_flow is a local dry-run: it cannot see the runtime, so an
// instance whose definition is not in the payload is not reported — the
// definition may well be deployed already. This is what it did before.
func TestValidateFlows_DoesNotReportMissingSubflowDefinition(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"i1","type":"subflow:deployed","z":"t1","x":100,"y":100,"wires":[]}
	]`
	issues := ValidateFlows(RawFlow(cfg))
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues, got %+v", issues)
	}
}

// A healthy flat configuration, with a global config node and a valid
// subflow, stays clean.
func TestValidateFlows_ValidFlatConfigHasNoIssues(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"t2","type":"tab","label":"Other"},
	  {"id":"b1","type":"mqtt-broker","broker":"localhost"},
	  {"id":"sf1","type":"subflow","name":"S","in":[],"out":[]},
	  {"id":"s1","type":"function","z":"sf1","x":140,"y":140,"wires":[]},
	  {"id":"g1","type":"group","z":"t1","nodes":["n1"],"x":10,"y":10,"w":300,"h":100},
	  {"id":"n1","type":"inject","z":"t1","g":"g1","x":140,"y":140,"wires":[["n2"]]},
	  {"id":"n2","type":"debug","z":"t1","x":300,"y":140,"wires":[]},
	  {"id":"i1","type":"subflow:sf1","z":"t2","x":100,"y":100,"wires":[]}
	]`
	issues := ValidateFlows(RawFlow(cfg))
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues, got %+v", issues)
	}
}

// A flat array without any tab is still not a flow document.
func TestValidateFlows_FlatArrayWithoutTabIsInvalidDocument(t *testing.T) {
	const cfg = `[{"id":"n1","type":"inject","x":1,"y":1}]`
	issues := ValidateFlows(RawFlow(cfg))
	if len(issues) != 1 || issues[0].Kind != "invalid_document" {
		t.Fatalf("expected one invalid_document issue, got %+v", issues)
	}
}

// The nested single-tab shape reports a missing type as well.
func TestValidateFlow_MissingTypeIsReported(t *testing.T) {
	const flow = `{"id":"t1","label":"Home","nodes":[
	  {"id":"n1","z":"t1","x":140,"y":140,"wires":[]},
	  {"id":"n2","type":null,"z":"t1","x":240,"y":140,"wires":[]},
	  {"id":"n3","type":"","z":"t1","x":340,"y":140,"wires":[]}
	],"configs":[{"id":"c1","z":"t1"}]}`
	issues := ValidateFlow(RawFlow(flow))
	for _, id := range []string{"n1", "n2", "n3", "c1"} {
		if _, ok := findIssue(issues, IssueMissingType, id); !ok {
			t.Errorf("expected %s on %s, got %+v", IssueMissingType, id, issues)
		}
	}
	if len(issues) != 4 {
		t.Fatalf("expected exactly 4 issues, got %d: %+v", len(issues), issues)
	}
}

// A type the runtime coerces into a name with String() resolves like the
// string, so it is not a missing type.
func TestValidateFlow_CoercibleTypeIsNotMissing(t *testing.T) {
	const flow = `{"id":"t1","label":"Home","nodes":[
	  {"id":"n1","type":["inject"],"z":"t1","x":140,"y":140,"wires":[]}
	]}`
	if issues := ValidateFlow(RawFlow(flow)); len(issues) != 0 {
		t.Fatalf("expected 0 issues, got %+v", issues)
	}
}

// The write guard refuses what validate_flow reports: a typeless node
// would stop every flow of the runtime.
func TestValidateFlowWires_RefusesMissingType(t *testing.T) {
	const flow = `{"id":"t1","label":"Home","nodes":[
	  {"id":"n1","z":"t1","x":140,"y":140,"wires":[]}
	]}`
	err := validateFlowWires(RawFlow(flow))
	var guard *FlowGuardError
	if !errors.As(err, &guard) {
		t.Fatalf("expected a FlowGuardError, got %v", err)
	}
}

// The deployment check reports a missing type once, not once from the
// whole-configuration pass and again from the per-owner pass.
func TestValidateEffectiveConfig_MissingTypeReportedOnce(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"n1","z":"t1","x":140,"y":140,"wires":[]},
	  {"id":"c1"}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if got := countKind(issues, IssueMissingType); got != 2 || len(issues) != 2 {
		t.Fatalf("expected exactly 2 %s issues (n1, c1), got %+v", IssueMissingType, issues)
	}
}

// The deployment check (set_flows, backup restore) refuses a group whose
// z names no tab and no subflow definition.
func TestValidateEffectiveConfig_OrphanGroup(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"g1","type":"group","z":"nowhere","nodes":[],"x":10,"y":10,"w":100,"h":100}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 1 {
		t.Fatalf("expected exactly 1 issue, got %d: %+v", len(issues), issues)
	}
	if orphan, ok := findIssue(issues, IssueOrphanNode, "g1"); !ok || orphan.Target != "nowhere" {
		t.Fatalf("expected %s on g1 naming z=nowhere, got %+v", IssueOrphanNode, issues)
	}
	if err := validateEffectiveConfig(mustFlowArray(t, cfg)); err == nil {
		t.Fatal("expected the deployment to be refused")
	}
}

// The deployment check reports the dangling wire of an orphan node, which
// no owner rule visits.
func TestValidateEffectiveConfig_OrphanDanglingWire(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"n1","type":"inject","z":"nowhere","x":140,"y":140,"wires":[["ghost"]]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 2 {
		t.Fatalf("expected exactly 2 issues, got %d: %+v", len(issues), issues)
	}
	if _, ok := findIssue(issues, IssueOrphanNode, "n1"); !ok {
		t.Fatalf("expected %s on n1, got %+v", IssueOrphanNode, issues)
	}
	if wire, ok := findIssue(issues, IssueDanglingWire, "n1"); !ok || wire.Target != "ghost" {
		t.Fatalf("expected %s from n1 to ghost, got %+v", IssueDanglingWire, issues)
	}
}

// A type the runtime coerces into a name is not a missing type on the
// deployment path either.
func TestValidateEffectiveConfig_CoercibleTypeIsNotMissing(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"n1","type":["inject"],"z":"t1","x":140,"y":140,"wires":[]}
	]`
	if issues := ValidateEffectiveConfig(RawFlow(cfg)); len(issues) != 0 {
		t.Fatalf("expected 0 issues, got %+v", issues)
	}
}
