package mcp

// Issue #337: export_flow and import_flow speak the real editor clipboard
// format — a flat array holding the tab and every node as separate
// elements — and importing a copy into the same instance never reuses an
// id the instance already has.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// fakeRuntime holds a flat configuration and answers the three requests the
// clipboard tools make: GET /flows, POST /flow and GET /flow/:id. POST /flow
// behaves like Node-RED's addFlow: it assigns the tab id, forces every
// member's z to it, and refuses a member id that already exists.
type fakeRuntime struct {
	mu     sync.Mutex
	flows  []map[string]any
	posted []map[string]any
	next   int
}

func newFakeRuntime(t *testing.T, flat string) (*fakeRuntime, *Server) {
	t.Helper()
	rt := &fakeRuntime{}
	if err := json.Unmarshal([]byte(flat), &rt.flows); err != nil {
		t.Fatalf("fixture is not a flat array: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(rt.serve))
	t.Cleanup(srv.Close)
	c, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return rt, New(c, Options{Version: "test"})
}

func (rt *fakeRuntime) serve(w http.ResponseWriter, r *http.Request) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	switch {
	case r.Method == "GET" && r.URL.Path == "/flows":
		_ = json.NewEncoder(w).Encode(map[string]any{"rev": "r1", "flows": rt.flows})
	case r.Method == "POST" && r.URL.Path == "/flow":
		var doc map[string]any
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		rt.posted = append(rt.posted, doc)
		rt.next++
		tabID := fmt.Sprintf("newtab%d", rt.next)
		added := []map[string]any{{"id": tabID, "type": "tab", "label": doc["label"]}}
		for _, key := range []string{"nodes", "configs"} {
			members, _ := doc[key].([]any)
			for _, m := range members {
				node := m.(map[string]any)
				if rt.hasID(node["id"]) {
					http.Error(w, `{"code":"unexpected_error","message":"Error: duplicate id"}`, http.StatusBadRequest)
					return
				}
				node["z"] = tabID
				added = append(added, node)
			}
		}
		rt.flows = append(rt.flows, added...)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": tabID})
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

func (rt *fakeRuntime) hasID(id any) bool {
	for _, n := range rt.flows {
		if n["id"] == id {
			return true
		}
	}
	return false
}

func (rt *fakeRuntime) lastPost(t *testing.T) map[string]any {
	t.Helper()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.posted) == 0 {
		t.Fatal("import_flow did not POST /flow")
	}
	return rt.posted[len(rt.posted)-1]
}

// instanceA carries tab1 with a wired pair, a tab-scoped config node, a
// global config node (with its own global dependency), a subflow instance,
// and a second tab plus an unrelated global config that must not leak.
const instanceA = `[
  {"id":"tab1","type":"tab","label":"Home","disabled":false,"info":""},
  {"id":"n1","type":"mqtt in","z":"tab1","name":"in","broker":"broker1","x":100,"y":100,"wires":[["n2"]]},
  {"id":"n2","type":"function","z":"tab1","name":"fn","func":"return msg;","x":300,"y":100,"wires":[["sfi1"]]},
  {"id":"sfi1","type":"subflow:sf1","z":"tab1","x":500,"y":100,"wires":[]},
  {"id":"cfgtab","type":"ui-base","z":"tab1","name":"scoped"},
  {"id":"broker1","type":"mqtt-broker","name":"broker","tls":"tls1"},
  {"id":"tls1","type":"tls-config","name":"tls"},
  {"id":"other","type":"mqtt-broker","name":"unused"},
  {"id":"sf1","type":"subflow","name":"SF","in":[{"x":40,"y":40,"wires":[{"id":"sfn1"}]}],"out":[]},
  {"id":"sfn1","type":"change","z":"sf1","x":200,"y":40,"wires":[]},
  {"id":"tab2","type":"tab","label":"Other"},
  {"id":"m1","type":"debug","z":"tab2","x":100,"y":100,"wires":[]}
]`

func exportClipboardArray(t *testing.T, s *Server, id string) []map[string]any {
	t.Helper()
	res, err := s.handleExportFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"id": id}},
	})
	if err != nil || res.IsError {
		t.Fatalf("export_flow failed: err=%v result=%v", err, res)
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(stripCodeFence(resultFirstText(res))), &arr); err != nil {
		t.Fatalf("export is not a flat JSON array of objects: %v", err)
	}
	return arr
}

func importClipboard(t *testing.T, s *Server, clipboard any) *mcp.CallToolResult {
	t.Helper()
	res, err := s.handleImportFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"clipboard": clipboard}},
	})
	if err != nil {
		t.Fatalf("handleImportFlow returned err=%v", err)
	}
	return res
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func idsOf(arr []map[string]any) []string {
	ids := make([]string, 0, len(arr))
	for _, n := range arr {
		ids = append(ids, fmt.Sprint(n["id"]))
	}
	return ids
}

