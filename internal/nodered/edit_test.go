package nodered

import (
	"encoding/json"
	"strings"
	"testing"
)

// sampleTab is the shape GET /flow/:id actually returns. Node-RED splits a
// tab's contents in two: anything carrying x/y canvas coordinates lands in
// "nodes", everything else in "configs". A shared broker has no coordinates and
// therefore lives in configs, inside the same tab.
//
// The function node carries a body and the mqtt node carries a topic and a
// broker reference — fields no fixed schema would know about, and exactly what
// a careless edit destroys.
const sampleTab = `{
  "id": "tabA",
  "label": "Home",
  "nodes": [
    {"id":"n1","type":"inject","z":"tabA","x":100,"y":80,"repeat":"5","payload":"go","wires":[["n2"]]},
    {"id":"n2","type":"function","z":"tabA","x":260,"y":80,"func":"return msg;","outputs":1,"wires":[["n3"]]},
    {"id":"n3","type":"mqtt out","z":"tabA","x":420,"y":80,"topic":"home/out","broker":"b1","qos":"1","wires":[]}
  ],
  "configs": [
    {"id":"b1","type":"mqtt-broker","z":"tabA","name":"local","broker":"localhost","port":"1883"}
  ]
}`

// nodeByID pulls one node out of a flow document, from either collection.
func nodeByID(t *testing.T, flow RawFlow, id string) map[string]any {
	t.Helper()
	var doc struct {
		Nodes   []map[string]any `json:"nodes"`
		Configs []map[string]any `json:"configs"`
	}
	if err := json.Unmarshal(flow, &doc); err != nil {
		t.Fatalf("unmarshalling flow: %v", err)
	}
	for _, n := range append(doc.Nodes, doc.Configs...) {
		if n["id"] == id {
			return n
		}
	}
	return nil
}

// collectionCounts reports how many entries sit in each collection, so tests
// can assert that a node landed where Node-RED would have put it.
func collectionCounts(t *testing.T, flow RawFlow) (nodes, configs int) {
	t.Helper()
	var doc struct {
		Nodes   []json.RawMessage `json:"nodes"`
		Configs []json.RawMessage `json:"configs"`
	}
	if err := json.Unmarshal(flow, &doc); err != nil {
		t.Fatalf("unmarshalling flow: %v", err)
	}
	return len(doc.Nodes), len(doc.Configs)
}

func nodeCount(t *testing.T, flow RawFlow) int {
	n, c := collectionCounts(t, flow)
	return n + c
}

// Node-RED classifies by the presence of x/y, so this code must too: a node
// AddNode once routed anything without x/y into the "configs" collection,
// mirroring Node-RED's split. Issue #26/#27 changed that: a node without
// coords (typically a broker or credential holder) is now rejected with
// an actionable error rather than silently filed into configs and left
// un-deployed by the runtime. The split still exists in Node-RED itself —
// the MCP just refuses to write a payload the runtime would not honour.
func TestAddNodeFilesByCanvasCoordinates(t *testing.T) {
	withXY, err := AddNodeToFlow(RawFlow(sampleTab), json.RawMessage(`{"id":"n4","type":"debug","z":"tabA","x":580,"y":80,"wires":[]}`))
	if err != nil {
		t.Fatalf("AddNodeToFlow: %v", err)
	}
	if n, c := collectionCounts(t, withXY); n != 4 || c != 1 {
		t.Errorf("a node with coordinates belongs in nodes: got %d nodes, %d configs", n, c)
	}

	// Without x/y the node must be rejected with the actionable message that
	// names the symptom (un-deployed by the runtime) and the cure (add x/y).
	_, err = AddNodeToFlow(RawFlow(sampleTab), json.RawMessage(`{"id":"b2","type":"mqtt-broker","z":"tabA","name":"other"}`))
	if err == nil {
		t.Fatal("expected AddNodeToFlow to reject a node without x/y canvas coordinates")
	}
	if !strings.Contains(err.Error(), "x/y canvas coordinates") {
		t.Errorf("expected the actionable x/y message, got %q", err.Error())
	}
}

