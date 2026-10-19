package nodered

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

const orderedConfig = `[
 {"id":"a","type":"tab","label":"A","env":[{"name":"KEEP","value":"yes"}],"unknown":{"keep":true}},
 {"id":"n","type":"comment","z":"a","x":1,"y":2,"name":"before","credentials":{"secret":"retained"},"unknown":42},
 {"id":"cfg","type":"broker","z":"a","credentials":{"secret":"config"}},
 {"id":"b","type":"tab","label":"B"},
 {"id":"other","type":"debug","z":"b","unknown":[1,2]},
 {"id":"global","type":"broker","unknown":"global"},
 {"id":"sf","type":"subflow","name":"Existing","in":[],"out":[]},
 {"id":"sfn","type":"debug","z":"sf","x":1,"y":1},
 {"id":"c","type":"tab","label":"C"}
]`

// orderedRuntime models the lossy nested GET and filter/concat PUT used by
// Node-RED. The full-array endpoint enforces the real v2 revision contract.
func orderedRuntime(t *testing.T, conflict bool) (*Client, *RawFlow, *int, *int) {
	t.Helper()
	state := RawFlow(orderedConfig)
	reads, writes := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/a":
			_, _ = io.WriteString(w, `{"id":"a","label":"A","nodes":[{"id":"n","type":"comment","z":"a","x":1,"y":2,"name":"before","unknown":42}],"configs":[{"id":"cfg","type":"broker","z":"a"}]}`)
		case r.Method == "GET" && r.URL.Path == "/flows":
			reads++
			if r.Header.Get("Node-RED-API-Version") == "v2" {
				_, _ = io.WriteString(w, `{"rev":"current","flows":`+string(state)+`}`)
			} else {
				_, _ = w.Write(state)
			}
		case r.Method == "PUT" && r.URL.Path == "/flow/a":
			writes++
			var doc struct{ Nodes, Configs []json.RawMessage }
			_ = json.NewDecoder(r.Body).Decode(&doc)
			var next []json.RawMessage
			for _, raw := range FlowArray(state) {
				var m map[string]json.RawMessage
				_ = json.Unmarshal(raw, &m)
				if stringField(m, "id") != "a" && stringField(m, "z") != "a" {
					next = append(next, raw)
				}
			}
			next = append(next, json.RawMessage(`{"id":"a","type":"tab","label":"A"}`))
			next = append(next, doc.Nodes...)
			next = append(next, doc.Configs...)
			state = RawFlow(rawSlice(next))
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "POST" && r.URL.Path == "/flows":
			writes++
			flows := readRevisionedDeployment(t, r)
			if conflict {
				http.Error(w, `{"code":"version_mismatch"}`, http.StatusConflict)
				return
			}
			state = RawFlow(rawSlice(flows))
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	t.Cleanup(srv.Close)
	return clientWithBackup(t, srv.URL), &state, &reads, &writes
}

func readRevisionedDeployment(t *testing.T, r *http.Request) []json.RawMessage {
	t.Helper()
	if r.Header.Get("Node-RED-API-Version") != "v2" || r.Header.Get("Node-RED-Deployment-Type") != "flows" {
		t.Error("deployment must use v2 and flows headers")
	}
	var body struct {
		Rev   string
		Flows []json.RawMessage
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Error(err)
	}
	if body.Rev != "current" {
		t.Errorf("rev = %q, want current", body.Rev)
	}
	return body.Flows
}

// testRevisionedTab creates a realistic flat API v2 fixture from a nested tab.
func testRevisionedTab(t *testing.T, nested string) []byte {
	t.Helper()
	var tab map[string]json.RawMessage
	if err := json.Unmarshal([]byte(nested), &tab); err != nil {
		t.Fatal(err)
	}
	var entries []json.RawMessage
	for _, key := range []string{"nodes", "configs"} {
		var members []json.RawMessage
		_ = json.Unmarshal(tab[key], &members)
		entries = append(entries, members...)
		delete(tab, key)
	}
	tab["type"] = json.RawMessage(`"tab"`)
	top, _ := json.Marshal(tab)
	all := append([]json.RawMessage{top}, entries...)
	body, _ := json.Marshal(map[string]any{"rev": "r", "flows": all})
	return body
}