// TestIssue337_ExportIsFlatEditorClipboard: the export is the flat array
// the editor produces — the tab first as its own element, its nodes as
// separate elements, plus the config nodes and subflow definitions they
// depend on (transitively), and nothing from other tabs.
func TestIssue337_ExportIsFlatEditorClipboard(t *testing.T) {
	_, s := newFakeRuntime(t, instanceA)
	arr := exportClipboardArray(t, s, "tab1")

	if arr[0]["id"] != "tab1" || arr[0]["type"] != "tab" {
		t.Fatalf("first element = %v, want the tab {id:tab1,type:tab}", arr[0])
	}
	if _, nested := arr[0]["nodes"]; nested {
		t.Errorf("tab element must not nest its nodes: %v", arr[0])
	}
	got := strings.Join(idsOf(arr), ",")
	want := "tab1,n1,n2,sfi1,cfgtab,broker1,tls1,sf1,sfn1"
	if got != want {
		t.Errorf("exported ids = %s, want %s", got, want)
	}
	for _, n := range arr[1:5] {
		if n["z"] != "tab1" {
			t.Errorf("tab member %v lost its z", n["id"])
		}
	}
}

// TestIssue337_ExportUnknownTab: exporting an id that is not a tab fails
// with a message naming it.
func TestIssue337_ExportUnknownTab(t *testing.T) {
	_, s := newFakeRuntime(t, instanceA)
	res, err := s.handleExportFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"id": "n1"}},
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !res.IsError || !strings.Contains(resultFirstText(res), `"n1"`) {
		t.Fatalf("want an error naming n1, got %v", res)
	}
}

// TestIssue337_ImportFlatTabAndNode is the audited reproduction: one tab
// and one comment node used to be refused as "clipboard has 2 tabs".
func TestIssue337_ImportFlatTabAndNode(t *testing.T) {
	rt, s := newFakeRuntime(t, `[]`)
	res := importClipboard(t, s, `[
	  {"id":"t1","type":"tab","label":"T"},
	  {"id":"c1","type":"comment","z":"t1","name":"note","x":100,"y":40,"wires":[]}
	]`)
	if res.IsError {
		t.Fatalf("import refused a one-tab clipboard: %s", resultFirstText(res))
	}
	if !strings.Contains(resultFirstText(res), "newtab1") {
		t.Errorf("response should name the new tab id: %s", resultFirstText(res))
	}
	post := rt.lastPost(t)
	if post["label"] != "T" {
		t.Errorf("posted label = %v, want T", post["label"])
	}
	nodes, _ := post["nodes"].([]any)
	if len(nodes) != 1 || nodes[0].(map[string]any)["id"] != "c1" {
		t.Errorf("posted nodes = %v, want exactly [c1]", nodes)
	}
}

// TestIssue337_ImportRefusesZeroOrManyTabs: the tab count is the number of
// "type":"tab" elements, not the array length; zero and two are refused
// before the runtime is contacted.
func TestIssue337_ImportRefusesZeroOrManyTabs(t *testing.T) {
	cases := map[string]struct{ clipboard, want string }{
		"no tab":   {`[{"id":"c1","type":"comment","z":"t1","x":1,"y":1}]`, "no tab"},
		"two tabs": {`[{"id":"a","type":"tab"},{"id":"c1","type":"comment","z":"a","x":1,"y":1},{"id":"b","type":"tab"}]`, "2 tabs"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rt, s := newFakeRuntime(t, `[]`)
			res := importClipboard(t, s, tc.clipboard)
			if !res.IsError || !strings.Contains(resultFirstText(res), tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, resultFirstText(res))
			}
			if len(rt.posted) != 0 {
				t.Error("a refused clipboard reached POST /flow")
			}
		})
	}
}

