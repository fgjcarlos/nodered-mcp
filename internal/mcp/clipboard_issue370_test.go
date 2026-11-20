package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestIssue370_PositionedNodeWithoutZIsRefused: a node with canvas
// coordinates and no z is named in a refusal, never filed as a global
// config node and dropped because its id exists.
func TestIssue370_PositionedNodeWithoutZIsRefused(t *testing.T) {
	rt, s := newFakeRuntime(t, `[
	  {"id":"other","type":"tab","label":"Other"},
	  {"id":"n1","type":"inject","z":"other","x":1,"y":1,"wires":[]}
	]`)
	res := importClipboard(t, s, `[{"id":"t","type":"tab"},{"id":"n1","type":"inject","x":1,"y":1,"wires":[]}]`)
	want := `clipboard element "n1" (type "inject") has canvas coordinates but no z; set its z to the tab id "t"`
	if !res.IsError || resultFirstText(res) != want {
		t.Fatalf("got %q (error=%v), want %q", resultFirstText(res), res.IsError, want)
	}
	if len(rt.posted) != 0 {
		t.Error("a refused clipboard reached POST /flow")
	}
}

// TestIssue370_GlobalWithOtherTypeIsNotReused: a global config node whose
// id exists on the instance with another type travels with the tab under a
// fresh id, and the nodes that use it are rewired to the copy.
func TestIssue370_GlobalWithOtherTypeIsNotReused(t *testing.T) {
	rt, s := newFakeRuntime(t, `[{"id":"broker1","type":"tls-config"}]`)
	res := importClipboard(t, s, `[
	  {"id":"t","type":"tab","label":"T"},
	  {"id":"in1","type":"mqtt in","z":"t","x":1,"y":1,"broker":"broker1","wires":[]},
	  {"id":"broker1","type":"mqtt-broker","broker":"localhost"}
	]`)
	if res.IsError {
		t.Fatalf("import refused: %s", resultFirstText(res))
	}
	if len(rt.posted) != 1 {
		t.Fatalf("posted %d documents, want 1", len(rt.posted))
	}
	configs, _ := rt.posted[0]["configs"].([]any)
	nodes, _ := rt.posted[0]["nodes"].([]any)
	if len(configs) != 1 || len(nodes) != 1 {
		t.Fatalf("posted %d configs and %d nodes, want 1 and 1: %v", len(configs), len(nodes), rt.posted[0])
	}
	cfg := configs[0].(map[string]any)
	in := nodes[0].(map[string]any)
	if cfg["type"] != "mqtt-broker" || cfg["id"] == "broker1" {
		t.Errorf("config = %v, want an mqtt-broker under a fresh id", cfg)
	}
	if in["broker"] != cfg["id"] {
		t.Errorf("mqtt in broker = %v, want the copied config %v", in["broker"], cfg["id"])
	}
	want := ` 1 of the copy's 2 node ids already existed on this instance, so all 2 were reminted and their wires and references rewritten.`
	if got := resultFirstText(res); !strings.HasSuffix(got, want) {
		t.Errorf("message = %q, want suffix %q", got, want)
	}
}

// TestIssue370_TabScopedConfigIsNotReusedAsGlobal: an id the instance has
// as another tab's config node is not a global dependency to bind to.
func TestIssue370_TabScopedConfigIsNotReusedAsGlobal(t *testing.T) {
	rt, s := newFakeRuntime(t, `[
	  {"id":"other","type":"tab","label":"Other"},
	  {"id":"broker1","type":"mqtt-broker","z":"other"}
	]`)
	res := importClipboard(t, s, `[
	  {"id":"t","type":"tab","label":"T"},
	  {"id":"in1","type":"mqtt in","z":"t","x":1,"y":1,"broker":"broker1","wires":[]},
	  {"id":"broker1","type":"mqtt-broker"}
	]`)
	if res.IsError {
		t.Fatalf("import refused: %s", resultFirstText(res))
	}
	configs, _ := rt.posted[0]["configs"].([]any)
	if len(configs) != 1 || configs[0].(map[string]any)["id"] == "broker1" {
		t.Fatalf("configs = %v, want the broker copied under a fresh id", configs)
	}
}

// TestIssue370_DenylistJudgesTheWholeClipboard pins the documented policy:
// a denied type anywhere in the clipboard — even a member of a subflow the
// instance already has — refuses the import before the runtime is contacted.
func TestIssue370_DenylistJudgesTheWholeClipboard(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		http.Error(w, "unexpected", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	s := issue329Server(t, srv, "exec")
	res := importClipboard(t, s, `[
	  {"id":"t","type":"tab"},
	  {"id":"sf","type":"subflow","name":"S","in":[],"out":[]},
	  {"id":"e1","type":"exec","z":"sf","x":1,"y":1,"wires":[]},
	  {"id":"i1","type":"subflow:sf","z":"t","x":1,"y":1,"wires":[]}
	]`)
	assertIssue329Denied(t, res, "exec")
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 0 {
		t.Errorf("runtime contacted before the refusal: %v", seen)
	}
}
