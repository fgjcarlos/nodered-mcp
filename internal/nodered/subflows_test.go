package nodered

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// globalSubflowsBody is the shape /flow/global returns. Reused as
// the "before" snapshot in tests that exercise the fetch-modify-PUT
// pattern, so the tests can read whatever is in the body's
// subflows/configs and confirm the helper preserves them.
const globalSubflowsBody = `{
	"id":"global",
	"subflows":[
		{"id":"sf_a","type":"subflow","name":"A","in":[],"out":[],"env":[],"nodes":[]}
	],
	"configs":[
		{"id":"cfg_x","type":"some-config","name":"X"}
	]
}`

// emptyGlobalSubflowsBody is the body for a fresh instance with no
// subflows defined yet.
const emptyGlobalSubflowsBody = `{"id":"global"}`

// TestListSubflows_Success covers the happy path: a global envelope
// with one subflow, and the helper extracts it as a one-element
// slice without dropping fields.
func TestListSubflows_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/flow/global" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(globalSubflowsBody))
	}))
	t.Cleanup(srv.Close)

	c, _ := newTestClient(t, nil)
	c.baseURL = srv.URL

	list, err := c.ListSubflows(context.Background())
	if err != nil {
		t.Fatalf("ListSubflows: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 subflow, got %d", len(list))
	}
	// The subflow item must preserve the original fields.
	if !strings.Contains(string(list[0]), `"id":"sf_a"`) {
		t.Errorf("subflow missing id field: %s", list[0])
	}
	if !strings.Contains(string(list[0]), `"type":"subflow"`) {
		t.Errorf("subflow missing type field: %s", list[0])
	}
}

// TestListSubflows_Empty covers a fresh instance: the envelope has no
// subflows key, the helper returns an empty (non-nil) slice so the
// caller can iterate without nil-checking.
func TestListSubflows_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(emptyGlobalSubflowsBody))
	}))
	t.Cleanup(srv.Close)

	c, _ := newTestClient(t, nil)
	c.baseURL = srv.URL

	list, err := c.ListSubflows(context.Background())
	if err != nil {
		t.Fatalf("ListSubflows: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 subflows, got %d", len(list))
	}
}

// TestGetSubflow_Hit returns the matching subflow from the global
// envelope, with every field preserved.
func TestGetSubflow_Hit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(globalSubflowsBody))
	}))
	t.Cleanup(srv.Close)

	c, _ := newTestClient(t, nil)
	c.baseURL = srv.URL

	raw, err := c.GetSubflow(context.Background(), "sf_a")
	if err != nil {
		t.Fatalf("GetSubflow: %v", err)
	}
	if !strings.Contains(string(raw), `"id":"sf_a"`) {
		t.Errorf("expected id in body, got %s", raw)
	}
	if !strings.Contains(string(raw), `"name":"A"`) {
		t.Errorf("expected name in body, got %s", raw)
	}
}

// TestGetSubflow_Miss surfaces a 404-shaped error so callers can
// distinguish "no such subflow" from "transient lookup failure".
func TestGetSubflow_Miss(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(globalSubflowsBody))
	}))
	t.Cleanup(srv.Close)

	c, _ := newTestClient(t, nil)
	c.baseURL = srv.URL

	_, err := c.GetSubflow(context.Background(), "sf_missing")
	if err == nil {
		t.Fatal("expected error for missing subflow, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
		t.Errorf("expected *APIError 404, got %T: %v", err, err)
	}
}

// TestGetSubflow_EmptyID is the validation guard.
func TestGetSubflow_EmptyID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be called for empty id")
	}))
	t.Cleanup(srv.Close)

	c, _ := newTestClient(t, nil)
	c.baseURL = srv.URL

	if _, err := c.GetSubflow(context.Background(), ""); err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

