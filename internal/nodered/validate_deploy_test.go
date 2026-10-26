package nodered

import (
	"encoding/json"
	"strings"
	"testing"
)

// The effective configuration is the document a full deployment would
// install. The rules below are the ones a full deployment can break,
// and each of them is a reference that does not resolve.
//
// The shapes in these tests are the ones the runtime itself produces
// (verified against node-red 5.0.8): a flat array of tabs, nodes,
// subflow definitions and the nodes inside them.

// A configuration whose references all resolve is safe to deploy.
func TestValidateEffectiveConfig_ValidConfigurationHasNoIssues(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"n1","type":"inject","z":"t1","x":140,"y":140,"wires":[["n2"]]},
	  {"id":"n2","type":"debug","z":"t1","x":300,"y":140,"wires":[]},
	  {"id":"cfg1","type":"mqtt-broker","z":"t1","name":"broker"}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %d: %+v", len(issues), issues)
	}
}

// A wire that names a node which is not in the same tab never
// delivers: the runtime resolves the target inside the owner.
func TestValidateEffectiveConfig_DanglingWire(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"n1","type":"inject","z":"t1","x":140,"y":140,"wires":[["ghost"]]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 1 {
		t.Fatalf("expected exactly 1 issue, got %d: %+v", len(issues), issues)
	}
	if issues[0].Kind != IssueDanglingWire {
		t.Errorf("expected %s, got %s", IssueDanglingWire, issues[0].Kind)
	}
	if issues[0].NodeID != "n1" || issues[0].Target != "ghost" {
		t.Errorf("expected the issue to name both endpoints, got %+v", issues[0])
	}
}

// A wire across two tabs is not a thing in Node-RED: the message is
// dropped, so the reference does not resolve either.
func TestValidateEffectiveConfig_CrossTabWireIsDangling(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"One"},
	  {"id":"t2","type":"tab","label":"Two"},
	  {"id":"n1","type":"inject","z":"t1","x":140,"y":140,"wires":[["n2"]]},
	  {"id":"n2","type":"debug","z":"t2","x":300,"y":140,"wires":[]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 1 || issues[0].Kind != IssueDanglingWire {
		t.Fatalf("expected one dangling wire, got %+v", issues)
	}
}

// A node whose z names no tab and no subflow lands in a tab the
// runtime invents on the spot.
func TestValidateEffectiveConfig_OrphanNode(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"n1","type":"inject","z":"nowhere","x":140,"y":140,"wires":[]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 1 {
		t.Fatalf("expected exactly 1 issue, got %d: %+v", len(issues), issues)
	}
	if issues[0].Kind != IssueOrphanNode {
		t.Errorf("expected %s, got %s", IssueOrphanNode, issues[0].Kind)
	}
	if issues[0].Target != "nowhere" {
		t.Errorf("expected the issue to name the unknown owner, got %+v", issues[0])
	}
}

// Two entries with the same id: the runtime keeps the last one and
// drops the other without a word.
func TestValidateEffectiveConfig_DuplicateID(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"t2","type":"tab","label":"Other"},
	  {"id":"n1","type":"inject","z":"t1","x":140,"y":140,"wires":[]},
	  {"id":"n1","type":"debug","z":"t2","x":300,"y":140,"wires":[]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 1 {
		t.Fatalf("expected exactly 1 issue, got %d: %+v", len(issues), issues)
	}
	if issues[0].Kind != IssueDuplicateID {
		t.Errorf("expected %s, got %s", IssueDuplicateID, issues[0].Kind)
	}
}

// An instance of a subflow whose definition is not in the configuration
// is a missing node type, and the runtime then refuses to start the
// flows at all.
func TestValidateEffectiveConfig_MissingSubflowDefinition(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"inst","type":"subflow:sf_a","z":"t1","x":140,"y":140,"wires":[]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 1 {
		t.Fatalf("expected exactly 1 issue, got %d: %+v", len(issues), issues)
	}
	if issues[0].Kind != IssueMissingSubflow {
		t.Errorf("expected %s, got %s", IssueMissingSubflow, issues[0].Kind)
	}
	if issues[0].Target != "sf_a" {
		t.Errorf("expected the issue to name the subflow, got %+v", issues[0])
	}
}

