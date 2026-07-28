package nodered

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestClassify covers the mapping the editor uses. The dot under a
// node is the operator's only clue for "is this thing alive?"; the
// MCP must agree with the editor on what each colour/shape means.
func TestClassify(t *testing.T) {
	tests := []struct {
		name        string
		fill, shape string
		cleared     bool
		want        Status
	}{
		{"green dot = connected", "green", "dot", false, StatusConnected},
		{"green ring = connecting", "green", "ring", false, StatusConnecting},
		{"yellow = reconnecting", "yellow", "dot", false, StatusReconnecting},
		{"red = errored", "red", "dot", false, StatusErrored},
		{"red ring = errored", "red", "ring", false, StatusErrored},
		{"blue = info", "blue", "dot", false, StatusInfo},
		{"grey = disconnected", "grey", "dot", false, StatusDisconnected},
		{"gray = disconnected", "gray", "dot", false, StatusDisconnected},
		{"empty fill = disconnected", "", "", false, StatusDisconnected},
		{"cleared = disconnected regardless of fill", "green", "dot", true, StatusDisconnected},
		{"uppercase green still connected", "GREEN", "dot", false, StatusConnected},
		{"unknown fill = disconnected (no guess)", "magenta", "dot", false, StatusDisconnected},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify(tc.fill, tc.shape, tc.cleared); got != tc.want {
				t.Errorf("classify(%q, %q, cleared=%v) = %q, want %q",
					tc.fill, tc.shape, tc.cleared, got, tc.want)
			}
		})
	}
}

// TestRecordStoresEntryById covers the basic write: a status
// event for a node id we have not seen is recorded, and Lookup
// returns it.
func TestRecordStoresEntryById(t *testing.T) {
	tail := newStatusTail("ws://x/comms", "", false)
	tail.record(statusEvent{
		id:   "n1",
		text: "ok",
		fill: "green", shape: "dot",
	})

	got, ok := tail.Lookup("n1")
	if !ok {
		t.Fatal("expected an entry for n1")
	}
	if got.Status != StatusConnected {
		t.Errorf("status = %q, want connected", got.Status)
	}
	if got.Text != "ok" {
		t.Errorf("text = %q, want ok", got.Text)
	}
	if got.Fill != "green" || got.Shape != "dot" {
		t.Errorf("fill/shape lost: %q/%q", got.Fill, got.Shape)
	}
	if got.UpdatedAt.IsZero() {
		t.Errorf("UpdatedAt should be set")
	}
}

// TestRecordLastWriteWins covers the "remember the latest" rule:
// two events for the same id overwrite, the first one is gone.
func TestRecordLastWriteWins(t *testing.T) {
	tail := newStatusTail("ws://x/comms", "", false)
	tail.record(statusEvent{id: "n1", text: "first", fill: "green", shape: "dot"})
	tail.record(statusEvent{id: "n1", text: "second", fill: "yellow", shape: "dot"})

	got, _ := tail.Lookup("n1")
	if got.Text != "second" {
		t.Errorf("last write should win, got text=%q", got.Text)
	}
	if got.Status != StatusReconnecting {
		t.Errorf("status = %q, want reconnecting (yellow)", got.Status)
	}
}

// TestRecordClearedMarksDisconnected covers the runtime's "null out"
// event: text/fill/shape all empty means the node is no longer
// reporting a status. We classify that as disconnected and DO NOT
// keep the old fill/shape on the entry — they would be stale.
func TestRecordClearedMarksDisconnected(t *testing.T) {
	tail := newStatusTail("ws://x/comms", "", false)
	tail.record(statusEvent{id: "n1", text: "ok", fill: "green", shape: "dot"})
	tail.record(statusEvent{id: "n1", cleared: true})

	got, _ := tail.Lookup("n1")
	if got.Status != StatusDisconnected {
		t.Errorf("cleared event should be disconnected, got %q", got.Status)
	}
	if got.Fill != "" || got.Shape != "" {
		t.Errorf("cleared event should wipe fill/shape, got %q/%q", got.Fill, got.Shape)
	}
}