// TestCreateSubflow_Appends covers the fetch-modify-PUT path: the
// helper reads the current subflows, appends, and writes back. The
// mock verifies that the PUT body contains both the existing
// subflow and the new one (so the write does not silently nuke the
// existing set), and that the response echoes the new subflow.
func TestCreateSubflow_Appends(t *testing.T) {
	var (
		mu        sync.Mutex
		putBody   []byte
		getCalled int
		putCalled int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/flow/global":
			if r.Method == "GET" {
				getCalled++
				_, _ = w.Write([]byte(globalSubflowsBody))
				return
			}
			if r.Method == "PUT" {
				putCalled++
				putBody, _ = io.ReadAll(r.Body)
				// Echo the new subflow so the caller's response
				// has a valid shape.
				var sent struct {
					Subflows []json.RawMessage `json:"subflows"`
				}
				_ = json.Unmarshal(putBody, &sent)
				out, _ := json.Marshal(sent.Subflows[len(sent.Subflows)-1])
				_, _ = w.Write(out)
				return
			}
		case "/flows":
			// Backup snapshot fetch. Serve a minimal flat array
			// so snapshotFlows has something to write to disk.
			_, _ = w.Write([]byte(`[{"id":"placeholder","type":"tab","label":"x","nodes":[]}]`))
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)

	newSF := json.RawMessage(`{"id":"sf_b","type":"subflow","name":"B","in":[],"out":[],"env":[],"nodes":[]}`)
	created, err := c.CreateSubflow(context.Background(), newSF)
	if err != nil {
		t.Fatalf("CreateSubflow: %v", err)
	}
	if !strings.Contains(string(created), `"id":"sf_b"`) {
		t.Errorf("expected new subflow id in response, got %s", created)
	}
	if getCalled != 1 {
		t.Errorf("expected 1 GET /flow/global (the read step), got %d", getCalled)
	}
	if putCalled != 1 {
		t.Errorf("expected 1 PUT /flow/global, got %d", putCalled)
	}
	// The PUT body must carry BOTH the existing and the new subflow:
	// the helper's contract is that create does not nuke what's
	// there. Both ids must be present.
	if !strings.Contains(string(putBody), `"id":"sf_a"`) {
		t.Errorf("PUT body dropped the existing subflow: %s", putBody)
	}
	if !strings.Contains(string(putBody), `"id":"sf_b"`) {
		t.Errorf("PUT body missing the new subflow: %s", putBody)
	}
}

// TestCreateSubflow_DuplicateID fails closed: a subflow with the
// same id is already there, so the helper refuses and surfaces a
// clear error rather than silently overwriting.
func TestCreateSubflow_DuplicateID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/flow/global" && r.Method == "GET" {
			_, _ = w.Write([]byte(globalSubflowsBody))
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	dup := json.RawMessage(`{"id":"sf_a","type":"subflow","name":"A2","in":[],"out":[],"env":[],"nodes":[]}`)
	_, err := c.CreateSubflow(context.Background(), dup)
	if err == nil {
		t.Fatal("expected duplicate-id error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention the existing id, got %v", err)
	}
}

// TestCreateSubflow_RejectsEmptyID covers the validation guard.
func TestCreateSubflow_RejectsEmptyID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be called for invalid subflow")
	}))
	t.Cleanup(srv.Close)

	c, _ := newTestClient(t, nil)
	c.baseURL = srv.URL

	_, err := c.CreateSubflow(context.Background(), json.RawMessage(`{"name":"oops"}`))
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

// TestUpdateSubflow_Replaces covers the fetch-modify-PUT path: the
// helper reads the current subflows, swaps the named one, and
// writes back. The PUT body must carry the new content under the
// same id and keep every other subflow.
func TestUpdateSubflow_Replaces(t *testing.T) {
	var putBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flow/global":
			if r.Method == "GET" {
				_, _ = w.Write([]byte(globalSubflowsBody))
				return
			}
			if r.Method == "PUT" {
				putBody, _ = io.ReadAll(r.Body)
				_, _ = w.Write([]byte(`{"id":"sf_a"}`))
				return
			}
		case "/flows":
			_, _ = w.Write([]byte(`[{"id":"placeholder","type":"tab","label":"x","nodes":[]}]`))
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)

	updated := json.RawMessage(`{"id":"sf_a","type":"subflow","name":"A-renamed","in":[],"out":[],"env":[],"nodes":[]}`)
	if err := c.UpdateSubflow(context.Background(), "sf_a", updated); err != nil {
		t.Fatalf("UpdateSubflow: %v", err)
	}
	if !strings.Contains(string(putBody), `"name":"A-renamed"`) {
		t.Errorf("PUT body did not carry the updated name: %s", putBody)
	}
	if !strings.Contains(string(putBody), `"id":"sf_a"`) {
		t.Errorf("PUT body lost the id: %s", putBody)
	}
}