// A subflow definition and the node inside it, with an instance of it:
// the whole thing is coherent and must not be refused.
func TestValidateEffectiveConfig_SubflowWithDefinitionAndInstance(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"sf_a","type":"subflow","name":"A","in":[],"out":[]},
	  {"id":"inner","type":"inject","z":"sf_a","x":140,"y":140,"wires":[["inner2"]]},
	  {"id":"inner2","type":"debug","z":"sf_a","x":300,"y":140,"wires":[]},
	  {"id":"inst","type":"subflow:sf_a","z":"t1","x":140,"y":140,"wires":[]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %d: %+v", len(issues), issues)
	}
}

// A wire inside a subflow that names a node which is not in it does not
// deliver either.
func TestValidateEffectiveConfig_DanglingWireInsideSubflow(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"sf_a","type":"subflow","name":"A","in":[],"out":[]},
	  {"id":"inner","type":"inject","z":"sf_a","x":140,"y":140,"wires":[["ghost"]]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 1 || issues[0].Kind != IssueDanglingWire {
		t.Fatalf("expected one dangling wire, got %+v", issues)
	}
}

// The runtime recomputes the wires of a link out node from the "links"
// arrays, so a wire of a link out that points nowhere is a value the
// runtime overwrites — not a dangling wire. Refusing it would break
// legitimate link topologies.
func TestValidateEffectiveConfig_LinkOutWiresAreNotChecked(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"lo","type":"link out","z":"t1","x":140,"y":140,"links":["li"],"wires":[["ghost"]]},
	  {"id":"li","type":"link in","z":"t1","x":300,"y":140,"links":["lo"],"wires":[]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %d: %+v", len(issues), issues)
	}
}

// A link in that points at a link out in another tab is what a link is
// for: the reference crosses the tab boundary on purpose.
func TestValidateEffectiveConfig_LinkAcrossTabsIsFine(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"One"},
	  {"id":"t2","type":"tab","label":"Two"},
	  {"id":"lo","type":"link out","z":"t1","x":140,"y":140,"links":[],"wires":[[]]},
	  {"id":"li","type":"link in","z":"t2","x":300,"y":140,"links":["lo"],"wires":[]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %d: %+v", len(issues), issues)
	}
}

// A subflow instance whose type is an array is still an instance: the
// runtime coerces the type with String() before matching it.
func TestValidateEffectiveConfig_SubflowInstanceWithArrayType(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"inst","type":["subflow:sf_a"],"z":"t1","x":140,"y":140,"wires":[]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 1 || issues[0].Kind != IssueMissingSubflow {
		t.Fatalf("expected one missing subflow, got %+v", issues)
	}
}

// The runtime serves the configuration in an envelope as well as as a
// bare array; both have to be read the same way.
func TestValidateEffectiveConfig_EnvelopeShape(t *testing.T) {
	const cfg = `{"rev":"abc","flows":[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"n1","type":"inject","z":"t1","x":140,"y":140,"wires":[["ghost"]]}
	]}`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 1 || issues[0].Kind != IssueDanglingWire {
		t.Fatalf("expected one dangling wire, got %+v", issues)
	}
}

// A null document decodes into an empty array, which would read as "a
// configuration with no entries" — and deploying that would wipe the
// runtime. It has to be refused.
func TestValidateEffectiveConfig_NullDocumentIsRefused(t *testing.T) {
	issues := ValidateEffectiveConfig(RawFlow(`null`))
	if len(issues) != 1 || issues[0].Kind != "invalid_document" {
		t.Fatalf("expected one invalid_document issue, got %+v", issues)
	}
}

// A document that is not an array at all is refused the same way.
func TestValidateEffectiveConfig_GarbageIsRefused(t *testing.T) {
	issues := ValidateEffectiveConfig(RawFlow(`"not a flow"`))
	if len(issues) != 1 || issues[0].Kind != "invalid_document" {
		t.Fatalf("expected one invalid_document issue, got %+v", issues)
	}
}

// A node with no id at all cannot be referenced by a wire and is
// keyed by the runtime under "undefined". It is reported like the
// validate_flow tool reports it.
func TestValidateEffectiveConfig_NodeWithoutID(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"type":"inject","z":"t1","x":140,"y":140,"wires":[]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 1 || issues[0].Kind != IssueMissingID {
		t.Fatalf("expected one missing_id issue, got %+v", issues)
	}
}