// TestRecordLastErrorSurvivesRecovery covers the audit's headline
// scenario: a node errored, the operator wants to know what the
// error was, the node is now back to green. The LastError field
// keeps the error text alive even after the status flipped to
// connected, so a model that asks "why was this node red?" still
// has the answer.
func TestRecordLastErrorSurvivesRecovery(t *testing.T) {
	tail := newStatusTail("ws://x/comms", "", false)
	tail.record(statusEvent{id: "n1", text: "broker unreachable", fill: "red", shape: "dot"})
	tail.record(statusEvent{id: "n1", text: "ok", fill: "green", shape: "dot"})

	got, _ := tail.Lookup("n1")
	if got.Status != StatusConnected {
		t.Errorf("status = %q, want connected (recovered)", got.Status)
	}
	if got.LastError != "broker unreachable" {
		t.Errorf("LastError = %q, want the error to survive recovery", got.LastError)
	}
}

// TestRecordLastErrorUpdatesOnFreshError covers the other half of
// the rule: a second errored event updates LastError to the new
// error text. The first error is gone; we only keep one.
func TestRecordLastErrorUpdatesOnFreshError(t *testing.T) {
	tail := newStatusTail("ws://x/comms", "", false)
	tail.record(statusEvent{id: "n1", text: "first error", fill: "red", shape: "dot"})
	tail.record(statusEvent{id: "n1", text: "second error", fill: "red", shape: "dot"})

	got, _ := tail.Lookup("n1")
	if got.LastError != "second error" {
		t.Errorf("LastError = %q, want the latest error", got.LastError)
	}
}

// TestLookupUnknownReturnsNotFound covers the audit's "never-seen"
// case: a model asks about a node id that the runtime has not
// reported any status for. The handler must render this as
// "unknown" rather than "disconnected" (the latter would imply a
// transition we cannot prove).
func TestLookupUnknownReturnsNotFound(t *testing.T) {
	tail := newStatusTail("ws://x/comms", "", false)
	tail.record(statusEvent{id: "n1", text: "ok", fill: "green", shape: "dot"})

	if _, ok := tail.Lookup("n_unknown"); ok {
		t.Errorf("expected no entry for an id the tail has never seen")
	}
}

// TestSnapshotWholeCache covers Snapshot() with no filter: every
// entry in the cache comes back. We assert on Tracked and the
// per-entry status, not on ordering (the map iteration is
// deliberately unordered).
func TestSnapshotWholeCache(t *testing.T) {
	tail := newStatusTail("ws://x/comms", "", false)
	tail.record(statusEvent{id: "n1", text: "ok", fill: "green", shape: "dot"})
	tail.record(statusEvent{id: "n2", text: "broken", fill: "red", shape: "dot"})

	snap := tail.Snapshot()
	if snap.Tracked != 2 {
		t.Errorf("Tracked = %d, want 2", snap.Tracked)
	}
	if len(snap.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(snap.Entries))
	}
	// Find n2 and assert it is errored.
	for _, e := range snap.Entries {
		if e.ID == "n2" {
			if e.Status != StatusErrored {
				t.Errorf("n2 status = %q, want errored", e.Status)
			}
		}
	}
}

// TestSnapshotFilterOmitsUnknown covers the flow-filtered case:
// Snapshot(ids...) returns only the entries the cache knows
// about. An id the cache has never seen is omitted (Tracked
// still reflects the cache size, so the caller can tell the
// difference between "no nodes match" and "no nodes tracked").
func TestSnapshotFilterOmitsUnknown(t *testing.T) {
	tail := newStatusTail("ws://x/comms", "", false)
	tail.record(statusEvent{id: "n1", text: "ok", fill: "green", shape: "dot"})

	snap := tail.Snapshot("n1", "n_unknown")
	if len(snap.Entries) != 1 {
		t.Fatalf("expected 1 entry (n_unknown is not in the cache), got %d", len(snap.Entries))
	}
	if snap.Entries[0].ID != "n1" {
		t.Errorf("entry id = %q, want n1", snap.Entries[0].ID)
	}
	if snap.Tracked != 1 {
		t.Errorf("Tracked = %d, want 1 (cache size, not filter size)", snap.Tracked)
	}
}

// TestConsumeParsesStatusTopic covers the parse path: one /comms
// frame, array envelope, one entry on status/n1.
func TestConsumeParsesStatusTopic(t *testing.T) {
	tail := newStatusTail("ws://x/comms", "", false)
	tail.consume([]byte(
		`[{"topic":"status/n1","data":{"text":"hello","fill":"green","shape":"dot"}}]`,
	))

	got, ok := tail.Lookup("n1")
	if !ok {
		t.Fatal("expected an entry after a status event")
	}
	if got.Status != StatusConnected || got.Text != "hello" {
		t.Errorf("entry lost data: %+v", got)
	}
}