// TestUpdateSubflow_NotFound surfaces a 404-shaped error.
func TestUpdateSubflow_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/flow/global" && r.Method == "GET" {
			_, _ = w.Write([]byte(globalSubflowsBody))
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	body := json.RawMessage(`{"id":"sf_missing","type":"subflow","name":"X","in":[],"out":[],"env":[],"nodes":[]}`)
	err := c.UpdateSubflow(context.Background(), "sf_missing", body)
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
		t.Errorf("expected *APIError 404, got %T: %v", err, err)
	}
}

// TestUpdateSubflow_BodyIDMismatch refuses a body whose id does not
// match the path id; the editor does the same, and the runtime
// would otherwise see two definitions with different ids.
func TestUpdateSubflow_BodyIDMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/flow/global" && r.Method == "GET" {
			_, _ = w.Write([]byte(globalSubflowsBody))
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	body := json.RawMessage(`{"id":"sf_other","type":"subflow","name":"X","in":[],"out":[],"env":[],"nodes":[]}`)
	err := c.UpdateSubflow(context.Background(), "sf_a", body)
	if err == nil {
		t.Fatal("expected id-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "id in body") {
		t.Errorf("error should explain the mismatch, got %v", err)
	}
}

// TestDeleteSubflow_Removes covers the fetch-modify-PUT path: the
// helper reads, drops the named entry, and writes back the rest.
// The PUT body must NOT carry the deleted id.
func TestDeleteSubflow_Removes(t *testing.T) {
	var putBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flow/global":
			if r.Method == "GET" {
				_, _ = w.Write([]byte(globalSubflowsBody))
				return
			}
			if r.Method == "PUT" {
				putBody, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusNoContent)
				return
			}
		case "/flows":
			_, _ = w.Write([]byte(`[{"id":"placeholder","type":"tab","label":"x","nodes":[]}]`))
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	if err := c.DeleteSubflow(context.Background(), "sf_a"); err != nil {
		t.Fatalf("DeleteSubflow: %v", err)
	}
	if strings.Contains(string(putBody), `"id":"sf_a"`) {
		t.Errorf("PUT body still carries the deleted subflow: %s", putBody)
	}
}

// TestDeleteSubflow_NotFound surfaces a 404-shaped error.
func TestDeleteSubflow_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/flow/global" && r.Method == "GET" {
			_, _ = w.Write([]byte(globalSubflowsBody))
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	err := c.DeleteSubflow(context.Background(), "sf_missing")
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
		t.Errorf("expected *APIError 404, got %T: %v", err, err)
	}
}