// TestAddNodeRejectsDanglingZ covers issue #27: a node whose z references
// something that does not exist (the owning tab or another node) makes the
// runtime crash with `Cannot read properties of undefined (reading wires')`
// at the next deploy and refuses to start. Refuse it before the write.
func TestAddNodeRejectsDanglingZ(t *testing.T) {
	_, err := AddNodeToFlow(RawFlow(sampleTab), json.RawMessage(`{"id":"x1","type":"inject","z":"ghost","x":1,"y":1,"wires":[]}`))
	if err == nil {
		t.Fatal("expected AddNodeToFlow to reject a node whose z does not resolve")
	}
	if !strings.Contains(err.Error(), "z=\"ghost\"") {
		t.Errorf("expected the error to name the dangling z, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "Cannot read properties of undefined") {
		t.Errorf("expected the error to name the runtime crash, got %q", err.Error())
	}
}

func TestAddNodeAppendsAndPreservesTheRest(t *testing.T) {
	got, err := AddNodeToFlow(RawFlow(sampleTab), json.RawMessage(`{"id":"n4","type":"debug","z":"tabA","x":580,"y":80,"wires":[]}`))
	if err != nil {
		t.Fatalf("AddNodeToFlow: %v", err)
	}
	if n := nodeCount(t, got); n != 5 {
		t.Errorf("expected 5 objects (4 nodes + 1 config), got %d", n)
	}
	// The shared broker must survive an unrelated edit.
	if nodeByID(t, got, "b1") == nil {
		t.Error("the config node was dropped")
	}
	if nodeByID(t, got, "n4") == nil {
		t.Error("the new node is missing")
	}
	// The tab's own metadata must survive.
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc["label"] != "Home" {
		t.Errorf("tab label was lost, got %v", doc["label"])
	}
}

func TestAddNodeRejectsADuplicateID(t *testing.T) {
	// Two nodes sharing an id is silently accepted by Node-RED and produces a
	// flow whose behaviour depends on ordering. Refuse it here.
	if _, err := AddNodeToFlow(RawFlow(sampleTab), json.RawMessage(`{"id":"n2","type":"debug","x":1,"y":1,"wires":[]}`)); err == nil {
		t.Fatal("expected a duplicate id to be rejected")
	}
	// The collision must also be caught across collections: a new node cannot
	// reuse the id of an existing config node either.
	if _, err := AddNodeToFlow(RawFlow(sampleTab), json.RawMessage(`{"id":"b1","type":"debug","x":1,"y":1,"wires":[]}`)); err == nil {
		t.Fatal("expected a collision with a config node id to be rejected")
	}
}

func TestAddNodeRejectsANodeWithoutAnID(t *testing.T) {
	if _, err := AddNodeToFlow(RawFlow(sampleTab), json.RawMessage(`{"type":"debug"}`)); err == nil {
		t.Fatal("expected a node without an id to be rejected")
	}
}

// The reason granular editing exists: changing one property must not disturb
// any other, including properties this code has never heard of.
func TestUpdateNodeMergesAndKeepsUnknownFields(t *testing.T) {
	got, err := UpdateNodeInFlow(RawFlow(sampleTab), "n3", map[string]json.RawMessage{
		"topic": json.RawMessage(`"home/changed"`),
	})
	if err != nil {
		t.Fatalf("UpdateNodeInFlow: %v", err)
	}

	n3 := nodeByID(t, got, "n3")
	if n3["topic"] != "home/changed" {
		t.Errorf("topic not updated, got %v", n3["topic"])
	}
	for field, want := range map[string]any{"broker": "b1", "qos": "1", "type": "mqtt out", "z": "tabA"} {
		if n3[field] != want {
			t.Errorf("field %q was lost or altered: got %v, want %v", field, n3[field], want)
		}
	}
	// Other nodes must be untouched, function body included.
	n2 := nodeByID(t, got, "n2")
	if n2["func"] != "return msg;" {
		t.Errorf("an unrelated node's body changed: %v", n2["func"])
	}
}

func TestUpdateNodeRefusesToChangeTheID(t *testing.T) {
	// Rewriting an id orphans every wire pointing at it.
	_, err := UpdateNodeInFlow(RawFlow(sampleTab), "n3", map[string]json.RawMessage{
		"id": json.RawMessage(`"somethingElse"`),
	})
	if err == nil {
		t.Fatal("expected changing the id to be rejected")
	}
}

func TestUpdateNodeRejectsAnUnknownNode(t *testing.T) {
	_, err := UpdateNodeInFlow(RawFlow(sampleTab), "nope", map[string]json.RawMessage{
		"topic": json.RawMessage(`"x"`),
	})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected an error naming the missing node, got %v", err)
	}
}