// testNestedDeployment lets the fake runtime serve a subsequent nested GET.
func testNestedDeployment(t *testing.T, body []byte, id string) []byte {
	t.Helper()
	var tab map[string]json.RawMessage
	var nodes, configs []json.RawMessage
	for _, raw := range FlowArray(body) {
		var m map[string]json.RawMessage
		_ = json.Unmarshal(raw, &m)
		if stringField(m, "id") == id {
			tab = m
			delete(tab, "type")
		}
		if stringField(m, "z") != id {
			continue
		}
		if _, ok := m["x"]; ok {
			nodes = append(nodes, raw)
		} else {
			configs = append(configs, raw)
		}
	}
	tab["nodes"] = rawSlice(nodes)
	if len(configs) > 0 {
		tab["configs"] = rawSlice(configs)
	}
	out, _ := json.Marshal(tab)
	return out
}

func TestUpdateNodePreservesTabOrder(t *testing.T) {
	c, state, reads, writes := orderedRuntime(t, false)
	if err := c.UpdateNode(context.Background(), "a", "n", map[string]json.RawMessage{"name": json.RawMessage(`"after"`)}); err != nil {
		t.Fatal(err)
	}
	want := RawFlow(orderedConfig)
	var entries []map[string]any
	_ = json.Unmarshal(want, &entries)
	entries[1]["name"] = "after"
	var got []map[string]any
	_ = json.Unmarshal(*state, &got)
	if !reflect.DeepEqual(got, entries) {
		t.Fatalf("update changed more than name (including tab order/metadata):\n%s", *state)
	}
	// Unchanged entries retain their raw JSON field order, rather than being
	// rebuilt from the runtime's lossy nested representation.
	before, after := FlowArray(RawFlow(orderedConfig)), FlowArray(*state)
	for _, index := range []int{0, 2, 3, 4, 5, 6, 7, 8} {
		if !containsJSON(after[index], string(compactTestJSON(t, before[index]))) {
			t.Errorf("unchanged entry %d was reconstructed: %s", index, after[index])
		}
	}
	if *reads != 1 || *writes != 1 {
		t.Errorf("reads/writes = %d/%d, want 1/1", *reads, *writes)
	}
	backups, err := c.ListBackups()
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, %v", backups, err)
	}
	raw, err := c.ReadBackup(backups[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	var saved, initial any
	_ = json.Unmarshal(raw, &saved)
	_ = json.Unmarshal([]byte(`{"rev":"current","flows":`+orderedConfig+`}`), &initial)
	if !reflect.DeepEqual(saved, initial) {
		t.Errorf("backup differs from revision used: %s", raw)
	}
}

func compactTestJSON(t *testing.T, raw []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// Full UpdateFlow submissions replace node properties. Omitted operational
// fields must not be restored from the snapshot after wire validation.
func TestUpdateFlowReplacesOperationalFields(t *testing.T) {
	c, state, reads, writes := orderedRuntime(t, false)
	initial := RawFlow(`[
		{"id":"a","type":"tab","label":"A","unknown":{"keep":true}},
		{"id":"n","type":"inject","z":"a","x":1,"y":2,"name":"before","wires":[["gone"]],"repeat":"5","once":true,"custom":"remove","credentials":{"secret":"retained"}},
		{"id":"gone","type":"debug","z":"a","x":3,"y":4},
		{"id":"cfg","type":"broker","z":"a","name":"old","custom":"remove","credentials":{"secret":"config"}},
		{"id":"b","type":"tab","label":"B"},
		{"id":"global","type":"broker","unknown":"global"},
		{"id":"sf","type":"subflow","name":"Existing","in":[],"out":[]},
		{"id":"sfn","type":"debug","z":"sf","x":1,"y":1},
		{"id":"c","type":"tab","label":"C"}
	]`)
	*state = initial
	submitted := RawFlow(`{"id":"a","nodes":[{"id":"n","type":"inject","z":"a","x":1,"y":2,"name":"after"}],"configs":[{"id":"cfg","type":"broker","z":"a"}]}`)
	if err := c.UpdateFlow(context.Background(), "a", submitted); err != nil {
		t.Fatal(err)
	}
	deployed, err := c.ListFlows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var want, got []map[string]any
	_ = json.Unmarshal(initial, &want)
	want = append(want[:2], want[3:]...)
	want[1] = map[string]any{"id": "n", "type": "inject", "z": "a", "x": float64(1), "y": float64(2), "name": "after", "credentials": map[string]any{"secret": "retained"}}
	want[2] = map[string]any{"id": "cfg", "type": "broker", "z": "a", "credentials": map[string]any{"secret": "config"}}
	_ = json.Unmarshal(deployed, &got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("replacement resurrected omitted fields or changed unrelated entries:\n%s", deployed)
	}
	if *reads != 2 || *writes != 1 {
		t.Errorf("reads/writes = %d/%d, want 2/1", *reads, *writes)
	}
	backups, err := c.ListBackups()
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, %v", backups, err)
	}
	backup, err := c.ReadBackup(backups[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	var saved []map[string]any
	_ = json.Unmarshal(rawSlice(FlowArray(backup)), &saved)
	var before []map[string]any
	_ = json.Unmarshal(initial, &before)
	if !reflect.DeepEqual(saved, before) {
		t.Errorf("backup does not preserve original wires and removed node: %s", backup)
	}
}

func TestUpdateFlowPreservesNodeZReferences(t *testing.T) {
	c, _, _, _ := orderedRuntime(t, false)
	submitted := RawFlow(`{"id":"a","nodes":[{"id":"n","type":"inject","z":"a","x":1,"y":2,"wires":[["child"]]},{"id":"child","type":"debug","z":"n","x":3,"y":4}]}`)
	if err := c.UpdateFlow(context.Background(), "a", submitted); err != nil {
		t.Fatalf("valid node-owned z reference rejected: %v", err)
	}
	deployed, err := c.ListFlows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var child map[string]any
	for _, raw := range FlowArray(deployed) {
		var node map[string]any
		_ = json.Unmarshal(raw, &node)
		if node["id"] == "child" {
			child = node
		}
	}
	if child["z"] != "n" {
		t.Errorf("node-owned z reference changed: %s", deployed)
	}
}

func TestUpdateFlowRevisionConflict(t *testing.T) {
	c, state, reads, writes := orderedRuntime(t, true)
	err := c.UpdateFlow(context.Background(), "a", RawFlow(`{"id":"a","nodes":[{"id":"n","type":"comment","z":"a","name":"after"}],"configs":[{"id":"cfg","type":"broker","z":"a"}]}`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 409 || apiErr.Method != "POST" || apiErr.Path != "/flows" {
		t.Fatalf("want POST /flows 409, got %v", err)
	}
	if string(*state) != orderedConfig || *reads != 1 || *writes != 1 {
		t.Errorf("conflict changed state or retried: reads=%d writes=%d state=%s", *reads, *writes, *state)
	}
	backups, err := c.ListBackups()
	if err != nil || len(backups) != 1 {
		t.Fatalf("conflict backup = %v, %v", backups, err)
	}
}

func TestSharedFlowEditsPreserveOtherEntries(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(*Client) error
		check func(*testing.T, []map[string]any)
	}{
		{"add", func(c *Client) error {
			return c.AddNode(context.Background(), "a", json.RawMessage(`{"id":"new","type":"debug","z":"a","x":3,"y":4}`))
		}, func(t *testing.T, items []map[string]any) {
			if items[len(items)-1]["id"] != "new" {
				t.Error("new node not deployed")
			}
		}},
		{"delete", func(c *Client) error { return c.DeleteNode(context.Background(), "a", "n") }, func(t *testing.T, items []map[string]any) {
			for _, m := range items {
				if m["id"] == "n" {
					t.Error("deleted node remains")
				}
			}
		}},
		{"connect", func(c *Client) error { return c.ConnectNodes(context.Background(), "a", "n", 0, "cfg") }, func(t *testing.T, items []map[string]any) {
			if !reflect.DeepEqual(items[1]["wires"], []any{[]any{"cfg"}}) {
				t.Errorf("wires=%v", items[1]["wires"])
			}
		}},
		{"disabled", func(c *Client) error { return c.SetFlowDisabled(context.Background(), "a", true) }, func(t *testing.T, items []map[string]any) {
			if items[0]["disabled"] != true {
				t.Error("tab not disabled")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, state, reads, writes := orderedRuntime(t, false)
			if err := tt.edit(c); err != nil {
				t.Fatal(err)
			}
			var got []map[string]any
			_ = json.Unmarshal(*state, &got)
			tt.check(t, got)
			assertUnaffectedEntries(t, got)
			var tabs []any
			for _, m := range got {
				if m["type"] == "tab" {
					tabs = append(tabs, m["id"])
				}
			}
			if !reflect.DeepEqual(tabs, []any{"a", "b", "c"}) || *reads != 1 || *writes != 1 {
				t.Errorf("tabs=%v reads=%d writes=%d", tabs, *reads, *writes)
			}
		})
	}
}

func assertUnaffectedEntries(t *testing.T, got []map[string]any) {
	t.Helper()
	var initial, unaffected []map[string]any
	_ = json.Unmarshal([]byte(orderedConfig), &initial)
	for _, m := range got {
		if m["id"] != "a" && m["z"] != "a" {
			unaffected = append(unaffected, m)
		}
	}
	if !reflect.DeepEqual(unaffected, initial[3:]) {
		t.Errorf("unaffected entries changed: %v", got)
	}
}

func TestUpdateFlowNestedSubflowsAndConfigs(t *testing.T) {
	c, state, _, _ := orderedRuntime(t, false)
	body := RawFlow(`{"id":"a","label":"Renamed","nodes":[{"id":"n","type":"comment","z":"a","name":"after"}],"configs":[{"id":"cfg","type":"broker","name":"changed"}],"subflows":[{"id":"sf","type":"subflow","name":"Updated","in":[],"out":[],"nodes":[{"id":"sfn","type":"debug","x":1,"y":1,"name":"inner"}],"configs":[{"id":"sfc","type":"broker","custom":true}]}]}`)
	if err := c.UpdateFlow(context.Background(), "a", body); err != nil {
		t.Fatal(err)
	}
	var entries []map[string]any
	_ = json.Unmarshal(*state, &entries)
	byID := map[string]map[string]any{}
	for _, m := range entries {
		byID[m["id"].(string)] = m
	}
	if byID["a"]["label"] != "Renamed" || byID["cfg"]["name"] != "changed" || byID["cfg"]["z"] != "a" || byID["sf"]["name"] != "Updated" || byID["sfn"]["name"] != "inner" || byID["sfn"]["z"] != "sf" || byID["sfc"]["z"] != "sf" {
		t.Errorf("nested submission lost: %s", *state)
	}
	if byID["cfg"]["credentials"] == nil || byID["global"]["unknown"] != "global" || byID["a"]["unknown"] == nil {
		t.Errorf("existing metadata lost: %s", *state)
	}
	if byID["sf"]["nodes"] != nil || byID["a"]["subflows"] != nil {
		t.Errorf("nested collections not flattened: %s", *state)
	}
}

func TestUpdateFlowRejectsInvalidSubmission(t *testing.T) {
	for _, body := range []string{
		`null`,
		`{"id":"different","nodes":[]}`,
		`{"nodes":[],"configs":[null]}`,
		`{"nodes":[],"configs":[{"id":"cfg"}]}`,
		`{"nodes":[{"id":"other","type":"debug"}]}`,
		`{"nodes":[{"id":"n","type":"comment"},{"id":"n","type":"comment"}]}`,
		`{"nodes":[],"subflows":"invalid"}`,
	} {
		t.Run(body, func(t *testing.T) {
			c, state, _, writes := orderedRuntime(t, false)
			if err := c.UpdateFlow(context.Background(), "a", RawFlow(body)); err == nil {
				t.Error("invalid submission accepted")
			}
			backups, err := c.ListBackups()
			if err != nil || len(backups) != 0 || *writes != 0 || string(*state) != orderedConfig {
				t.Errorf("rejected submission changed state: writes=%d backups=%v err=%v", *writes, backups, err)
			}
		})
	}
}

func TestUpdateFlowRequiresRevisionAndTarget(t *testing.T) {
	for _, body := range []string{`[]`, `{"flows":[]}`, `{"rev":"r","flows":null}`, `{"rev":"r","flows":[null]}`, `{"rev":"r","flows":[{"id":"b","type":"tab"}]}`} {
		t.Run(body, func(t *testing.T) {
			writes := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					writes++
					w.WriteHeader(204)
					return
				}
				if r.Header.Get("Node-RED-API-Version") != "v2" {
					t.Error("missing v2 request")
				}
				_, _ = io.WriteString(w, body)
			}))
			defer srv.Close()
			c := clientWithBackup(t, srv.URL)
			if err := c.UpdateFlow(context.Background(), "a", RawFlow(`{"nodes":[]}`)); err == nil {
				t.Error("invalid snapshot accepted")
			}
			backups, err := c.ListBackups()
			if err != nil || len(backups) != 0 || writes != 0 {
				t.Errorf("rejected state: writes=%d backups=%v err=%v", writes, backups, err)
			}
		})
	}
}