// TestInstantiateSubflow_HappyPath: the helper reads the target
// tab, adds an instance node of type "subflow:<id>" with the
// supplied x/y/name, and writes the tab back. Verifies the final
// PUT body carries the instance node AND preserves the rest of
// the tab.
func TestInstantiateSubflow_HappyPath(t *testing.T) {
	var (
		mu       sync.Mutex
		putBody  []byte
		getPath  string
		putPath  string
		flowBody = []byte(`{
			"id":"tab_x","label":"X","nodes":[
				{"id":"n1","type":"inject","z":"tab_x","x":140,"y":140,"wires":[[]]}
			]
		}`)
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			// subflow lookup: present, so InstantiateSubflow
			// does not bail at the GetSubflow precheck.
			_, _ = w.Write([]byte(globalSubflowsBody))
		case r.Method == "GET" && r.URL.Path == "/flow/tab_x":
			getPath = r.URL.Path
			_, _ = w.Write(flowBody)
		case r.Method == "PUT" && r.URL.Path == "/flow/tab_x":
			putPath = r.URL.Path
			putBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/flows":
			// Backup snapshot for UpdateFlow's safety net.
			_, _ = w.Write([]byte(`[` + string(flowBody) + `]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)

	params := json.RawMessage(`{"id":"inst_1","name":"first","x":300,"y":300,"wires":[[]]}`)
	created, err := c.InstantiateSubflow(context.Background(), "tab_x", "sf_a", params)
	if err != nil {
		t.Fatalf("InstantiateSubflow: %v", err)
	}
	if getPath != "/flow/tab_x" {
		t.Errorf("expected GET /flow/tab_x for the read step, got %q", getPath)
	}
	if putPath != "/flow/tab_x" {
		t.Errorf("expected PUT /flow/tab_x for the write, got %q", putPath)
	}
	// The returned node must carry the runtime-required fields set
	// by the helper regardless of the caller's payload.
	if !strings.Contains(string(created), `"type":"subflow:sf_a"`) {
		t.Errorf("instance missing the subflow:<id> type: %s", created)
	}
	if !strings.Contains(string(created), `"z":"tab_x"`) {
		t.Errorf("instance missing the owning tab z: %s", created)
	}
	// The PUT body must preserve the existing node (n1) and add the
	// new instance.
	if !strings.Contains(string(putBody), `"id":"n1"`) {
		t.Errorf("PUT body lost the existing node: %s", putBody)
	}
	if !strings.Contains(string(putBody), `"id":"inst_1"`) {
		t.Errorf("PUT body missing the new instance: %s", putBody)
	}
	if !strings.Contains(string(putBody), `"type":"subflow:sf_a"`) {
		t.Errorf("PUT body missing the subflow:<id> type: %s", putBody)
	}
}

// TestInstantiateSubflow_NoParams covers the no-params path: the
// helper still produces a valid instance with the runtime-required
// fields filled in and sensible defaults for x/y.
func TestInstantiateSubflow_NoParams(t *testing.T) {
	var putBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			_, _ = w.Write([]byte(globalSubflowsBody))
		case r.Method == "GET" && r.URL.Path == "/flow/tab_x":
			_, _ = w.Write([]byte(`{"id":"tab_x","label":"X","nodes":[]}`))
		case r.Method == "PUT" && r.URL.Path == "/flow/tab_x":
			putBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[{"id":"tab_x","type":"tab","label":"X","nodes":[]}]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	if _, err := c.InstantiateSubflow(context.Background(), "tab_x", "sf_a", nil); err != nil {
		t.Fatalf("InstantiateSubflow: %v", err)
	}
	// Default canvas coords must be present, otherwise the runtime
	// files the node into configs and the instance never deploys.
	if !strings.Contains(string(putBody), `"x":140`) {
		t.Errorf("PUT body missing default x coord: %s", putBody)
	}
	if !strings.Contains(string(putBody), `"y":140`) {
		t.Errorf("PUT body missing default y coord: %s", putBody)
	}
	if !strings.Contains(string(putBody), `"type":"subflow:sf_a"`) {
		t.Errorf("PUT body missing the subflow:<id> type: %s", putBody)
	}
}

// TestInstantiateSubflow_UnknownSubflow pre-flights the subflow
// existence check: a missing subflow must fail the call BEFORE the
// target tab is read, so a typo from the caller is loud and the
// tab is not touched.
func TestInstantiateSubflow_UnknownSubflow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/flow/global" && r.Method == "GET" {
			_, _ = w.Write([]byte(globalSubflowsBody))
			return
		}
		t.Errorf("unexpected %s %s — should have bailed before any other call", r.Method, r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	_, err := c.InstantiateSubflow(context.Background(), "tab_x", "sf_missing", nil)
	if err == nil {
		t.Fatal("expected error for missing subflow, got nil")
	}
}

// TestInstantiateSubflow_RejectsEmptyIDs covers the validation
// guards on both flow_id and subflow_id.
func TestInstantiateSubflow_RejectsEmptyIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be called for empty ids")
	}))
	t.Cleanup(srv.Close)

	c, _ := newTestClient(t, nil)
	c.baseURL = srv.URL

	if _, err := c.InstantiateSubflow(context.Background(), "", "sf_a", nil); err == nil {
		t.Error("expected validation error for empty flow id")
	}
	if _, err := c.InstantiateSubflow(context.Background(), "tab_x", "", nil); err == nil {
		t.Error("expected validation error for empty subflow id")
	}
}

// TestSubflowCRUD_EndToEnd is the round-trip test from the issue:
// create a subflow, then instantiate it, then confirm the instance
// appears in the target tab. The mock serves /flow/global for the
// subflow list and /flow/<tab> for the tab, and tracks every call
// so a regression that drops one of the steps fails loud.
func TestSubflowCRUD_EndToEnd(t *testing.T) {
	var (
		mu          sync.Mutex
		tab         = []byte(`{"id":"tab_app","label":"App","nodes":[]}`)
		putTabBody  []byte
		putGlobBody []byte
	)
	refreshTab := func() []byte {
		// Build a flat flow that the mock can serve as the live
		// tab. The current state of tab is what AddNode will
		// rewrite; we just keep a snapshot for the GET handler.
		out := make([]byte, len(tab))
		copy(out, tab)
		return out
	}
	ingestTabPut := func(body []byte) {
		tab = body
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			// Backup snapshot fetch.
			_, _ = w.Write([]byte(`[` + string(refreshTab()) + `]`))
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			// Initial state: no subflows. After CreateSubflow, the
			// next GET sees the new one because the mock reads
			// from the putGlobBody it captured last.
			if putGlobBody != nil {
				_, _ = w.Write(putGlobBody)
				return
			}
			_, _ = w.Write([]byte(emptyGlobalSubflowsBody))
		case r.Method == "PUT" && r.URL.Path == "/flow/global":
			putGlobBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "GET" && r.URL.Path == "/flow/tab_app":
			_, _ = w.Write(refreshTab())
		case r.Method == "PUT" && r.URL.Path == "/flow/tab_app":
			putTabBody, _ = io.ReadAll(r.Body)
			ingestTabPut(putTabBody)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	ctx := context.Background()

	// Step 1: create a subflow.
	created, err := c.CreateSubflow(ctx, json.RawMessage(`{
		"id":"sf_round","type":"subflow","name":"Round-trip",
		"in":[],"out":[],"env":[],"nodes":[]
	}`))
	if err != nil {
		t.Fatalf("CreateSubflow: %v", err)
	}
	if !strings.Contains(string(created), `"id":"sf_round"`) {
		t.Errorf("CreateSubflow: returned body missing id: %s", created)
	}

	// Step 2: instantiate it into the tab.
	if _, err := c.InstantiateSubflow(ctx, "tab_app", "sf_round", json.RawMessage(`{"id":"inst_1","x":200,"y":200}`)); err != nil {
		t.Fatalf("InstantiateSubflow: %v", err)
	}

	// Step 3: confirm the instance is in the live tab.
	list, err := c.ListSubflows(ctx)
	if err != nil {
		t.Fatalf("ListSubflows: %v", err)
	}
	if len(list) != 1 || !strings.Contains(string(list[0]), `"id":"sf_round"`) {
		t.Errorf("ListSubflows: expected the round-trip subflow, got %s", list)
	}
	// Decode the latest tab body and look for the instance node.
	var tabDoc struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := json.Unmarshal(tab, &tabDoc); err != nil {
		t.Fatalf("decoding tab: %v", err)
	}
	found := false
	for _, n := range tabDoc.Nodes {
		if n["id"] == "inst_1" {
			found = true
			if n["type"] != "subflow:sf_round" {
				t.Errorf("instance type=%v, want subflow:sf_round", n["type"])
			}
			if n["z"] != "tab_app" {
				t.Errorf("instance z=%v, want tab_app", n["z"])
			}
		}
	}
	if !found {
		t.Errorf("instance node not in tab: %s", tab)
	}

	// Step 4: delete the subflow and confirm the helper removes it.
	if err := c.DeleteSubflow(ctx, "sf_round"); err != nil {
		t.Fatalf("DeleteSubflow: %v", err)
	}
	list, err = c.ListSubflows(ctx)
	if err != nil {
		t.Fatalf("ListSubflows (after delete): %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 subflows after delete, got %d", len(list))
	}
}
