package nodered

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Fixtures of issue #473: a one-output definition and a tab whose instance
// of it is wired on that one output.
const (
	portFixtureDefinition = `{"id":"sf_a","type":"subflow","name":"A","in":[],"out":[{"x":300,"y":100,"wires":[]}]}`
	portFixtureTab        = `{"id":"tab_x","label":"X","nodes":[
		{"id":"n1","type":"debug","z":"tab_x","x":100,"y":100,"wires":[]},
		{"id":"n2","type":"debug","z":"tab_x","x":100,"y":140,"wires":[]},
		{"id":"inst","type":"subflow:sf_a","z":"tab_x","x":200,"y":100,"wires":[["n1"]]}]}`
	portFixtureRefusal = `subflow "sf_a" has 1 output(s), but the instance wires 3 port(s): wires must have at most 1 port(s)`
)

// portRuntime serves one flat configuration and records what a deployment
// carried. posted stays nil while nothing was deployed.
func portRuntime(t *testing.T) (*Client, *[]json.RawMessage) {
	t.Helper()
	entries := append(FlowArray(testRevisionedTab(t, portFixtureTab)), json.RawMessage(portFixtureDefinition))
	var (
		mu     sync.Mutex
		posted []json.RawMessage
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveTestPalette(w, r) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			body, err := json.Marshal(map[string]any{"rev": "r", "flows": entries})
			if err != nil {
				t.Errorf("encoding flows: %v", err)
			}
			_, _ = w.Write(body)
		case r.Method == "POST" && r.URL.Path == "/flows":
			var dep struct {
				Flows []json.RawMessage `json:"flows"`
			}
			if err := json.NewDecoder(r.Body).Decode(&dep); err != nil {
				t.Errorf("decoding deployment: %v", err)
			}
			posted = dep.Flows
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	return clientWithBackup(t, srv.URL), &posted
}

// refusedNothingWritten asserts the shape every refusal shares: the error
// carries want, and no deployment (and therefore no backup) happened.
func refusedNothingWritten(t *testing.T, c *Client, err error, posted *[]json.RawMessage, want string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected the write to be refused")
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error does not mention %q: %v", want, err)
	}
	if *posted != nil {
		t.Errorf("a refused write deployed: %s", rawJSON(*posted))
	}
	backups, err := c.ListBackups()
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(backups) != 0 {
		t.Errorf("a refused write took a backup: %v", backups)
	}
}

// TestAddNode_InstanceWiredPastOutputs is the issue #473 acceptance case: the
// instance an add_node builds is bounded by its definition's outputs, with
// the message instantiate_subflow uses.
func TestAddNode_InstanceWiredPastOutputs(t *testing.T) {
	c, posted := portRuntime(t)
	err := c.AddNode(context.Background(), "tab_x",
		json.RawMessage(`{"id":"inst2","type":"subflow:sf_a","z":"tab_x","x":300,"y":100,"wires":[["n1"],["n1"],["n1"]]}`))
	refusedNothingWritten(t, c, err, posted, portFixtureRefusal)
}

// TestAddNode_InstanceWiresNotAnArrayOfArrays: a wires value of the wrong
// shape is refused on every instance path, not just the port count.
func TestAddNode_InstanceWiresNotAnArrayOfArrays(t *testing.T) {
	for _, wires := range []string{`["n1"]`, `"n1"`} {
		t.Run(wires, func(t *testing.T) {
			c, posted := portRuntime(t)
			err := c.AddNode(context.Background(), "tab_x",
				json.RawMessage(`{"id":"inst2","type":"subflow:sf_a","z":"tab_x","x":300,"y":100,"wires":`+wires+`}`))
			refusedNothingWritten(t, c, err, posted, `node "inst2" has invalid wires (expected array of arrays`)
		})
	}
}

// TestAddNode_InstanceWithinOutputs: a valid instance still deploys.
func TestAddNode_InstanceWithinOutputs(t *testing.T) {
	c, posted := portRuntime(t)
	err := c.AddNode(context.Background(), "tab_x",
		json.RawMessage(`{"id":"inst2","type":"subflow:sf_a","z":"tab_x","x":300,"y":100,"wires":[["n1"]]}`))
	if err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if *posted == nil {
		t.Fatal("the valid instance was not deployed")
	}
	if got := compactJSON(t, fieldOfObject(t, []byte(rawJSON(*posted)), "inst2", "wires")); got != `[["n1"]]` {
		t.Errorf("deployed wires = %s", got)
	}
}

