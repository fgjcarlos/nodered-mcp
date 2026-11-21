package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Issue #409: import_flow names the http in routes the copy shares with a
// node already on the instance, and still imports.

const issue409Instance = `[
  {"id":"t1","type":"tab","label":"Flow 1"},
  {"id":"n1","type":"http in","z":"t1","method":"get","url":"/hello","x":1,"y":1,"wires":[]},
  {"id":"n2","type":"http in","z":"t1","method":"post","url":"/off","d":true,"x":1,"y":1,"wires":[]},
  {"id":"t2","type":"tab","label":"Parked","disabled":true},
  {"id":"n3","type":"http in","z":"t2","method":"put","url":"/parked","x":1,"y":1,"wires":[]}
]`

func issue409Collisions(t *testing.T, clipNodes string) []routeCollision {
	t.Helper()
	doc, err := parseClipboard(json.RawMessage(`[{"id":"tc","type":"tab","label":"Copy"}` + clipNodes + `]`))
	if err != nil {
		t.Fatalf("parseClipboard: %v", err)
	}
	var current []json.RawMessage
	if err := json.Unmarshal([]byte(issue409Instance), &current); err != nil {
		t.Fatal(err)
	}
	return routeCollisions(doc, current)
}

func TestIssue409_RouteCollisions(t *testing.T) {
	hello := []routeCollision{{method: "get", url: "/hello", nodeID: "n1", tabID: "t1", tabLabel: "Flow 1"}}
	cases := []struct {
		name, nodes string
		want        []routeCollision
	}{
		{"same method and url",
			`,{"id":"c1","type":"http in","z":"tc","method":"get","url":"/hello","x":1,"y":1,"wires":[]}`, hello},
		{"url without its leading slash",
			`,{"id":"c1","type":"http in","z":"tc","method":"get","url":"hello","x":1,"y":1,"wires":[]}`, hello},
		{"two copies of one route are named once",
			`,{"id":"c1","type":"http in","z":"tc","method":"get","url":"/hello","x":1,"y":1,"wires":[]}` +
				`,{"id":"c2","type":"http in","z":"tc","method":"get","url":"hello","x":1,"y":1,"wires":[]}`, hello},
		{"different method",
			`,{"id":"c1","type":"http in","z":"tc","method":"post","url":"/hello","x":1,"y":1,"wires":[]}`, nil},
		// 21-httpin.js registers only exact get/post/put/patch/delete; any
		// other method (there is no "all") registers no route.
		{"all is not a Node-RED http in method",
			`,{"id":"c1","type":"http in","z":"tc","method":"all","url":"/hello","x":1,"y":1,"wires":[]}`, nil},
		{"upper-case method registers nothing",
			`,{"id":"c1","type":"http in","z":"tc","method":"GET","url":"/hello","x":1,"y":1,"wires":[]}`, nil},
		{"existing node disabled",
			`,{"id":"c1","type":"http in","z":"tc","method":"post","url":"/off","x":1,"y":1,"wires":[]}`, nil},
		{"existing tab disabled",
			`,{"id":"c1","type":"http in","z":"tc","method":"put","url":"/parked","x":1,"y":1,"wires":[]}`, nil},
		{"copy disabled",
			`,{"id":"c1","type":"http in","z":"tc","method":"get","url":"/hello","d":true,"x":1,"y":1,"wires":[]}`, nil},
		{"not an http in",
			`,{"id":"c1","type":"http request","z":"tc","method":"get","url":"/hello","x":1,"y":1,"wires":[]}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := issue409Collisions(t, tc.nodes); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestIssue409_DisabledCopyTab: a disabled imported tab registers nothing.
func TestIssue409_DisabledCopyTab(t *testing.T) {
	doc, err := parseClipboard(json.RawMessage(`[{"id":"tc","type":"tab","label":"Copy","disabled":true},
	  {"id":"c1","type":"http in","z":"tc","method":"get","url":"/hello","x":1,"y":1,"wires":[]}]`))
	if err != nil {
		t.Fatal(err)
	}
	var current []json.RawMessage
	_ = json.Unmarshal([]byte(issue409Instance), &current)
	if got := routeCollisions(doc, current); got != nil {
		t.Fatalf("got %+v, want none", got)
	}
}

// TestIssue409_ImportNamesCollidingRoute: the import still happens and the
// result names the route, the node and tab serving it, and the redeploy.
func TestIssue409_ImportNamesCollidingRoute(t *testing.T) {
	rt, s := newFakeRuntime(t, issue409Instance)
	res := importClipboard(t, s, `[{"id":"tc","type":"tab","label":"Copy"},
	  {"id":"c1","type":"http in","z":"tc","method":"get","url":"hello","x":1,"y":1,"wires":[]}]`)
	if res.IsError {
		t.Fatalf("import refused: %s", resultFirstText(res))
	}
	if len(rt.posted) != 1 {
		t.Fatalf("POST /flow calls = %d, want 1", len(rt.posted))
	}
	want := ` These http in routes of the copy are already served on this instance: [get] /hello by node "n1" on tab "t1" ("Flow 1"). ` +
		`Both copies stay registered; deleting the copy later also unregisters the original route until its tab is redeployed (disable_flow + enable_flow, or any deploy of that tab).`
	if got := resultFirstText(res); !strings.HasSuffix(got, want) {
		t.Fatalf("result = %q, want suffix %q", got, want)
	}
}

// TestIssue409_ImportWithoutCollisionUnchanged: no shared route, no notice.
func TestIssue409_ImportWithoutCollisionUnchanged(t *testing.T) {
	_, s := newFakeRuntime(t, issue409Instance)
	res := importClipboard(t, s, `[{"id":"tc","type":"tab","label":"Copy"},
	  {"id":"c1","type":"http in","z":"tc","method":"get","url":"/other","x":1,"y":1,"wires":[]}]`)
	if got := resultFirstText(res); res.IsError || strings.Contains(got, "http in") {
		t.Fatalf("result = %q (error=%v), want the plain import message", got, res.IsError)
	}
}
