package mcp

import (
	"reflect"
	"testing"
)

// TestIssue371_RemintRewritesOnlyRealReferences: importing a copy of a
// tab into the instance it came from remints every tab-owned id. The
// rewrite used to replace any string value equal to a reminted id, so
// free text — a node named "n1" — was silently rewritten too; short
// hand-written ids make that false match real (issue #371). Only
// structural references (id, wires, g, a group's nodes) and config-node
// properties (an mqtt out's broker) are rewritten; free text naming a
// canvas node survives untouched.
func TestIssue371_RemintRewritesOnlyRealReferences(t *testing.T) {
	rt, s := newFakeRuntime(t, `[
	  {"id":"other","type":"tab","label":"Other"},
	  {"id":"n1","type":"inject","z":"other","x":1,"y":1,"wires":[]},
	  {"id":"cfg1","type":"mqtt-broker","z":"other","name":"broker"}
	]`)
	res := importClipboard(t, s, `[
	  {"id":"t","type":"tab"},
	  {"id":"g1","type":"group","z":"t","nodes":["n1","n2"],"x":0,"y":0,"w":10,"h":10},
	  {"id":"n1","type":"inject","z":"t","g":"g1","name":"n1","payload":"n1","topic":"n1","x":1,"y":1,"wires":[["n2"]]},
	  {"id":"n2","type":"debug","z":"t","g":"g1","name":"n2","x":2,"y":2,"wires":[]},
	  {"id":"out","type":"mqtt out","z":"t","broker":"cfg1","x":3,"y":3,"wires":[]},
	  {"id":"cfg1","type":"mqtt-broker","z":"t","name":"broker","tls":"tls1"},
	  {"id":"tls1","type":"tls-config","name":"tls"}
	]`)
	if res.IsError {
		t.Fatalf("import refused: %s", resultFirstText(res))
	}
	post := rt.lastPost(t)
	pick := func(key, typ string) map[string]any {
		t.Helper()
		members, _ := post[key].([]any)
		for _, m := range members {
			if node := m.(map[string]any); node["type"] == typ {
				return node
			}
		}
		t.Fatalf("posted %s has no %s node: %v", key, typ, post)
		return nil
	}
	inject, debug := pick("nodes", "inject"), pick("nodes", "debug")
	group, mqttOut := pick("nodes", "group"), pick("nodes", "mqtt out")
	broker, tls := pick("configs", "mqtt-broker"), pick("configs", "tls-config")

	// Free text that spells out a reminted canvas-node id is not a
	// reference: it survives the remint unchanged.
	for _, tc := range []struct {
		node        map[string]any
		field, want string
	}{
		{inject, "name", "n1"}, {inject, "payload", "n1"}, {inject, "topic", "n1"},
		{debug, "name", "n2"},
	} {
		if got := tc.node[tc.field]; got != tc.want {
			t.Errorf("%s.%s = %v, want the free text %q kept", tc.node["type"], tc.field, got, tc.want)
		}
	}
	// Every tab-owned id was reminted…
	for _, old := range []string{"g1", "n1", "n2", "out", "cfg1", "tls1"} {
		for _, node := range []map[string]any{inject, debug, group, mqttOut, broker, tls} {
			if node["id"] == old {
				t.Errorf("node kept the existing id %s: %v", old, node)
			}
		}
	}
	// …so every real reference points at the new ids.
	if w := inject["wires"].([]any)[0].([]any)[0]; w != debug["id"] {
		t.Errorf("inject wire = %v, want the new id of n2 (%v)", w, debug["id"])
	}
	if g := inject["g"]; g != group["id"] {
		t.Errorf("inject g = %v, want the new id of g1 (%v)", g, group["id"])
	}
	if nodes := group["nodes"]; !reflect.DeepEqual(nodes, []any{inject["id"], debug["id"]}) {
		t.Errorf("group nodes = %v, want the new ids of n1 and n2 (%v %v)", nodes, inject["id"], debug["id"])
	}
	if b := mqttOut["broker"]; b != broker["id"] {
		t.Errorf("mqtt out broker = %v, want the new id of cfg1 (%v)", b, broker["id"])
	}
	if tl := broker["tls"]; tl != tls["id"] {
		t.Errorf("mqtt-broker tls = %v, want the new id of tls1 (%v)", tl, tls["id"])
	}
}