// TestConsumeIgnoresOtherTopics covers the other side of the
// filter: debug, hb, and other comms topics do not pollute the
// status cache. The handler for get_node_status must only ever
// see data that came from a status/* frame.
func TestConsumeIgnoresOtherTopics(t *testing.T) {
	tail := newStatusTail("ws://x/comms", "", false)
	tail.consume([]byte(
		`[{"topic":"debug","data":{"msg":"hello"}},{"topic":"hb","data":1730000000}]`,
	))

	if snap := tail.Snapshot(); snap.Tracked != 0 {
		t.Errorf("non-status topics should not enter the cache, got %d tracked", snap.Tracked)
	}
}

// TestStatusConsumeAcceptsSingleObjectFrame covers the same
// envelope flexibility DebugTail.consume does: a single-object
// frame is a legitimate shape on a late auth reply, and we
// must not crash on it.
func TestStatusConsumeAcceptsSingleObjectFrame(t *testing.T) {
	tail := newStatusTail("ws://x/comms", "", false)
	tail.consume([]byte(
		`{"topic":"status/n1","data":{"text":"x","fill":"red","shape":"dot"}}`,
	))

	got, _ := tail.Lookup("n1")
	if got.Status != StatusErrored {
		t.Errorf("status = %q, want errored", got.Status)
	}
}

// TestConsumeIgnoresBareStatusTopic covers the prefix match: a
// frame on topic "status" (no id) is not recorded. The wire
// never sends it, but the prefix check is the line of defence
// against a misbehaving runtime.
func TestConsumeIgnoresBareStatusTopic(t *testing.T) {
	tail := newStatusTail("ws://x/comms", "", false)
	tail.consume([]byte(
		`[{"topic":"status","data":{"text":"x","fill":"red","shape":"dot"}}]`,
	))

	if snap := tail.Snapshot(); snap.Tracked != 0 {
		t.Errorf("bare status topic should not be recorded, got %d tracked", snap.Tracked)
	}
}

// TestConsumeIgnoresUnparseablePayload covers the runtime sending
// bytes we cannot decode. The audit calls this out: a model
// that asks "why is the cache empty?" needs an answer, and "the
// runtime sent garbage" is one; we log it at debug and move on.
func TestConsumeIgnoresUnparseablePayload(t *testing.T) {
	tail := newStatusTail("ws://x/comms", "", false)
	tail.consume([]byte(
		`[{"topic":"status/n1","data":"not an object"}]`,
	))

	if _, ok := tail.Lookup("n1"); ok {
		t.Errorf("unparseable payload should not enter the cache")
	}
}

// TestParseFlowNodeIDs covers the helper that joins the
// per-flow node list (from GET /flow/:id) with the status
// cache. The audit's get_node_status "all nodes in this flow"
// path depends on this.
func TestParseFlowNodeIDs(t *testing.T) {
	t.Run("nested v2 shape", func(t *testing.T) {
		body := []byte(`{"id":"tabA","nodes":[{"id":"n1"},{"id":"n2"}]}`)
		ids := ParseFlowNodeIDs(body)
		if len(ids) != 2 || ids[0] != "n1" || ids[1] != "n2" {
			t.Errorf("ids = %v, want [n1 n2]", ids)
		}
	})
	t.Run("flat v1 shape", func(t *testing.T) {
		body := []byte(`[{"id":"n1"},{"id":"n2","z":"tabA"}]`)
		ids := ParseFlowNodeIDs(body)
		if len(ids) != 2 {
			t.Errorf("ids = %v, want 2 entries", ids)
		}
	})
	t.Run("garbage yields no ids", func(t *testing.T) {
		ids := ParseFlowNodeIDs([]byte(`not json`))
		if ids != nil {
			t.Errorf("garbage input should yield nil, got %v", ids)
		}
	})
	t.Run("ignores entries with no id", func(t *testing.T) {
		body := []byte(`[{"id":"n1"},{"name":"no-id"}]`)
		ids := ParseFlowNodeIDs(body)
		if len(ids) != 1 || ids[0] != "n1" {
			t.Errorf("ids = %v, want [n1]", ids)
		}
	})
}

