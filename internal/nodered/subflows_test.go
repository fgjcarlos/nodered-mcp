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

// TestDeleteSubflow_RefusesWhileInstanceInTab is the regression test
// for issue #332: deleting a definition that a flow tab still
// instances left the runtime with a node pointing at a missing type,
// and the next deploy logged "Waiting for missing types to be
// registered".
//
// The delete must be refused, with the dependency named, and nothing
// may be written: no PUT to /flow/global, no backup.
func TestDeleteSubflow_RefusesWhileInstanceInTab(t *testing.T) {
	var putGlobal bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			_, _ = w.Write([]byte(globalSubflowsBody))
		case r.Method == "GET" && r.URL.Path == "/flows":
			// A tab holding one instance of sf_a.
			_, _ = w.Write([]byte(`[
				{"id":"tab1","type":"tab","label":"Home"},
				{"id":"inst1","type":"subflow:sf_a","z":"tab1","x":140,"y":140}
			]`))
		case r.Method == "PUT" && r.URL.Path == "/flow/global":
			putGlobal = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	err := c.DeleteSubflow(context.Background(), "sf_a")
	if err == nil {
		t.Fatal("expected the delete to be refused while sf_a is in use, got nil")
	}
	var inUse *SubflowInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("expected *SubflowInUseError, got %T: %v", err, err)
	}
	if inUse.ID != "sf_a" {
		t.Errorf("error names subflow %q, want sf_a", inUse.ID)
	}
	if len(inUse.Refs) != 1 {
		t.Fatalf("expected 1 reference, got %d: %+v", len(inUse.Refs), inUse.Refs)
	}
	ref := inUse.Refs[0]
	if ref.Owner != "tab1" || ref.OwnerKind != "tab" || ref.NodeID != "inst1" {
		t.Errorf("unexpected reference: %+v", ref)
	}
	// The message has to name the dependency, not just refuse.
	msg := err.Error()
	for _, want := range []string{"sf_a", "inst1", "tab1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message does not mention %q: %s", want, msg)
		}
	}
	if putGlobal {
		t.Error("the definition was written despite being in use")
	}
}