func TestDeleteNodeAlsoRemovesWiresPointingAtIt(t *testing.T) {
	got, err := DeleteNodeFromFlow(RawFlow(sampleTab), "n3")
	if err != nil {
		t.Fatalf("DeleteNodeFromFlow: %v", err)
	}
	if n := nodeCount(t, got); n != 3 {
		t.Errorf("expected 3 objects left (2 nodes + 1 config), got %d", n)
	}
	if nodeByID(t, got, "n3") != nil {
		t.Error("the deleted node is still present")
	}
	// n2 wired to n3. Leaving that wire behind is exactly the dangling
	// reference validateFlowWires would later reject.
	if strings.Contains(string(got), `"n3"`) {
		t.Errorf("a wire still references the deleted node: %s", got)
	}
}

func TestDeleteNodeRejectsAnUnknownNode(t *testing.T) {
	if _, err := DeleteNodeFromFlow(RawFlow(sampleTab), "nope"); err == nil {
		t.Fatal("expected deleting a non-existent node to be rejected")
	}
}

func TestConnectNodesAppendsToTheRightPort(t *testing.T) {
	// n1 already wires port 0 to n2. Adding n3 must extend that port, not
	// replace it — replacing is the classic whole-array rewrite mistake.
	got, err := ConnectNodesInFlow(RawFlow(sampleTab), "n1", 0, "n3")
	if err != nil {
		t.Fatalf("ConnectNodesInFlow: %v", err)
	}
	wires := nodeByID(t, got, "n1")["wires"].([]any)
	port0 := wires[0].([]any)
	if len(port0) != 2 || port0[0] != "n2" || port0[1] != "n3" {
		t.Errorf("port 0 should be [n2 n3], got %v", port0)
	}
}

func TestConnectNodesGrowsTheWiresArrayForAHigherPort(t *testing.T) {
	// A switch node's second output does not exist in the array until used.
	got, err := ConnectNodesInFlow(RawFlow(sampleTab), "n1", 2, "n3")
	if err != nil {
		t.Fatalf("ConnectNodesInFlow: %v", err)
	}
	wires := nodeByID(t, got, "n1")["wires"].([]any)
	if len(wires) != 3 {
		t.Fatalf("expected the wires array to grow to 3 ports, got %d", len(wires))
	}
	if port1 := wires[1].([]any); len(port1) != 0 {
		t.Errorf("the skipped port should be an empty array, got %v", port1)
	}
	if port2 := wires[2].([]any); len(port2) != 1 || port2[0] != "n3" {
		t.Errorf("port 2 should be [n3], got %v", port2)
	}
}

func TestConnectNodesIsIdempotent(t *testing.T) {
	got, err := ConnectNodesInFlow(RawFlow(sampleTab), "n1", 0, "n2")
	if err != nil {
		t.Fatalf("ConnectNodesInFlow: %v", err)
	}
	port0 := nodeByID(t, got, "n1")["wires"].([]any)[0].([]any)
	if len(port0) != 1 {
		t.Errorf("connecting an existing link should not duplicate it, got %v", port0)
	}
}

func TestConnectNodesRejectsUnknownEndpoints(t *testing.T) {
	if _, err := ConnectNodesInFlow(RawFlow(sampleTab), "nope", 0, "n2"); err == nil {
		t.Error("expected an unknown source to be rejected")
	}
	// A wire to a node that does not exist is the dangling reference Node-RED
	// accepts silently and never runs.
	if _, err := ConnectNodesInFlow(RawFlow(sampleTab), "n1", 0, "nope"); err == nil {
		t.Error("expected an unknown target to be rejected")
	}
}

func TestConnectNodesRejectsANegativePort(t *testing.T) {
	if _, err := ConnectNodesInFlow(RawFlow(sampleTab), "n1", -1, "n2"); err == nil {
		t.Error("expected a negative port to be rejected")
	}
}

