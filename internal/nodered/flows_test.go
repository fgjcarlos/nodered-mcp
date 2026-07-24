package nodered

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// richFlow carries node-specific properties (topic, broker, qos, func, repeat)
// that a fixed Go struct would drop. The round-trip must preserve every one.
const richFlow = `{
	"id": "tab1",
	"type": "tab",
	"label": "Home",
	"nodes": [
		{"id":"n1","type":"mqtt in","z":"tab1","topic":"home/temp","broker":"b1","qos":"2","wires":[["n2"]]},
		{"id":"n2","type":"function","z":"tab1","func":"return msg;","outputs":1},
		{"id":"n3","type":"inject","z":"tab1","payload":"hello","repeat":"5"}
	]
}`

func clientWithBackup(t *testing.T, url string) *Client {
	t.Helper()
	c, err := NewClient(Options{
		BaseURL:   url,
		Token:     "test-token",
		BackupDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// TestUpdateFlow_RoundTripPreservesFields is the regression test for the
// data-loss bug: reading a flow and writing it back must not drop any
// node-specific field. With a typed Node struct this fails; with opaque
// RawFlow it passes.
func TestUpdateFlow_RoundTripPreservesFields(t *testing.T) {
	var putBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/tab1":
			_, _ = w.Write([]byte(richFlow))
		case r.Method == "GET" && r.URL.Path == "/flows":
			// Backup snapshot fetch.
			_, _ = w.Write([]byte("[" + richFlow + "]"))
		case r.Method == "PUT" && r.URL.Path == "/flow/tab1":
			putBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	ctx := context.Background()

	got, err := c.GetFlow(ctx, "tab1")
	if err != nil {
		t.Fatalf("GetFlow: %v", err)
	}
	if err := c.UpdateFlow(ctx, "tab1", got); err != nil {
		t.Fatalf("UpdateFlow: %v", err)
	}

	// Every node-specific field must survive the round-trip.
	for _, want := range []string{"home/temp", `"broker":"b1"`, `"qos":"2"`, "return msg;", `"repeat":"5"`, `"payload":"hello"`} {
		if !containsJSON(putBody, want) {
			t.Errorf("round-trip lost data: PUT body missing %q\nbody: %s", want, putBody)
		}
	}
}

// TestUpdateFlow_WritesBackupFirst verifies the safety net: a snapshot of the
// current config lands on disk before the flow is modified.
func TestUpdateFlow_WritesBackupFirst(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte("[" + richFlow + "]"))
		case r.Method == "PUT":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(Options{BaseURL: srv.URL, Token: "t", BackupDir: dir})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.UpdateFlow(context.Background(), "tab1", RawFlow(richFlow)); err != nil {
		t.Fatalf("UpdateFlow: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 backup file, got %d", len(entries))
	}
	data, _ := os.ReadFile(dir + "/" + entries[0].Name())
	if !containsJSON(data, "home/temp") {
		t.Errorf("backup does not contain current flows: %s", data)
	}
}

// TestUpdateFlow_AbortsWhenBackupFails checks the fail-closed behaviour: if the
// pre-write snapshot cannot be taken, no write reaches Node-RED.
func TestUpdateFlow_AbortsWhenBackupFails(t *testing.T) {
	var wrote bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/flows" {
			http.Error(w, "down", http.StatusBadGateway) // backup fetch fails
			return
		}
		wrote = true // any write attempt is a failure here
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	if err := c.UpdateFlow(context.Background(), "tab1", RawFlow(richFlow)); err == nil {
		t.Fatal("expected UpdateFlow to fail when backup fails")
	}
	if wrote {
		t.Error("write reached Node-RED despite backup failure (not fail-closed)")
	}
}

func TestValidateFlowWires(t *testing.T) {
	tests := []struct {
		name    string
		flow    string
		wantErr bool
	}{
		{"valid wires", richFlow, false},
		{"empty tab", `{"label":"x","nodes":[]}`, false},
		{"self wire allowed", `{"nodes":[{"id":"a","type":"t","wires":[["a"]]}]}`, false},
		{"dangling wire", `{"nodes":[{"id":"a","type":"t","wires":[["ghost"]]}]}`, true},
		{"not a flow object", `[{"id":"a"}]`, true},
		{"invalid json", `{nope}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFlowWires(RawFlow(tt.flow))
			if (err != nil) != tt.wantErr {
				t.Errorf("validateFlowWires() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestUpdateFlow_RejectsDanglingWire proves the guard aborts the write (and
// therefore never even takes a backup) when a wire points nowhere.
func TestUpdateFlow_RejectsDanglingWire(t *testing.T) {
	var reached bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := clientWithBackup(t, srv.URL)
	bad := RawFlow(`{"nodes":[{"id":"a","type":"inject","wires":[["ghost"]]}]}`)
	if err := c.UpdateFlow(context.Background(), "tab1", bad); err == nil {
		t.Fatal("expected UpdateFlow to reject a dangling wire")
	}
	if reached {
		t.Error("a request reached Node-RED despite invalid wires")
	}
}

// containsJSON reports whether needle appears in the compacted form of body,
// so whitespace differences don't cause false negatives.
func containsJSON(body []byte, needle string) bool {
	var compact bytes.Buffer
	if err := json.Compact(&compact, body); err != nil {
		return bytes.Contains(body, []byte(needle))
	}
	return bytes.Contains(compact.Bytes(), []byte(needle))
}