// TestStatusTailRoundTripWithFakeComms covers the wire-level
// end-to-end: open a real (httptest) WebSocket, send a status
// frame, verify the cache picks it up. This is the test the
// audit wants to see: the WebSocket path is exercised, not
// just the in-memory helpers.
func TestStatusTailRoundTripWithFakeComms(t *testing.T) {
	srv := httptestStatusServer(t, []string{
		`[{"topic":"status/n1","data":{"text":"hi","fill":"green","shape":"dot"}}]`,
		`[{"topic":"status/n2","data":{"text":"err","fill":"red","shape":"dot"}}]`,
	})

	tail := newStatusTail(strings.Replace(srv.URL, "http://", "ws://", 1)+"/comms", "", false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tail.Run(ctx)

	if !waitFor(t, func() bool {
		_, ok1 := tail.Lookup("n1")
		_, ok2 := tail.Lookup("n2")
		return ok1 && ok2
	}) {
		t.Fatal("tail did not pick up both events within the deadline")
	}

	n1, _ := tail.Lookup("n1")
	if n1.Status != StatusConnected {
		t.Errorf("n1 status = %q, want connected", n1.Status)
	}
	n2, _ := tail.Lookup("n2")
	if n2.Status != StatusErrored {
		t.Errorf("n2 status = %q, want errored", n2.Status)
	}
	if n2.LastError != "err" {
		t.Errorf("n2 LastError = %q, want err", n2.LastError)
	}
}

// TestStatusTailReconnectsAfterDrop covers the audit's resilience
// requirement: a Node-RED that bounces the WebSocket (a deploy
// does this) must be picked up again without operator action.
// We force a close from the server side, then verify the tail
// reports Connected=false and the error is recorded.
func TestStatusTailReconnectsAfterDrop(t *testing.T) {
	// closer is a hook the test can use to drop the WebSocket
	// from the server side, which is what a Node-RED restart
	// looks like to the tail. httptest.Server.Close() does
	// not always close existing connections, so we go
	// through the connection object directly.
	connCh := make(chan *websocket.Conn, 1)
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		connCh <- conn
		<-r.Context().Done()
	}))
	t.Cleanup(srv1.Close)

	tail := newStatusTail(strings.Replace(srv1.URL, "http://", "ws://", 1)+"/comms", "", false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tail.Run(ctx)

	// Wait for the tail to have an active connection we can close.
	var conn *websocket.Conn
	select {
	case conn = <-connCh:
	case <-time.After(3 * time.Second):
		t.Fatal("server never accepted a connection")
	}

	if !waitFor(t, func() bool { return tail.Snapshot().Connected }) {
		t.Fatal("tail never reported Connected")
	}

	// Drop the WebSocket from the server side.
	_ = conn.Close(websocket.StatusGoingAway, "test forced reconnect")

	if !waitFor(t, func() bool { return !tail.Snapshot().Connected }) {
		t.Fatal("tail did not notice the connection drop")
	}
	if snap := tail.Snapshot(); snap.LastError == "" {
		t.Error("expected the drop to record a lastError")
	}
}

// TestStatusTailSurvivesAnUnreachableServer covers the same
// resilience check DebugTail does: an unreachable server must
// not take the MCP down.
func TestStatusTailSurvivesAnUnreachableServer(t *testing.T) {
	tail := newStatusTail("ws://127.0.0.1:1/comms", "", false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tail.Run(ctx)

	if !waitFor(t, func() bool { return tail.Snapshot().LastError != "" }) {
		t.Error("expected the connection failure to be reported in the snapshot")
	}
	if snap := tail.Snapshot(); snap.Connected {
		t.Error("Connected should be false while the server is unreachable")
	}
}

// TestStatusTailStopsWhenContextCancelled covers the lifecycle
// guard: a tail that is told to stop (server shutdown) must
// return from Run.
func TestStatusTailStopsWhenContextCancelled(t *testing.T) {
	srv := httptestStatusServer(t, nil)
	tail := newStatusTail(strings.Replace(srv.URL, "http://", "ws://", 1)+"/comms", "", false)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() { tail.Run(ctx); close(done) }()

	waitFor(t, func() bool { return tail.Snapshot().Connected })
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}

// httptestStatusServer stands in for Node-RED's /comms endpoint
// for the StatusTail tests. It accepts any connection on /comms
// and pushes the supplied frames down the wire. Same shape as
// the DebugTail's fakeComms, but specialised for status tests:
// we do not need to capture what the client sent, only deliver
// what we want it to see.
func httptestStatusServer(t *testing.T, push []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/comms" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := r.Context()
		// Drain the subscribe frame so the client does not
		// see a "broken pipe" while it is still queuing
		// its first message.
		_, _, _ = conn.Read(ctx)
		for _, frame := range push {
			if err := conn.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
				return
			}
		}
		<-ctx.Done()
	}))
	t.Cleanup(srv.Close)
	return srv
}