// A node at global scope (no z) is legal: the runtime files it with
// the global configuration nodes.
func TestValidateEffectiveConfig_GlobalConfigNodeIsFine(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"cfg1","type":"mqtt-broker","name":"broker"}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %d: %+v", len(issues), issues)
	}
}

// The error names every problem, so the operator can act on the list
// without a second round trip.
func TestConfigValidationError_NamesEveryIssue(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"n1","type":"inject","z":"t1","x":140,"y":140,"wires":[["ghost"]]},
	  {"id":"inst","type":"subflow:sf_a","z":"t1","x":300,"y":140,"wires":[]}
	]`
	issues := newConfigIndex(mustFlowArray(t, cfg)).issues()
	err := &ConfigValidationError{Issues: issues}
	msg := err.Error()
	if !strings.Contains(msg, "ghost") {
		t.Errorf("expected the message to name the dangling target: %s", msg)
	}
	if !strings.Contains(msg, "sf_a") {
		t.Errorf("expected the message to name the missing subflow: %s", msg)
	}
	if !strings.Contains(msg, "2 structural problem(s)") {
		t.Errorf("expected the message to count the problems: %s", msg)
	}
}

// mustFlowArray decodes a flat configuration for a test that needs the
// entries themselves rather than the issues.
func mustFlowArray(t *testing.T, cfg string) []json.RawMessage {
	t.Helper()
	items, ok := effectiveConfigItems(RawFlow(cfg))
	if !ok {
		t.Fatalf("the fixture is not a flow array: %s", cfg)
	}
	return items
}

// A subflow whose "in" port names a node that is not in it makes the
// runtime throw when it starts an instance of that subflow, and the
// deployment that installed it had already answered with success.
func TestValidateEffectiveConfig_DanglingSubflowInPort(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"sf_a","type":"subflow","name":"A",
	   "in":[{"x":40,"y":40,"wires":[{"id":"ghost","port":0}]}],
	   "out":[{"x":160,"y":160,"wires":[{"id":"inner","port":0}]}]},
	  {"id":"inner","type":"inject","z":"sf_a","x":140,"y":140,"wires":[[]]},
	  {"id":"inst","type":"subflow:sf_a","z":"t1","x":300,"y":140,"wires":[[]]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 1 {
		t.Fatalf("expected exactly 1 issue, got %d: %+v", len(issues), issues)
	}
	if issues[0].Kind != IssueDanglingSubflowPort {
		t.Errorf("expected %s, got %s", IssueDanglingSubflowPort, issues[0].Kind)
	}
	if issues[0].Target != "ghost" {
		t.Errorf("expected the issue to name the missing node, got %+v", issues[0])
	}
}

// An "out" port that names a node which is not in the definition is
// refused too: the runtime logs "Unknown node referenced inside subflow"
// and the output never fires.
func TestValidateEffectiveConfig_DanglingSubflowOutPort(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"sf_a","type":"subflow","name":"A",
	   "in":[{"x":40,"y":40,"wires":[{"id":"inner","port":0}]}],
	   "out":[{"x":160,"y":160,"wires":[{"id":"ghost","port":0}]}]},
	  {"id":"inner","type":"inject","z":"sf_a","x":140,"y":140,"wires":[[]]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 1 || issues[0].Kind != IssueDanglingSubflowPort {
		t.Fatalf("expected one dangling port, got %+v", issues)
	}
}

// A subflow that wires its input straight to its output names its own
// id on the "out" port. The runtime handles that case explicitly, so it
// is legal and must not be refused.
func TestValidateEffectiveConfig_SubflowOutputWiredToItsOwnID(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"sf_a","type":"subflow","name":"A",
	   "in":[{"x":40,"y":40,"wires":[{"id":"inner","port":0}]}],
	   "out":[{"x":160,"y":160,"wires":[{"id":"sf_a","port":0}]}]},
	  {"id":"inner","type":"inject","z":"sf_a","x":140,"y":140,"wires":[[]]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %d: %+v", len(issues), issues)
	}
}

// The same self-reference on an "in" port is not legal: the runtime
// resolves the "in" ports against the members of the definition, and its
// own id is not one of them, so the start throws.
func TestValidateEffectiveConfig_SubflowInputWiredToItsOwnIDIsRefused(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"sf_a","type":"subflow","name":"A",
	   "in":[{"x":40,"y":40,"wires":[{"id":"sf_a","port":0}]}],
	   "out":[{"x":160,"y":160,"wires":[{"id":"sf_a","port":0}]}]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 1 || issues[0].Kind != IssueDanglingSubflowPort {
		t.Fatalf("expected one dangling port, got %+v", issues)
	}
}

// The ports of a definition whose members are nested resolve against
// those members.
func TestValidateEffectiveConfig_SubflowPortsInNestedShape(t *testing.T) {
	const def = `{
	  "id":"sf_a","type":"subflow","name":"A",
	  "in":[{"x":40,"y":40,"wires":[{"id":"inner","port":0}]}],
	  "out":[{"x":160,"y":160,"wires":[{"id":"inner","port":0}]}],
	  "nodes":[{"id":"inner","type":"inject","z":"sf_a","x":140,"y":140,"wires":[[]]}]
	}`
	if issues := subflowDefinitionPortIssues(json.RawMessage(def)); len(issues) != 0 {
		t.Fatalf("expected no issues, got %+v", issues)
	}
	broken := `{
	  "id":"sf_a","type":"subflow","name":"A",
	  "in":[{"x":40,"y":40,"wires":[{"id":"ghost","port":0}]}],
	  "nodes":[{"id":"inner","type":"inject","z":"sf_a","x":140,"y":140,"wires":[[]]}]
	}`
	if issues := subflowDefinitionPortIssues(json.RawMessage(broken)); len(issues) != 1 {
		t.Fatalf("expected one issue, got %+v", issues)
	}
}

// A link out whose "links" field is null keeps its wires at runtime:
// the runtime tests the field for truthiness, and null is falsy. The
// guard has to test it the same way, or it would leave a dangling wire
// standing in the one shape where the runtime keeps it.
func TestValidateEffectiveConfig_LinkOutWithNullLinksKeepsItsWires(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"lo","type":"link out","z":"t1","x":140,"y":140,"links":null,"wires":[["ghost"]]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 1 || issues[0].Kind != IssueDanglingWire {
		t.Fatalf("expected one dangling wire, got %+v", issues)
	}
}

// A link out with an empty links array is the editor's shape: the
// runtime rewires it, so its wires are not the caller's business.
func TestValidateEffectiveConfig_LinkOutWithEmptyLinksIsNotChecked(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"lo","type":"link out","z":"t1","x":140,"y":140,"links":[],"wires":[["ghost"]]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %d: %+v", len(issues), issues)
	}
}

// The runtime iterates the links field with forEach when it parses the
// configuration, so a truthy value that is not an array stops the deploy
// with a TypeError.
func TestValidateEffectiveConfig_LinkNodeWithANonArrayLinksField(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"lo","type":"link out","z":"t1","x":140,"y":140,"links":"nope","wires":[[]]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 1 || issues[0].Kind != IssueInvalidLinks {
		t.Fatalf("expected one invalid_links issue, got %+v", issues)
	}
}

// Two owners with the same id are one owner: the problem is reported
// once, not twice.
func TestValidateEffectiveConfig_DuplicateOwnerIsReportedOnce(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"One"},
	  {"id":"t1","type":"tab","label":"Two"},
	  {"id":"n1","type":"inject","z":"t1","x":140,"y":140,"wires":[["ghost"]]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	dangling := 0
	for _, issue := range issues {
		if issue.Kind == IssueDanglingWire {
			dangling++
		}
	}
	if dangling != 1 {
		t.Fatalf("expected the dangling wire once, got %d: %+v", dangling, issues)
	}
}

// A node whose z is not a string names no owner: the message has to
// quote what the value actually is, because an empty z is legal.
func TestValidateEffectiveConfig_OrphanNodeWithANonStringZ(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"n1","type":"inject","z":123,"x":140,"y":140,"wires":[]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 1 || issues[0].Kind != IssueOrphanNode {
		t.Fatalf("expected one orphan_node issue, got %+v", issues)
	}
	if !strings.Contains(issues[0].Message, "123") {
		t.Errorf("expected the message to quote the value, got: %s", issues[0].Message)
	}
}