// TestConnectNodesRejectsAPortOverTheBound guards against a port=999999 call
// from growing the wires array to a multi-megabyte allocation before the HTTP
// round trip. Node-RED's editor never uses more than a single-digit port; 999
// is already generous and keeps the wires slice trivially small.
func TestConnectNodesRejectsAPortOverTheBound(t *testing.T) {
	_, err := ConnectNodesInFlow(RawFlow(sampleTab), "n1", 1000, "n2")
	if err == nil {
		t.Fatal("expected a port above 999 to be rejected")
	}
	if !strings.Contains(err.Error(), "out of range") && !strings.Contains(err.Error(), "999") {
		t.Errorf("error should mention the bound, got: %v", err)
	}
}

// Every edit must leave a document that still passes wire validation, which is
// what the write path checks before it reaches Node-RED.
func TestEditsProduceValidFlows(t *testing.T) {
	steps := []struct {
		name string
		fn   func(RawFlow) (RawFlow, error)
	}{
		{"add", func(f RawFlow) (RawFlow, error) {
			return AddNodeToFlow(f, json.RawMessage(`{"id":"n9","type":"debug","z":"tabA","x":580,"y":80,"wires":[]}`))
		}},
		{"connect", func(f RawFlow) (RawFlow, error) { return ConnectNodesInFlow(f, "n3", 0, "n9") }},
		{"update", func(f RawFlow) (RawFlow, error) {
			return UpdateNodeInFlow(f, "n9", map[string]json.RawMessage{"name": json.RawMessage(`"tap"`)})
		}},
		{"delete", func(f RawFlow) (RawFlow, error) { return DeleteNodeFromFlow(f, "n9") }},
	}

	flow := RawFlow(sampleTab)
	for _, s := range steps {
		next, err := s.fn(flow)
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if err := validateFlowWires(next); err != nil {
			t.Fatalf("after %s the flow has broken wires: %v", s.name, err)
		}
		flow = next
	}
	// Back to where we started, structurally.
	if n := nodeCount(t, flow); n != 4 {
		t.Errorf("expected to be back at 4 objects, got %d", n)
	}
}

// Config nodes live inside the tab too, and are exactly the ones a model needs
// to retune when a broker address or a credential reference is wrong.
func TestUpdateNodeReachesConfigNodes(t *testing.T) {
	got, err := UpdateNodeInFlow(RawFlow(sampleTab), "b1", map[string]json.RawMessage{
		"broker": json.RawMessage(`"mqtt.example.com"`),
	})
	if err != nil {
		t.Fatalf("UpdateNodeInFlow on a config node: %v", err)
	}
	b1 := nodeByID(t, got, "b1")
	if b1 == nil {
		t.Fatal("the config node vanished")
	}
	if b1["broker"] != "mqtt.example.com" {
		t.Errorf("broker not updated, got %v", b1["broker"])
	}
	if b1["port"] != "1883" || b1["name"] != "local" {
		t.Errorf("unrelated config properties were lost: %v", b1)
	}
	if n, c := collectionCounts(t, got); n != 3 || c != 1 {
		t.Errorf("editing must not move a node between collections: %d nodes, %d configs", n, c)
	}
}

func TestDeleteNodeReachesConfigNodes(t *testing.T) {
	got, err := DeleteNodeFromFlow(RawFlow(sampleTab), "b1")
	if err != nil {
		t.Fatalf("DeleteNodeFromFlow on a config node: %v", err)
	}
	if nodeByID(t, got, "b1") != nil {
		t.Error("the config node is still present")
	}
	if n, c := collectionCounts(t, got); n != 3 || c != 0 {
		t.Errorf("expected 3 nodes and no configs, got %d and %d", n, c)
	}
}

// Node-RED omits the configs key entirely when a flow has none. Emitting an
// empty array instead would be a gratuitous difference on every write.
func TestEmptyConfigsKeyIsOmitted(t *testing.T) {
	got, err := DeleteNodeFromFlow(RawFlow(sampleTab), "b1")
	if err != nil {
		t.Fatalf("DeleteNodeFromFlow: %v", err)
	}
	if strings.Contains(string(got), "configs") {
		t.Errorf("an empty configs array should be omitted: %s", got)
	}
}
