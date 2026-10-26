package nodered

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// externalChangeRuntime models a runtime whose configuration another editor
// changes while a granular edit is being prepared.
//
// The nested GET /flow/:id serves the state *before* the change and the v2
// GET /flows the state *after* it, with a new revision. That is the pair of
// reads a granular edit used to make, and the window in which the change
// was lost: the edit was built on the first read and deployed with the
// revision of the second.
//
// The revision is the runtime's own contract: a POST that carries the
// revision of the state it was built on is accepted, and one that carries a
// stale revision is answered with 409.
type externalChangeRuntime struct {
	before    []json.RawMessage // the state the nested GET serves
	after     []json.RawMessage // the state the v2 GET serves
	rev       string            // the revision of after
	late      []json.RawMessage // the state installed after the v2 GET
	lateRev   string            // the revision of late
	requests  []string
	posted    []json.RawMessage
	postedRev string
	server    *httptest.Server
}

// externalChangeConfig is a tab whose node carries a topic: the property
// another editor changes, next to the name the granular edit patches.
func externalChangeConfig(topic string) []json.RawMessage {
	return []json.RawMessage{
		json.RawMessage(`{"id":"a","type":"tab","label":"A"}`),
		json.RawMessage(`{"id":"n","type":"inject","z":"a","x":100,"y":80,"name":"before","topic":"` + topic + `","wires":[]}`),
	}
}

func newExternalChangeRuntime(t *testing.T) *externalChangeRuntime {
	t.Helper()
	rt := &externalChangeRuntime{
		before: externalChangeConfig("old"),
		after:  externalChangeConfig("EXTERNAL-NEW"),
		rev:    "rev-2",
	}
	rt.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt.requests = append(rt.requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/a":
			_, _ = w.Write([]byte(`{"id":"a","label":"A","nodes":[` + string(rt.before[1]) + `],"configs":[]}`))
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`{"rev":"` + rt.rev + `","flows":` + rawJSON(rt.after) + `}`))
			// The other editor wins the race right after the read.
			if rt.late != nil {
				rt.rev = rt.lateRev
				rt.after = rt.late
			}
		case r.Method == "POST" && r.URL.Path == "/flows":
			var body struct {
				Rev   string            `json:"rev"`
				Flows []json.RawMessage `json:"flows"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding deployment: %v", err)
			}
			rt.postedRev = body.Rev
			if body.Rev != rt.rev {
				// The runtime refuses a deployment built on a revision
				// that is no longer current.
				http.Error(w, `{"code":"version_mismatch"}`, http.StatusConflict)
				return
			}
			rt.posted = body.Flows
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	return rt
}

// rawJSON renders the entries as a JSON array.
func rawJSON(items []json.RawMessage) string {
	out, _ := json.Marshal(items)
	return string(out)
}

// postedNode returns the node with the given id from the deployed array.
func (rt *externalChangeRuntime) postedNode(t *testing.T, id string) map[string]any {
	t.Helper()
	for _, raw := range rt.posted {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		if m["id"] == id {
			return m
		}
	}
	t.Fatalf("node %q was not deployed: %s", id, rawJSON(rt.posted))
	return nil
}

// TestGranularEditKeepsAnExternalChangeToAnotherProperty is the regression
// for issue #345: a change another editor made to a property the edit did
// not touch must survive the edit.
func TestGranularEditKeepsAnExternalChangeToAnotherProperty(t *testing.T) {
	rt := newExternalChangeRuntime(t)
	defer rt.server.Close()
	c := clientWithBackup(t, rt.server.URL)

	err := c.UpdateNode(context.Background(), "a", "n", map[string]json.RawMessage{
		"name": json.RawMessage(`"after"`),
	})
	if err != nil {
		t.Fatal(err)
	}
	node := rt.postedNode(t, "n")
	if node["topic"] != "EXTERNAL-NEW" {
		t.Errorf("the external change was lost: topic = %v, want EXTERNAL-NEW", node["topic"])
	}
	if node["name"] != "after" {
		t.Errorf("the patch was not applied: name = %v, want after", node["name"])
	}
	if rt.postedRev != "rev-2" {
		t.Errorf("deployed with revision %q, want rev-2", rt.postedRev)
	}
	// The base of the edit, the configuration and the revision come from
	// one read: the nested endpoint is a second, different snapshot.
	for _, req := range rt.requests {
		if strings.Contains(req, "/flow/") {
			t.Errorf("the edit read a second snapshot: %v", rt.requests)
			break
		}
	}
}

// TestGranularEditConflictAfterTheSnapshotIsNotReplayed covers the other
// half of the contract: a change that lands after the snapshot is refused
// with the runtime's own 409, and the edit is not replayed on top of it.
func TestGranularEditConflictAfterTheSnapshotIsNotReplayed(t *testing.T) {
	rt := newExternalChangeRuntime(t)
	rt.late = externalChangeConfig("EXTERNAL-LATE")
	rt.lateRev = "rev-3"
	defer rt.server.Close()
	c := clientWithBackup(t, rt.server.URL)

	err := c.UpdateNode(context.Background(), "a", "n", map[string]json.RawMessage{
		"name": json.RawMessage(`"after"`),
	})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict {
		t.Fatalf("want a 409 conflict, got %v", err)
	}
	if rt.posted != nil {
		t.Errorf("a conflicting deployment was accepted: %s", rawJSON(rt.posted))
	}
	posts := 0
	for _, req := range rt.requests {
		if strings.HasPrefix(req, "POST") {
			posts++
		}
	}
	if posts != 1 {
		t.Errorf("the edit was replayed after the conflict: %v", rt.requests)
	}
}

// TestGranularEditAcceptsALinkOutWhoseWiresTheRuntimeOwns covers the other
// shape the snapshot brings with it: the runtime owns the wires of a link
// out and recomputes them, so what the file carries is not a dangling wire,
// and an edit of that tab must not be refused because of it.
func TestGranularEditAcceptsALinkOutWhoseWiresTheRuntimeOwns(t *testing.T) {
	var posted []json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`{"rev":"rev-1","flows":[
				{"id":"a","type":"tab","label":"A"},
				{"id":"link","type":"link out","z":"a","x":100,"y":80,"links":["other"],"wires":[["ghost"]]},
				{"id":"b","type":"tab","label":"B"},
				{"id":"ghost","type":"debug","z":"b","x":10,"y":10}
			]}`))
		case r.Method == "POST" && r.URL.Path == "/flows":
			var body struct {
				Flows []json.RawMessage `json:"flows"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding deployment: %v", err)
			}
			posted = body.Flows
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	c := clientWithBackup(t, srv.URL)
	err := c.UpdateNode(context.Background(), "a", "link", map[string]json.RawMessage{
		"name": json.RawMessage(`"renamed"`),
	})
	if err != nil {
		t.Fatalf("the edit was refused: %v", err)
	}
	for _, raw := range posted {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		if m["id"] == "link" && m["name"] != "renamed" {
			t.Errorf("the patch was not applied: %s", raw)
		}
	}
}