// TestIssue337_ImportCopyIntoSameInstanceRemintsIDs: re-importing an
// exported tab into the instance it came from used to collide on every
// node id. Every tab-owned id is reminted and every reference rewritten;
// the global dependencies the instance already has are reused, not copied.
func TestIssue337_ImportCopyIntoSameInstanceRemintsIDs(t *testing.T) {
	rt, s := newFakeRuntime(t, instanceA)
	exported := exportClipboardArray(t, s, "tab1")

	res := importClipboard(t, s, mustJSON(t, exported))
	if res.IsError {
		t.Fatalf("same-instance copy refused: %s", resultFirstText(res))
	}
	if !strings.Contains(resultFirstText(res), "reminted") {
		t.Errorf("response should say ids were reminted: %s", resultFirstText(res))
	}

	post := rt.lastPost(t)
	nodes, _ := post["nodes"].([]any)
	configs, _ := post["configs"].([]any)
	if len(nodes) != 3 || len(configs) != 1 {
		t.Fatalf("posted %d nodes and %d configs, want 3 and 1 (global deps reused): %v", len(nodes), len(configs), post)
	}
	byName := map[string]map[string]any{}
	for _, n := range nodes {
		m := n.(map[string]any)
		for _, old := range []string{"n1", "n2", "sfi1", "cfgtab"} {
			if m["id"] == old {
				t.Errorf("node kept the existing id %s", old)
			}
		}
		byName[fmt.Sprint(m["type"])] = m
	}
	in, fn, sfi := byName["mqtt in"], byName["function"], byName["subflow:sf1"]
	if in["broker"] != "broker1" {
		t.Errorf("reference to the reused global config changed: %v", in["broker"])
	}
	if w := in["wires"].([]any)[0].([]any)[0]; w != fn["id"] {
		t.Errorf("n1 wire = %v, want the new id of n2 (%v)", w, fn["id"])
	}
	if w := fn["wires"].([]any)[0].([]any)[0]; w != sfi["id"] {
		t.Errorf("n2 wire = %v, want the new id of sfi1 (%v)", w, sfi["id"])
	}
	if c := configs[0].(map[string]any); c["id"] == "cfgtab" || c["type"] != "ui-base" {
		t.Errorf("tab config = %v, want a reminted ui-base", c)
	}
}

// TestIssue337_ImportIntoOtherInstanceKeepsIDs: a clipboard whose ids do
// not exist on the target keeps them, so an editor → MCP → editor trip
// is lossless; a global config the target lacks travels with the tab.
func TestIssue337_ImportIntoOtherInstanceKeepsIDs(t *testing.T) {
	_, a := newFakeRuntime(t, instanceA)
	exported := exportClipboardArray(t, a, "tab1")

	// Instance B already has the subflow, nothing else.
	rtB, b := newFakeRuntime(t, `[
	  {"id":"sf1","type":"subflow","name":"SF","in":[],"out":[]},
	  {"id":"sfn1","type":"change","z":"sf1","x":200,"y":40,"wires":[]}
	]`)
	res := importClipboard(t, b, mustJSON(t, exported))
	if res.IsError {
		t.Fatalf("cross-instance import refused: %s", resultFirstText(res))
	}
	if strings.Contains(resultFirstText(res), "reminted") {
		t.Errorf("no id collided, nothing should be reminted: %s", resultFirstText(res))
	}
	post := rtB.lastPost(t)
	var posted []string
	for _, key := range []string{"nodes", "configs"} {
		members, _ := post[key].([]any)
		for _, m := range members {
			posted = append(posted, fmt.Sprint(m.(map[string]any)["id"]))
		}
	}
	if got, want := strings.Join(posted, ","), "n1,n2,sfi1,cfgtab,broker1,tls1"; got != want {
		t.Errorf("posted ids = %s, want %s", got, want)
	}

	// MCP → editor: B's export carries the same nodes and wires.
	again := exportClipboardArray(t, b, "newtab1")
	byID := map[string]map[string]any{}
	for _, n := range again {
		byID[fmt.Sprint(n["id"])] = n
	}
	for _, id := range []string{"n1", "n2", "sfi1", "cfgtab", "broker1", "tls1", "sf1", "sfn1"} {
		if byID[id] == nil {
			t.Errorf("re-export lost %s", id)
		}
	}
	if w := byID["n1"]["wires"].([]any)[0].([]any)[0]; w != "n2" {
		t.Errorf("re-exported n1 wire = %v, want n2", w)
	}
}

// TestIssue337_ImportRefusesMissingSubflow: a subflow definition the
// target does not have cannot be created by POST /flow; the import is
// refused with the step to take, and nothing is written.
func TestIssue337_ImportRefusesMissingSubflow(t *testing.T) {
	_, a := newFakeRuntime(t, instanceA)
	exported := exportClipboardArray(t, a, "tab1")

	rtB, b := newFakeRuntime(t, `[]`)
	res := importClipboard(t, b, mustJSON(t, exported))
	if !res.IsError {
		t.Fatal("import of a tab using an absent subflow should be refused")
	}
	if text := resultFirstText(res); !strings.Contains(text, `"sf1"`) || !strings.Contains(text, "create_subflow") {
		t.Errorf("error should name sf1 and create_subflow: %s", text)
	}
	if len(rtB.posted) != 0 {
		t.Error("a refused import reached POST /flow")
	}
}
