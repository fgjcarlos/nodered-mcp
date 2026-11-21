package nodered

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// A subflow instance is judged by the definition it runs, resolved in the
// configuration the write deploys.
func TestDeniedSubflowInstance(t *testing.T) {
	effective := []json.RawMessage{
		json.RawMessage(`{"id":"t1","type":"tab"}`),
		json.RawMessage(`{"id":"sfa","type":"subflow"}`),
		json.RawMessage(`{"id":"ia","type":"subflow:sfb","z":"sfa"}`),
		json.RawMessage(`{"id":"sfb","type":"subflow","nodes":[{"id":"e1","type":"exec","z":"sfb"}]}`),
		json.RawMessage(`{"id":"sfc","type":"subflow"}`),
		json.RawMessage(`{"id":"ic","type":"subflow:sfd","z":"sfc"}`),
		json.RawMessage(`{"id":"sfd","type":"subflow"}`),
		json.RawMessage(`{"id":"id","type":"subflow:sfc","z":"sfd"}`),
		json.RawMessage(`{"id":"in","type":"inject","z":"sfd"}`),
		// A tab sharing an id with nothing a subflow:<id> instance names.
		json.RawMessage(`{"id":"e2","type":"exec","z":"t1"}`),
	}
	cases := map[string]struct {
		doc         string
		wantType    string
		wantSubflow string
	}{
		"nested instance reports the definition that carries the type": {
			doc: `{"id":"t1","nodes":[{"id":"i1","type":"subflow:sfa","z":"t1"}]}`, wantType: "exec", wantSubflow: "sfb",
		},
		"definition carried by the document itself": {
			doc:      `{"id":"t1","nodes":[{"id":"i1","type":"subflow:sfz","z":"t1"}],"subflows":[{"id":"sfz","type":"subflow","configs":[{"id":"s1","type":"system","z":"sfz"}]}]}`,
			wantType: "system", wantSubflow: "sfz",
		},
		"benign cycle ends and is allowed":                   {doc: `{"id":"t1","nodes":[{"id":"i1","type":"subflow:sfc","z":"t1"}]}`},
		"missing definition is left to reference validation": {doc: `{"id":"t1","nodes":[{"id":"i1","type":"subflow:nope","z":"t1"}]}`},
		"a tab id is not a definition":                       {doc: `{"id":"t2","nodes":[{"id":"i1","type":"subflow:t1","z":"t2"}]}`},
		"direct types are not this check's business":         {doc: `{"id":"t1","nodes":[{"id":"e3","type":"exec","z":"t1"}]}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := issue329Denied(t, issue329Policy("exec", "system"))
			done := make(chan error, 1)
			go func() { done <- c.deniedSubflowInstance(json.RawMessage(tc.doc), effective) }()
			var err error
			select {
			case err = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("the check did not terminate")
			}
			if tc.wantType == "" {
				if err != nil {
					t.Fatalf("expected no refusal, got %v", err)
				}
				return
			}
			assertIssue329DeniedError(t, err, tc.wantType)
			var denied *DeniedNodeError
			if errors.As(err, &denied) && denied.Subflow != tc.wantSubflow {
				t.Errorf("refusal names subflow %q, want %q", denied.Subflow, tc.wantSubflow)
			}
		})
	}
}

func TestDeniedNodeErrorNamesTheSubflow(t *testing.T) {
	err := &DeniedNodeError{NodeType: "exec", Subflow: "sfx"}
	const want = `node type "exec" (inside subflow "sfx") is denied by the configured node policy`
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// subflowInstanceRuntime serves a tab t1 that already holds an instance i0
// of sfx (which carries exec), plus a benign definition sfok, and accepts
// deployments. It returns the client and the deployment count.
func subflowInstanceRuntime(t *testing.T) (*Client, *int) {
	t.Helper()
	const flows = `{"rev":"r","flows":[
		{"id":"t1","type":"tab","label":"T1"},
		{"id":"n1","type":"inject","z":"t1","x":100,"y":100,"wires":[[]]},
		{"id":"i0","type":"subflow:sfx","z":"t1","x":200,"y":100,"wires":[]},
		{"id":"sfx","type":"subflow","name":"x","in":[{"x":0,"y":0,"wires":[]}],"out":[]},
		{"id":"ex","type":"exec","z":"sfx","x":10,"y":10,"command":"id","wires":[[],[],[]]},
		{"id":"sfok","type":"subflow","name":"ok","in":[],"out":[]}]}`
	var mu sync.Mutex
	deploys := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(flows))
		case r.Method == http.MethodPost && r.URL.Path == "/flows":
			mu.Lock()
			deploys++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"rev":"r2"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.SetNodePolicy(issue329Policy("exec", "system"))
	return c, &deploys
}

// An instance already in the runtime is not judged by a write that leaves
// it alone, and an instance of a benign definition still deploys.
func TestDeniedSubflowInstance_LeavesUntouchedAndBenignInstancesAlone(t *testing.T) {
	ctx := context.Background()
	c, deploys := subflowInstanceRuntime(t)
	if err := c.AddNode(ctx, "t1", json.RawMessage(`{"id":"n2","type":"debug","z":"t1","x":300,"y":100,"wires":[]}`)); err != nil {
		t.Fatalf("AddNode next to an existing instance: %v", err)
	}
	if err := c.AddNode(ctx, "t1", json.RawMessage(`{"id":"i9","type":"subflow:sfok","z":"t1","x":300,"y":200,"wires":[]}`)); err != nil {
		t.Fatalf("AddNode of a benign instance: %v", err)
	}
	if *deploys != 2 {
		t.Errorf("deployments = %d, want 2", *deploys)
	}
}

// Wiring into an existing instance arms it, so it is judged by its
// definition, like a direct node connect_nodes touches.
func TestDeniedSubflowInstance_ConnectNodesJudgesTheInstance(t *testing.T) {
	c, deploys := subflowInstanceRuntime(t)
	err := c.ConnectNodes(context.Background(), "t1", "n1", 0, "i0")
	assertIssue329DeniedError(t, err, "exec")
	if *deploys != 0 {
		t.Errorf("a refused connect_nodes deployed %d time(s)", *deploys)
	}
}