// TestUpdateNode_PatchRaisesInstancePorts: the merged node is judged, so a
// patch that only touches wires is caught too.
func TestUpdateNode_PatchRaisesInstancePorts(t *testing.T) {
	c, posted := portRuntime(t)
	err := c.UpdateNode(context.Background(), "tab_x", "inst", map[string]json.RawMessage{
		"wires": json.RawMessage(`[["n1"],["n2"]]`),
	})
	refusedNothingWritten(t, c, err, posted,
		`subflow "sf_a" has 1 output(s), but the instance wires 2 port(s): wires must have at most 1 port(s)`)
}

// TestUpdateNode_InstanceWithinOutputs: a patch that keeps the wires inside
// the outputs still deploys.
func TestUpdateNode_InstanceWithinOutputs(t *testing.T) {
	c, posted := portRuntime(t)
	if err := c.UpdateNode(context.Background(), "tab_x", "inst", map[string]json.RawMessage{
		"wires": json.RawMessage(`[["n2"]]`),
	}); err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}
	if got := compactJSON(t, fieldOfObject(t, []byte(rawJSON(*posted)), "inst", "wires")); got != `[["n2"]]` {
		t.Errorf("deployed wires = %s", got)
	}
}

// TestConnectNodes_InstancePortBeyondOutputs: wiring an instance on a port at
// or past its outputs is refused, so connect_nodes closes the same hole.
func TestConnectNodes_InstancePortBeyondOutputs(t *testing.T) {
	c, posted := portRuntime(t)
	err := c.ConnectNodes(context.Background(), "tab_x", "inst", 1, "n2")
	refusedNothingWritten(t, c, err, posted,
		`subflow "sf_a" has 1 output(s), but the instance wires 2 port(s): wires must have at most 1 port(s)`)
}

// TestConnectNodes_InstancePortWithinOutputs: the one port the definition
// has is still usable.
func TestConnectNodes_InstancePortWithinOutputs(t *testing.T) {
	c, posted := portRuntime(t)
	if err := c.ConnectNodes(context.Background(), "tab_x", "inst", 0, "n2"); err != nil {
		t.Fatalf("ConnectNodes: %v", err)
	}
	if got := compactJSON(t, fieldOfObject(t, []byte(rawJSON(*posted)), "inst", "wires")); got != `[["n1","n2"]]` {
		t.Errorf("deployed wires = %s", got)
	}
}

// TestValidateInstancePorts pins the guard itself: a non-instance and an
// instance whose definition the configuration does not carry are left to the
// other checks, while a malformed wires value is refused either way.
func TestValidateInstancePorts(t *testing.T) {
	flows := []json.RawMessage{json.RawMessage(portFixtureDefinition)}
	for _, tc := range []struct {
		name, node, wantErr string
	}{
		{"not an instance", `{"id":"n","type":"debug","wires":[["a"],["b"]]}`, ""},
		{"definition missing is unknown outputs", `{"id":"i","type":"subflow:sf_ghost","wires":[["a"],["b"]]}`, ""},
		{"definition missing still refuses malformed wires", `{"id":"i","type":"subflow:sf_ghost","wires":"a"}`, `node "i" has invalid wires`},
		{"null wires", `{"id":"i","type":"subflow:sf_a","wires":null}`, ""},
		{"wires absent", `{"id":"i","type":"subflow:sf_a"}`, ""},
		{"within outputs", `{"id":"i","type":"subflow:sf_a","wires":[["a"]]}`, ""},
		{"past outputs", `{"id":"i","type":"subflow:sf_a","wires":[["a"],["b"]]}`, `subflow "sf_a" has 1 output(s), but the instance wires 2 port(s)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateInstancePorts(json.RawMessage(tc.node), flows)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected refusal: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected %q, got %v", tc.wantErr, err)
			}
		})
	}
}
