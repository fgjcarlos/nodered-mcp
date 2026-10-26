package nodered

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The write-path guards of issue #334: a configuration whose references
// do not resolve never reaches the runtime.
//
// Every test in this file fails on the tree before the fix: SetFlows
// and RestoreFlows deployed whatever they were given, and the subflow
// writes did not look at the topology of the definition at all.

// A full deployment with a wire that points nowhere is refused, and the
// runtime is never contacted.
func TestSetFlows_RefusesConfigurationWithDanglingWire(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the runtime was contacted for a configuration that was refused: %s %s", r.Method, r.URL.Path)
	})
	flows := mustFlowArray(t, `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"n1","type":"inject","z":"t1","x":140,"y":140,"wires":[["ghost"]]}
	]`)
	err := c.SetFlows(context.Background(), flows)
	if err == nil {
		t.Fatal("expected SetFlows to refuse a dangling wire, got nil")
	}
	var invalid *ConfigValidationError
	if !asConfigValidationError(err, &invalid) {
		t.Fatalf("expected a ConfigValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("expected the error to name the dangling target: %v", err)
	}
}

// A full deployment that instances a subflow without its definition is
// refused: the runtime would install a node type it cannot resolve and
// then refuse to start the flows.
func TestSetFlows_RefusesSubflowInstanceWithoutDefinition(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the runtime was contacted for a configuration that was refused: %s %s", r.Method, r.URL.Path)
	})
	flows := mustFlowArray(t, `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"inst","type":"subflow:sf_a","z":"t1","x":140,"y":140,"wires":[]}
	]`)
	err := c.SetFlows(context.Background(), flows)
	if err == nil {
		t.Fatal("expected SetFlows to refuse a subflow instance without a definition, got nil")
	}
	if !strings.Contains(err.Error(), "sf_a") {
		t.Errorf("expected the error to name the subflow: %v", err)
	}
}

// A valid configuration is deployed: the guard does not get in the way
// of a configuration whose references all resolve.
func TestSetFlows_DeploysAValidConfiguration(t *testing.T) {
	var posted bool
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[{"id":"cur","type":"tab","label":"Current"}]`))
		case r.Method == "POST" && r.URL.Path == "/flows":
			posted = true
			if got := r.Header.Get("Node-RED-Deployment-Type"); got != "full" {
				t.Errorf("expected a full deployment, got %q", got)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	flows := mustFlowArray(t, `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"n1","type":"inject","z":"t1","x":140,"y":140,"wires":[["n2"]]},
	  {"id":"n2","type":"debug","z":"t1","x":300,"y":140,"wires":[]}
	]`)
	if err := c.SetFlows(context.Background(), flows); err != nil {
		t.Fatalf("SetFlows: %v", err)
	}
	if !posted {
		t.Error("the configuration was not deployed")
	}
}

// A restore of a backup whose references do not resolve is refused too.
func TestRestoreFlows_RefusesBackupWithDanglingWire(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the runtime was contacted for a backup that was refused: %s %s", r.Method, r.URL.Path)
	})
	backup := RawFlow(`[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"n1","type":"inject","z":"t1","x":140,"y":140,"wires":[["ghost"]]}
	]`)
	err := c.RestoreFlows(context.Background(), backup)
	if err == nil {
		t.Fatal("expected RestoreFlows to refuse a dangling wire, got nil")
	}
	var invalid *ConfigValidationError
	if !asConfigValidationError(err, &invalid) {
		t.Fatalf("expected a ConfigValidationError, got %T: %v", err, err)
	}
}

// The recovery path: a backup that no longer validates can be
// installed on purpose, and it is deployed.
func TestRestoreFlowsUnchecked_DeploysABackupThatDoesNotValidate(t *testing.T) {
	var posted bool
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[{"id":"cur","type":"tab","label":"Current"}]`))
		case r.Method == "POST" && r.URL.Path == "/flows":
			posted = true
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "ghost") {
				t.Errorf("the backup was not deployed as it was: %s", body)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	backup := RawFlow(`[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"n1","type":"inject","z":"t1","x":140,"y":140,"wires":[["ghost"]]}
	]`)
	if err := c.RestoreFlowsUnchecked(context.Background(), backup); err != nil {
		t.Fatalf("RestoreFlowsUnchecked: %v", err)
	}
	if !posted {
		t.Error("the backup was not deployed")
	}
}