// TestDeleteSubflow_RefusesWhileInstanceNestedInSubflow covers the
// second shape an instance can take: a node of type "subflow:<id>"
// living inside another subflow's definition rather than in a tab.
// Removing the definition would break the outer subflow the same way.
func TestDeleteSubflow_RefusesWhileInstanceNestedInSubflow(t *testing.T) {
	const globalBody = `{
		"id":"global",
		"subflows":[
			{"id":"sf_a","type":"subflow","name":"A","in":[],"out":[],"env":[],"nodes":[]},
			{"id":"sf_b","type":"subflow","name":"B","in":[],"out":[],"env":[],"nodes":[
				{"id":"nested1","type":"subflow:sf_a","x":140,"y":140}
			]}
		],
		"configs":[]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			_, _ = w.Write([]byte(globalBody))
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[{"id":"tab1","type":"tab","label":"Home"}]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	err := c.DeleteSubflow(context.Background(), "sf_a")
	if err == nil {
		t.Fatal("expected the delete to be refused while sf_a is nested in sf_b, got nil")
	}
	var inUse *SubflowInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("expected *SubflowInUseError, got %T: %v", err, err)
	}
	if len(inUse.Refs) != 1 {
		t.Fatalf("expected 1 reference, got %d: %+v", len(inUse.Refs), inUse.Refs)
	}
	ref := inUse.Refs[0]
	if ref.Owner != "sf_b" || ref.OwnerKind != "subflow" || ref.NodeID != "nested1" {
		t.Errorf("unexpected reference: %+v", ref)
	}
}

// TestDeleteSubflow_RefusalListsEveryReference: an operator who has
// to delete one instance, retry, and find the next one has a much
// worse time than one handed the full list. Every reference is
// reported at once.
func TestDeleteSubflow_RefusalListsEveryReference(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			_, _ = w.Write([]byte(globalSubflowsBody))
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[
				{"id":"tab1","type":"tab","label":"Home"},
				{"id":"inst1","type":"subflow:sf_a","z":"tab1","x":140,"y":140},
				{"id":"tab2","type":"tab","label":"Other"},
				{"id":"inst2","type":"subflow:sf_a","z":"tab2","x":140,"y":140}
			]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	err := c.DeleteSubflow(context.Background(), "sf_a")
	if err == nil {
		t.Fatal("expected the delete to be refused, got nil")
	}
	var inUse *SubflowInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("expected *SubflowInUseError, got %T: %v", err, err)
	}
	if len(inUse.Refs) != 2 {
		t.Fatalf("expected 2 references, got %d: %+v", len(inUse.Refs), inUse.Refs)
	}
	msg := err.Error()
	for _, want := range []string{"inst1", "tab1", "inst2", "tab2"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message does not mention %q: %s", want, msg)
		}
	}
}

// TestDeleteSubflow_AllowsWhenNothingInstancesIt is the other half of
// the contract: a definition no flow instances is still deletable, and
// the runtime keeps working. The delete must reach the runtime.
func TestDeleteSubflow_AllowsWhenNothingInstancesIt(t *testing.T) {
	var putGlobal bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			_, _ = w.Write([]byte(globalSubflowsBody))
		case r.Method == "GET" && r.URL.Path == "/flows":
			// A tab with an unrelated node and an instance of another
			// subflow: neither is a reference to sf_a.
			_, _ = w.Write([]byte(`[
				{"id":"tab1","type":"tab","label":"Home"},
				{"id":"n1","type":"inject","z":"tab1","x":140,"y":140},
				{"id":"inst1","type":"subflow:sf_other","z":"tab1","x":140,"y":140}
			]`))
		case r.Method == "PUT" && r.URL.Path == "/flow/global":
			putGlobal = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	if err := c.DeleteSubflow(context.Background(), "sf_a"); err != nil {
		t.Fatalf("DeleteSubflow: %v", err)
	}
	if !putGlobal {
		t.Error("the definition was not written to the runtime")
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
		case r.Method == "POST" && r.URL.Path == "/flows":
			putPath = r.URL.Path
			putBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write(testRevisionedTab(t, string(flowBody)))
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
	if putPath != "/flows" {
		t.Errorf("expected POST /flows for the write, got %q", putPath)
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
		case r.Method == "POST" && r.URL.Path == "/flows":
			putBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write(testRevisionedTab(t, `{"id":"tab_x","label":"X","nodes":[]}`))
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
		tab = testNestedDeployment(t, body, "tab_app")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			// Revisioned deployment base and backup.
			_, _ = w.Write(testRevisionedTab(t, string(refreshTab())))
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
		case r.Method == "POST" && r.URL.Path == "/flows":
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

	// Step 4: deleting the subflow while the instance is live must be
	// refused. The instance would be left pointing at a missing type,
	// and the runtime would not deploy the affected flows.
	err = c.DeleteSubflow(ctx, "sf_round")
	if err == nil {
		t.Fatal("expected the delete to be refused while the subflow is instantiated, got nil")
	}
	var inUse *SubflowInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("expected *SubflowInUseError, got %T: %v", err, err)
	}
	if len(inUse.Refs) != 1 {
		t.Fatalf("expected 1 reference, got %d: %+v", len(inUse.Refs), inUse.Refs)
	}
	if inUse.Refs[0].NodeID != "inst_1" || inUse.Refs[0].Owner != "tab_app" {
		t.Errorf("unexpected reference: %+v", inUse.Refs[0])
	}

	// Step 5: remove the instance, and the definition goes away.
	if err := c.DeleteNode(ctx, "tab_app", "inst_1"); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
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

// TestDeleteSubflow_RefusesWhenInstanceHasNonStringID: a flow
// configuration written by something other than this client can carry
// a non-string id. Decoding the whole entry into a struct of string
// fields drops the entry on the first non-string value, which would
// hide a real instance and let the definition be deleted — the exact
// failure the guard exists to prevent. The instance is still an
// instance, so the delete is refused.
func TestDeleteSubflow_RefusesWhenInstanceHasNonStringID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			_, _ = w.Write([]byte(globalSubflowsBody))
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[
				{"id":"tab1","type":"tab","label":"Home"},
				{"id":123,"type":"subflow:sf_a","z":"tab1","x":140,"y":140}
			]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	err := c.DeleteSubflow(context.Background(), "sf_a")
	if err == nil {
		t.Fatal("expected the delete to be refused for an instance with a non-string id, got nil")
	}
	var inUse *SubflowInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("expected *SubflowInUseError, got %T: %v", err, err)
	}
	if len(inUse.Refs) != 1 {
		t.Fatalf("expected 1 reference, got %d: %+v", len(inUse.Refs), inUse.Refs)
	}
	if inUse.Refs[0].Owner != "tab1" {
		t.Errorf("unexpected owner: %+v", inUse.Refs[0])
	}
}

// TestDeleteSubflow_RefusesWhenInstanceHasNonStringZ: the same
// tolerance for the owner field. The owner cannot be named, but the
// instance is real, so the delete is refused and the owner is
// reported as unknown rather than guessed.
func TestDeleteSubflow_RefusesWhenInstanceHasNonStringZ(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			_, _ = w.Write([]byte(globalSubflowsBody))
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[
				{"id":"tab1","type":"tab","label":"Home"},
				{"id":"inst1","type":"subflow:sf_a","z":99,"x":140,"y":140}
			]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	err := c.DeleteSubflow(context.Background(), "sf_a")
	if err == nil {
		t.Fatal("expected the delete to be refused for an instance with a non-string z, got nil")
	}
	var inUse *SubflowInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("expected *SubflowInUseError, got %T: %v", err, err)
	}
	if len(inUse.Refs) != 1 {
		t.Fatalf("expected 1 reference, got %d: %+v", len(inUse.Refs), inUse.Refs)
	}
	if inUse.Refs[0].OwnerKind != SubflowOwnerUnknown {
		t.Errorf("owner kind = %q, want %q", inUse.Refs[0].OwnerKind, SubflowOwnerUnknown)
	}
}

// TestDeleteSubflow_RefusesWhenInstanceNestedInDefinitionConfigs: a
// subflow definition carries two member collections, "nodes" and
// "configs". An instance with no canvas coordinates is filed in
// "configs", and the runtime resolves its type just the same.
func TestDeleteSubflow_RefusesWhenInstanceNestedInDefinitionConfigs(t *testing.T) {
	const globalBody = `{
		"id":"global",
		"subflows":[
			{"id":"sf_a","type":"subflow","name":"A","in":[],"out":[],"env":[],"nodes":[],"configs":[]},
			{"id":"sf_b","type":"subflow","name":"B","in":[],"out":[],"env":[],"nodes":[],"configs":[
				{"id":"nested_cfg","type":"subflow:sf_a","z":"sf_b"}
			]}
		],
		"configs":[]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			_, _ = w.Write([]byte(globalBody))
		case r.Method == "GET" && r.URL.Path == "/flows":
			// The flat view carries no instance at all: only the
			// definition scan can see this one.
			_, _ = w.Write([]byte(`[{"id":"tab1","type":"tab","label":"Home"}]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	err := c.DeleteSubflow(context.Background(), "sf_a")
	if err == nil {
		t.Fatal("expected the delete to be refused for an instance nested in a definition's configs, got nil")
	}
	var inUse *SubflowInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("expected *SubflowInUseError, got %T: %v", err, err)
	}
	if len(inUse.Refs) != 1 {
		t.Fatalf("expected 1 reference, got %d: %+v", len(inUse.Refs), inUse.Refs)
	}
	ref := inUse.Refs[0]
	if ref.Owner != "sf_b" || ref.OwnerKind != SubflowOwnerSubflow || ref.NodeID != "nested_cfg" {
		t.Errorf("unexpected reference: %+v", ref)
	}
}

// TestDeleteSubflow_NamesTheUnreadableDefinition: a definition whose
// member collections cannot be read cannot be checked for references,
// so the delete is refused. The message has to name both the
// definition that is broken and the one that was asked for,
// otherwise the operator cannot tell which one to repair.
func TestDeleteSubflow_NamesTheUnreadableDefinition(t *testing.T) {
	const globalBody = `{
		"id":"global",
		"subflows":[
			{"id":"sf_a","type":"subflow","name":"A","in":[],"out":[],"env":[],"nodes":[]},
			{"id":"sf_broken","type":"subflow","name":"Broken","nodes":"not-an-array"}
		],
		"configs":[]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			_, _ = w.Write([]byte(globalBody))
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[{"id":"tab1","type":"tab","label":"Home"}]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	err := c.DeleteSubflow(context.Background(), "sf_a")
	if err == nil {
		t.Fatal("expected the delete to be refused when a definition cannot be checked, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"sf_broken", "sf_a"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message does not mention %q: %s", want, msg)
		}
	}
}

// TestDeleteSubflow_AllowsSelfInstantiatingDefinition: a definition
// that contains an instance of itself is not a reference to itself —
// the instance goes away with the definition, and the runtime does not
// report a missing type. Refusing would make the definition
// impossible to delete, with advice that cannot be followed.
func TestDeleteSubflow_AllowsSelfInstantiatingDefinition(t *testing.T) {
	const globalBody = `{
		"id":"global",
		"subflows":[
			{"id":"sf_a","type":"subflow","name":"A","in":[],"out":[],"env":[],"nodes":[
				{"id":"n1","type":"subflow:sf_a","z":"sf_a","x":140,"y":140}
			],"configs":[]}
		],
		"configs":[]
	}`
	var putGlobal bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			_, _ = w.Write([]byte(globalBody))
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[
				{"id":"tab1","type":"tab","label":"Home"},
				{"id":"n1","type":"subflow:sf_a","z":"sf_a","x":140,"y":140}
			]`))
		case r.Method == "PUT" && r.URL.Path == "/flow/global":
			putGlobal = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	if err := c.DeleteSubflow(context.Background(), "sf_a"); err != nil {
		t.Fatalf("DeleteSubflow: %v", err)
	}
	if !putGlobal {
		t.Error("the definition was not written to the runtime")
	}
}

// TestDeleteSubflow_ReportsAnInstanceOnlyOnce: the same instance can
// be seen by both scans — the definition scan and the flat /flows
// scan. It must be reported once, with one owner, not twice with
// contradictory owners.
func TestDeleteSubflow_ReportsAnInstanceOnlyOnce(t *testing.T) {
	const globalBody = `{
		"id":"global",
		"subflows":[
			{"id":"sf_a","type":"subflow","name":"A","in":[],"out":[],"env":[],"nodes":[]},
			{"id":"sf_b","type":"subflow","name":"B","in":[],"out":[],"env":[],"nodes":[
				{"id":"n1","type":"subflow:sf_a","z":"sf_b","x":140,"y":140}
			],"configs":[]}
		],
		"configs":[]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			_, _ = w.Write([]byte(globalBody))
		case r.Method == "GET" && r.URL.Path == "/flows":
			// The owning subflow is not named in the flat view, so
			// this scan cannot tell the owner kind on its own.
			_, _ = w.Write([]byte(`[{"id":"n1","type":"subflow:sf_a","z":"sf_b","x":140,"y":140}]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	err := c.DeleteSubflow(context.Background(), "sf_a")
	if err == nil {
		t.Fatal("expected the delete to be refused, got nil")
	}
	var inUse *SubflowInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("expected *SubflowInUseError, got %T: %v", err, err)
	}
	if len(inUse.Refs) != 1 {
		t.Fatalf("expected 1 reference for one instance, got %d: %+v", len(inUse.Refs), inUse.Refs)
	}
	if inUse.Refs[0].OwnerKind != SubflowOwnerSubflow || inUse.Refs[0].Owner != "sf_b" {
		t.Errorf("unexpected reference: %+v", inUse.Refs[0])
	}
}

// TestDeleteSubflow_ReportsOwnerlessInstanceAsGlobal: an instance
// with no "z" lives at global scope. Reporting it as "in tab \"\"" is
// unreadable, so the owner is named as the global configuration.
func TestDeleteSubflow_ReportsOwnerlessInstanceAsGlobal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			_, _ = w.Write([]byte(globalSubflowsBody))
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[{"id":"i1","type":"subflow:sf_a"}]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	err := c.DeleteSubflow(context.Background(), "sf_a")
	if err == nil {
		t.Fatal("expected the delete to be refused, got nil")
	}
	var inUse *SubflowInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("expected *SubflowInUseError, got %T: %v", err, err)
	}
	if len(inUse.Refs) != 1 {
		t.Fatalf("expected 1 reference, got %d: %+v", len(inUse.Refs), inUse.Refs)
	}
	if inUse.Refs[0].OwnerKind != SubflowOwnerGlobal {
		t.Errorf("owner kind = %q, want %q", inUse.Refs[0].OwnerKind, SubflowOwnerGlobal)
	}
	if !strings.Contains(err.Error(), "global configuration") {
		t.Errorf("error message does not name the global configuration: %s", err.Error())
	}
}

// TestDeleteSubflow_RefusesWhenTabSharesTheDefinitionID: tabs and
// subflow definitions live in separate id maps inside the runtime, so
// a tab can carry the same id as a definition. The owner of a node is
// resolved tab-first, so an instance whose owner is that id may belong
// to the tab — and skipping it by owner id would drop a real
// reference and let the definition be deleted under a live instance.
func TestDeleteSubflow_RefusesWhenTabSharesTheDefinitionID(t *testing.T) {
	const globalBody = `{
		"id":"global",
		"subflows":[
			{"id":"DUP","type":"subflow","name":"Dup","in":[],"out":[],"env":[],"nodes":[],"configs":[]}
		],
		"configs":[]
	}`
	var putGlobal bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			_, _ = w.Write([]byte(globalBody))
		case r.Method == "GET" && r.URL.Path == "/flows":
			// A tab and a subflow definition sharing the id "DUP",
			// with an instance of subflow:DUP in the tab.
			_, _ = w.Write([]byte(`[
				{"id":"DUP","type":"tab","label":"Home"},
				{"id":"i1","type":"subflow:DUP","z":"DUP","x":140,"y":140},
				{"id":"DUP","type":"subflow","name":"Dup"}
			]`))
		case r.Method == "PUT" && r.URL.Path == "/flow/global":
			putGlobal = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	err := c.DeleteSubflow(context.Background(), "DUP")
	if err == nil {
		t.Fatal("expected the delete to be refused while the tab still instances it, got nil")
	}
	var inUse *SubflowInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("expected *SubflowInUseError, got %T: %v", err, err)
	}
	if len(inUse.Refs) != 1 {
		t.Fatalf("expected 1 reference, got %d: %+v", len(inUse.Refs), inUse.Refs)
	}
	ref := inUse.Refs[0]
	if ref.NodeID != "i1" || ref.Owner != "DUP" {
		t.Errorf("unexpected reference: %+v", ref)
	}
	// The runtime resolves the owner tab-first, so the tab is the owner.
	if ref.OwnerKind != SubflowOwnerTab {
		t.Errorf("owner kind = %q, want %q", ref.OwnerKind, SubflowOwnerTab)
	}
	if putGlobal {
		t.Error("the definition was written despite being in use")
	}
}

// TestDeleteSubflow_RefusesWhenInstanceTypeIsAnArray: the runtime
// matches a missing type against /^subflow:(.+)$/ after coercing the
// type with String(), and String(["subflow:x"]) is "subflow:x". An
// array-typed instance is therefore a real reference.
func TestDeleteSubflow_RefusesWhenInstanceTypeIsAnArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			_, _ = w.Write([]byte(globalSubflowsBody))
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[
				{"id":"tab1","type":"tab","label":"Home"},
				{"id":"i1","type":["subflow:sf_a"],"z":"tab1","x":140,"y":140}
			]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	err := c.DeleteSubflow(context.Background(), "sf_a")
	if err == nil {
		t.Fatal("expected the delete to be refused for an array-typed instance, got nil")
	}
	var inUse *SubflowInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("expected *SubflowInUseError, got %T: %v", err, err)
	}
	if len(inUse.Refs) != 1 {
		t.Fatalf("expected 1 reference, got %d: %+v", len(inUse.Refs), inUse.Refs)
	}
	if inUse.Refs[0].NodeID != "i1" {
		t.Errorf("unexpected reference: %+v", inUse.Refs[0])
	}
}

// TestDeleteSubflow_RefusesWhenNestedInstanceTypeIsAnArray: the same
// coercion applies to an instance nested in another subflow
// definition.
func TestDeleteSubflow_RefusesWhenNestedInstanceTypeIsAnArray(t *testing.T) {
	const globalBody = `{
		"id":"global",
		"subflows":[
			{"id":"sf_a","type":"subflow","name":"A","in":[],"out":[],"env":[],"nodes":[],"configs":[]},
			{"id":"sf_b","type":"subflow","name":"B","in":[],"out":[],"env":[],"nodes":[
				{"id":"n1","type":["subflow:sf_a"],"z":"sf_b","x":140,"y":140}
			],"configs":[]}
		],
		"configs":[]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			_, _ = w.Write([]byte(globalBody))
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[{"id":"tab1","type":"tab","label":"Home"}]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	err := c.DeleteSubflow(context.Background(), "sf_a")
	if err == nil {
		t.Fatal("expected the delete to be refused for an array-typed nested instance, got nil")
	}
	var inUse *SubflowInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("expected *SubflowInUseError, got %T: %v", err, err)
	}
	if len(inUse.Refs) != 1 {
		t.Fatalf("expected 1 reference, got %d: %+v", len(inUse.Refs), inUse.Refs)
	}
	if inUse.Refs[0].NodeID != "n1" || inUse.Refs[0].Owner != "sf_b" {
		t.Errorf("unexpected reference: %+v", inUse.Refs[0])
	}
}

// TestDeleteSubflow_FailsClosedOnUnreadableFlowConfiguration: a
// /flows body this client cannot read is not a configuration without
// instances. The delete is refused instead of going through on a
// configuration nobody checked.
func TestDeleteSubflow_FailsClosedOnUnreadableFlowConfiguration(t *testing.T) {
	var putGlobal bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			_, _ = w.Write([]byte(globalSubflowsBody))
		case r.Method == "GET" && r.URL.Path == "/flows":
			// A revisioned envelope with no flows array.
			_, _ = w.Write([]byte(`{"rev":"abc123"}`))
		case r.Method == "PUT" && r.URL.Path == "/flow/global":
			putGlobal = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	err := c.DeleteSubflow(context.Background(), "sf_a")
	if err == nil {
		t.Fatal("expected the delete to be refused on an unreadable flow configuration, got nil")
	}
	if !strings.Contains(err.Error(), "flow configuration") {
		t.Errorf("error message does not name the unreadable configuration: %s", err.Error())
	}
	if putGlobal {
		t.Error("the definition was written despite the configuration not being readable")
	}
}

// TestJSNumberMatchesJavaScript pins the number rendering against
// JavaScript's own Number::toString.
//
// The runtime coerces a node's type with String() before matching it
// against /^subflow:(.+)$/, and a number can reach that comparison
// inside an array type. Go's own formatting disagrees with JavaScript
// on small magnitudes ("1e-07" against "1e-7") and on negative zero,
// and that difference is enough for the guard to miss a reference.
//
// The expected values are the ones JavaScript produces — the table was
// generated with node, not reasoned about.
func TestJSNumberMatchesJavaScript(t *testing.T) {
	cases := []struct {
		json string
		want string
	}{
		{"0", "0"},
		{"-0", "0"},
		{"0", "0"},
		{"1", "1"},
		{"-1", "-1"},
		{"123", "123"},
		{"123.45", "123.45"},
		{"0.1", "0.1"},
		{"0.2", "0.2"},
		{"0.3", "0.3"},
		{"0.3333333333333333", "0.3333333333333333"},
		{"1e+21", "1e+21"},
		{"1e-7", "1e-7"},
		{"0.000001", "0.000001"},
		{"0.00001", "0.00001"},
		{"1e-16", "1e-16"},
		{"1.5e-7", "1.5e-7"},
		{"123456789012345680000", "123456789012345680000"},
		{"5e-324", "5e-324"},
		{"1.7976931348623157e+308", "1.7976931348623157e+308"},
		{"1e-21", "1e-21"},
		{"0.000001", "0.000001"},
		{"1e-7", "1e-7"},
		{"-1e-7", "-1e-7"},
		{"1.2345678e-9", "1.2345678e-9"},
		{"2.5e-10", "2.5e-10"},
		{"1e+100", "1e+100"},
		{"1e-100", "1e-100"},
		{"999999999999999900000", "999999999999999900000"},
		{"1e-323", "1e-323"},
		{"3.14159265358979", "3.14159265358979"},
		{"10000000000000000", "10000000000000000"},
		{"1000000000000000", "1000000000000000"},
		{"1234567890123456800", "1234567890123456800"},
		{"0.001", "0.001"},
		{"0.5", "0.5"},
		{"2.5", "2.5"},
		{"1e+22", "1e+22"},
		{"1.5e+22", "1.5e+22"},
		{"1e-10", "1e-10"},
		{"6.02e+23", "6.02e+23"},
		{"0.00001", "0.00001"},
		{"100", "100"},
		{"100", "100"},
	}
	for _, c := range cases {
		var f float64
		if err := json.Unmarshal([]byte(c.json), &f); err != nil {
			t.Fatalf("decoding %q: %v", c.json, err)
		}
		if got := jsNumber(f); got != c.want {
			t.Errorf("jsNumber(%s) = %q, want %q", c.json, got, c.want)
		}
	}
}

// TestDeleteSubflow_RefusesWhenInstanceTypeArrayHoldsANumber: a number
// inside an array type changes the coerced text, and the runtime
// resolves the instance against the id that text carries. Go's own
// number formatting disagrees with JavaScript's on small magnitudes
// ("1e-07" against "1e-7"), which is enough to make the guard miss the
// reference — so the rendering has to match JavaScript's exactly.
//
// The id here carries a comma on purpose: an array type joins with
// commas, so a number can only reach the comparison through an id that
// contains one.
func TestDeleteSubflow_RefusesWhenInstanceTypeArrayHoldsANumber(t *testing.T) {
	const globalBody = `{
		"id":"global",
		"subflows":[
			{"id":"X,1e-7","type":"subflow","name":"Comma","in":[],"out":[],"env":[],"nodes":[],"configs":[]}
		],
		"configs":[]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			_, _ = w.Write([]byte(globalBody))
		case r.Method == "GET" && r.URL.Path == "/flows":
			// String(["subflow:X",1e-7]) is "subflow:X,1e-7" in
			// JavaScript, and that names the definition above.
			_, _ = w.Write([]byte(`[
				{"id":"tab1","type":"tab","label":"Home"},
				{"id":"i1","type":["subflow:X",1e-7],"z":"tab1","x":140,"y":140}
			]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	err := c.DeleteSubflow(context.Background(), "X,1e-7")
	if err == nil {
		t.Fatal("expected the delete to be refused for an array-typed instance holding a number, got nil")
	}
	var inUse *SubflowInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("expected *SubflowInUseError, got %T: %v", err, err)
	}
	if len(inUse.Refs) != 1 {
		t.Fatalf("expected 1 reference, got %d: %+v", len(inUse.Refs), inUse.Refs)
	}
	if inUse.Refs[0].NodeID != "i1" {
		t.Errorf("unexpected reference: %+v", inUse.Refs[0])
	}
}
