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

// Issue #408: the runtime indexes the wires of an "out" port by its
// "port" (Subflow.js: node.wires[wires[j].port], and for a pass-through
// subflowInstanceConfig.wires[wires[j].port], built from the "in" ports).
// A missing or invalid port drops the output or throws when an instance
// starts, so it is refused; the "in" ports carry only an id.
func TestValidateEffectiveConfig_SubflowOutPortIndex(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		outWire string
		ok      bool
	}{
		{"missing port", `[{"id":"inner"}]`, `{"id":"inner"}`, false},
		{"string port", `[{"id":"inner"}]`, `{"id":"inner","port":"0"}`, false},
		{"null port", `[{"id":"inner"}]`, `{"id":"inner","port":null}`, false},
		{"negative port", `[{"id":"inner"}]`, `{"id":"inner","port":-1}`, false},
		{"fractional port", `[{"id":"inner"}]`, `{"id":"inner","port":0.5}`, false},
		{"port beyond the node's outputs", `[{"id":"inner"}]`, `{"id":"inner","port":1}`, false},
		{"port beyond a node with no outputs", `[{"id":"inner"}]`, `{"id":"sink","port":0}`, false},
		{"pass-through port beyond the in ports", `[{"id":"inner"}]`, `{"id":"sf_a","port":1}`, false},
		{"pass-through with no in port", ``, `{"id":"sf_a","port":0}`, false},
		{"pass-through missing port", `[{"id":"inner"}]`, `{"id":"sf_a"}`, false},
		{"port 0", `[{"id":"inner"}]`, `{"id":"inner","port":0}`, true},
		{"second output", `[{"id":"inner"}]`, `{"id":"two","port":1}`, true},
		{"node whose outputs are not known", `[{"id":"inner"}]`, `{"id":"bare","port":3}`, true},
		{"pass-through port 0", `[{"id":"inner"}]`, `{"id":"sf_a","port":0}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := `[]`
			if tc.in != "" {
				in = `[{"x":40,"y":40,"wires":` + tc.in + `}]`
			}
			cfg := `[
			  {"id":"t1","type":"tab","label":"Home"},
			  {"id":"sf_a","type":"subflow","name":"A","in":` + in + `,
			   "out":[{"x":160,"y":160,"wires":[` + tc.outWire + `]}]},
			  {"id":"inner","type":"function","z":"sf_a","x":140,"y":140,"wires":[[]]},
			  {"id":"two","type":"switch","z":"sf_a","x":140,"y":200,"wires":[[],[]]},
			  {"id":"sink","type":"debug","z":"sf_a","x":140,"y":260,"wires":[]},
			  {"id":"bare","type":"function","z":"sf_a","x":140,"y":320}
			]`
			issues := ValidateEffectiveConfig(RawFlow(cfg))
			if tc.ok {
				if len(issues) != 0 {
					t.Fatalf("expected no issues, got %+v", issues)
				}
				return
			}
			if len(issues) != 1 || issues[0].Kind != IssueInvalidSubflowPort {
				t.Fatalf("expected one %s issue, got %+v", IssueInvalidSubflowPort, issues)
			}
			if !strings.Contains(issues[0].Message, `subflow "sf_a"`) || !strings.Contains(issues[0].Message, "port") {
				t.Errorf("expected the message to name the subflow and the port: %s", issues[0].Message)
			}
			// The flat array a model hands validate_flow is judged the same way.
			if got := ValidateFlows(RawFlow(cfg)); len(got) != 1 || got[0].Kind != IssueInvalidSubflowPort {
				t.Errorf("validate_flow should report it too, got %+v", got)
			}
		})
	}
}

// The nested shape the subflow write path carries is judged the same way,
// and the error names the subflow, the output and the target.
func TestValidateSubflowPorts_RefusesOutWireWithoutPort(t *testing.T) {
	def := `{
	  "id":"sf_a","type":"subflow","name":"A",
	  "in":[{"x":40,"y":40,"wires":[{"id":"fn"}]}],
	  "out":[{"x":300,"y":30,"wires":[{"id":"fn"}]}],
	  "nodes":[{"id":"fn","type":"function","z":"sf_a","x":140,"y":140,"wires":[[]]}]
	}`
	err := validateSubflowPorts(json.RawMessage(def))
	if err == nil {
		t.Fatal("expected the port-less out wire to be refused")
	}
	for _, want := range []string{`subflow "sf_a"`, `output 0`, `"fn"`, "port"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should contain %q: %v", want, err)
		}
	}
	ok := strings.Replace(def, `"out":[{"x":300,"y":30,"wires":[{"id":"fn"}]}]`, `"out":[{"x":300,"y":30,"wires":[{"id":"fn","port":0}]}]`, 1)
	if err := validateSubflowPorts(json.RawMessage(ok)); err != nil {
		t.Fatalf("port 0 should be accepted: %v", err)
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

// A canvas node with no z at all is dropped by the runtime: it looks up
// the node's container by the z, finds none, and never runs the node.
func TestValidateEffectiveConfig_CanvasNodeWithoutZIsAnOrphan(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"n1","type":"inject","x":140,"y":140,"wires":[]}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 1 || issues[0].Kind != IssueOrphanNode {
		t.Fatalf("expected one orphan_node issue, got %+v", issues)
	}
}

// A configuration node with no z is legal: the runtime files it at
// global scope.
func TestValidateEffectiveConfig_GlobalConfigNodeWithoutZIsFine(t *testing.T) {
	const cfg = `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"cfg1","type":"mqtt-broker","name":"broker"}
	]`
	issues := ValidateEffectiveConfig(RawFlow(cfg))
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %d: %+v", len(issues), issues)
	}
}

// A link node whose links field is not an array is refused by the
// per-document guard too, so the tab write path stops it before the
// runtime does.
func TestValidateFlowWires_RefusesALinkNodeWithANonArrayLinksField(t *testing.T) {
	doc := `{
	  "id":"tabA","label":"Home",
	  "nodes":[
	    {"id":"lo","type":"link out","z":"tabA","x":140,"y":140,"links":"nope","wires":[[]]}
	  ]
	}`
	err := validateFlowWires(RawFlow(doc))
	if err == nil {
		t.Fatal("expected validateFlowWires to refuse a link node with a non-array links field, got nil")
	}
	if !strings.Contains(err.Error(), "links") {
		t.Errorf("expected the error to name the links field: %v", err)
	}
}

// The shapes the Node-RED editor exports stay accepted (#408: no false
// positives): two outputs, a junction, a nested instance, a pass-through,
// a status port, env and a subflow with no ports.
func TestValidateEffectiveConfig_SubflowEditorShapesAccepted(t *testing.T) {
	cfg := `[{"id":"t1","type":"tab"},
	{"id":"sfX","type":"subflow","in":[{"x":1,"y":1,"wires":[{"id":"ch"}]}],
	 "out":[{"x":1,"y":1,"wires":[{"id":"ch","port":0},{"id":"j","port":0},{"id":"nest","port":1}]}],
	 "status":{"x":1,"y":1,"wires":[{"id":"st","port":0}]},"env":[{"name":"FOO","type":"str","value":"bar"}]},
	{"id":"ch","type":"change","z":"sfX","rules":[],"x":1,"y":1,"wires":[["j"]]},
	{"id":"j","type":"junction","z":"sfX","x":1,"y":1,"wires":[[]]},
	{"id":"st","type":"status","z":"sfX","scope":null,"x":1,"y":1,"wires":[[]]},
	{"id":"nest","type":"subflow:sfP","z":"sfX","x":1,"y":1,"wires":[[],[]]},
	{"id":"sfP","type":"subflow","in":[{"x":1,"y":1,"wires":[]}],"out":[{"x":1,"y":1,"wires":[{"id":"sfP","port":0}]},{"x":2,"y":2,"wires":[]}]},
	{"id":"sfN","type":"subflow","in":[],"out":[]}]`
	if got := ValidateEffectiveConfig(RawFlow(cfg)); len(got) != 0 {
		t.Fatalf("editor shapes must validate, got %+v", got)
	}
}