// A subflow definition with a wire that names a node which is not in it
// is refused, and the global configuration is never read.
func TestCreateSubflow_RefusesDefinitionWithDanglingWire(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the runtime was contacted for a definition that was refused: %s %s", r.Method, r.URL.Path)
	})
	def := json.RawMessage(`{
	  "id":"sf_a","type":"subflow","name":"A","in":[],"out":[],
	  "nodes":[
	    {"id":"inner","type":"inject","z":"sf_a","x":140,"y":140,"wires":[["ghost"]]}
	  ]
	}`)
	_, err := c.CreateSubflow(context.Background(), def)
	if err == nil {
		t.Fatal("expected CreateSubflow to refuse a dangling wire, got nil")
	}
	var guard *FlowGuardError
	if !asFlowGuardError(err, &guard) {
		t.Fatalf("expected a FlowGuardError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("expected the error to name the dangling target: %v", err)
	}
}

// A subflow definition whose node claims to live in another tab is
// refused: the runtime would file the node there and the subflow would
// come out empty.
func TestUpdateSubflow_RefusesDefinitionWhoseNodeBelongsToAnotherTab(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the runtime was contacted for a definition that was refused: %s %s", r.Method, r.URL.Path)
	})
	def := json.RawMessage(`{
	  "id":"sf_a","type":"subflow","name":"A","in":[],"out":[],
	  "nodes":[
	    {"id":"inner","type":"inject","z":"t1","x":140,"y":140,"wires":[]}
	  ]
	}`)
	err := c.UpdateSubflow(context.Background(), "sf_a", def)
	if err == nil {
		t.Fatal("expected UpdateSubflow to refuse a node that belongs to another tab, got nil")
	}
	var guard *FlowGuardError
	if !asFlowGuardError(err, &guard) {
		t.Fatalf("expected a FlowGuardError, got %T: %v", err, err)
	}
}

// A subflow definition whose references resolve is written: the guard
// does not get in the way of a valid definition.
func TestCreateSubflow_DeploysAValidDefinition(t *testing.T) {
	var put bool
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			// the snapshot taken before the write
			_, _ = w.Write([]byte(`[{"id":"cur","type":"tab","label":"Current"}]`))
		case r.Method == "GET" && r.URL.Path == "/flow/global":
			_, _ = w.Write([]byte(emptyGlobalSubflowsBody))
		case r.Method == "PUT" && r.URL.Path == "/flow/global":
			put = true
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	def := json.RawMessage(`{
	  "id":"sf_a","type":"subflow","name":"A","in":[],"out":[],
	  "nodes":[
	    {"id":"inner","type":"inject","z":"sf_a","x":140,"y":140,"wires":[["inner2"]]},
	    {"id":"inner2","type":"debug","z":"sf_a","x":300,"y":140,"wires":[]}
	  ]
	}`)
	if _, err := c.CreateSubflow(context.Background(), def); err != nil {
		t.Fatalf("CreateSubflow: %v", err)
	}
	if !put {
		t.Error("the definition was not written")
	}
}

// asConfigValidationError reports whether err is a ConfigValidationError
// and binds it. errors.As with a typed target, without importing
// errors in every test.
func asConfigValidationError(err error, target **ConfigValidationError) bool {
	return errors.As(err, target)
}

// asFlowGuardError reports whether err is a FlowGuardError and binds it.
func asFlowGuardError(err error, target **FlowGuardError) bool {
	return errors.As(err, target)
}
