package nodered

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestCommsURL(t *testing.T) {
	tests := []struct {
		base, want string
		wantErr    bool
	}{
		{base: "http://localhost:1880", want: "ws://localhost:1880/comms"},
		{base: "https://nodered.example.com", want: "wss://nodered.example.com/comms"},
		{base: "http://localhost:1880/", want: "ws://localhost:1880/comms"},
		// Node-RED behind a path prefix: /comms hangs off the same root.
		{base: "http://host/nodered", want: "ws://host/nodered/comms"},
		{base: "http://host/nodered/", want: "ws://host/nodered/comms"},
		{base: "ftp://host", wantErr: true},
		{base: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.base, func(t *testing.T) {
			got, err := commsURL(tc.base)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %q, got %q", tc.base, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("commsURL(%q): %v", tc.base, err)
			}
			if got != tc.want {
				t.Errorf("commsURL(%q) = %q, want %q", tc.base, got, tc.want)
			}
		})
	}
}

func TestRingKeepsNewestAndReportsDrops(t *testing.T) {
	tail := newDebugTail("ws://x/comms", "", false, 3)

	for i := 1; i <= 5; i++ {
		tail.record(json.RawMessage(fmt.Sprintf(`{"n":%d}`, i)))
	}

	snap := tail.Snapshot(10, time.Time{})
	if len(snap.Messages) != 3 {
		t.Fatalf("expected the buffer to hold 3, got %d", len(snap.Messages))
	}
	// Oldest first, and 1 and 2 must have fallen off the back.
	for i, want := range []string{`{"n":3}`, `{"n":4}`, `{"n":5}`} {
		if string(snap.Messages[i].Data) != want {
			t.Errorf("position %d = %s, want %s", i, snap.Messages[i].Data, want)
		}
	}
	if snap.Received != 5 {
		t.Errorf("Received = %d, want 5", snap.Received)
	}
	// Silent loss is the failure mode to avoid: 2 messages are gone and the
	// caller must be able to tell.
	if snap.Dropped != 2 {
		t.Errorf("Dropped = %d, want 2", snap.Dropped)
	}
}

func TestRingBelowCapacityDropsNothing(t *testing.T) {
	tail := newDebugTail("ws://x/comms", "", false, 10)
	tail.record(json.RawMessage(`{"n":1}`))
	tail.record(json.RawMessage(`{"n":2}`))

	snap := tail.Snapshot(10, time.Time{})
	if len(snap.Messages) != 2 || snap.Dropped != 0 || snap.Received != 2 {
		t.Errorf("unexpected snapshot: %d messages, %d dropped, %d received",
			len(snap.Messages), snap.Dropped, snap.Received)
	}
}

func TestSnapshotLimitReturnsNewest(t *testing.T) {
	tail := newDebugTail("ws://x/comms", "", false, 10)
	for i := 1; i <= 5; i++ {
		tail.record(json.RawMessage(fmt.Sprintf(`{"n":%d}`, i)))
	}

	// A limit must yield the most recent ones — asking for "the last 2" and
	// getting the two oldest would be worse than useless when debugging.
	snap := tail.Snapshot(2, time.Time{})
	if len(snap.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(snap.Messages))
	}
	if string(snap.Messages[0].Data) != `{"n":4}` || string(snap.Messages[1].Data) != `{"n":5}` {
		t.Errorf("limit returned the wrong window: %s, %s", snap.Messages[0].Data, snap.Messages[1].Data)
	}
	// Buffered still reports everything held, so the caller knows more exists.
	if snap.Buffered != 5 {
		t.Errorf("Buffered = %d, want 5", snap.Buffered)
	}
}

func TestSnapshotSinceFiltersByTime(t *testing.T) {
	tail := newDebugTail("ws://x/comms", "", false, 10)
	tail.record(json.RawMessage(`{"n":1}`))
	time.Sleep(10 * time.Millisecond)
	cut := time.Now()
	time.Sleep(10 * time.Millisecond)
	tail.record(json.RawMessage(`{"n":2}`))

	snap := tail.Snapshot(10, cut)
	if len(snap.Messages) != 1 {
		t.Fatalf("expected 1 message after the cutoff, got %d", len(snap.Messages))
	}
	if string(snap.Messages[0].Data) != `{"n":2}` {
		t.Errorf("since returned the wrong message: %s", snap.Messages[0].Data)
	}
}

// fakeComms stands in for Node-RED's /comms endpoint. It records what the
// client sent and pushes back whatever frames the test supplies.
func fakeComms(t *testing.T, requireAuth bool, push []string, got *[]string) *httptest.Server {
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

		if requireAuth {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			*got = append(*got, string(data))
			if err := conn.Write(ctx, websocket.MessageText, []byte(`{"auth":"ok"}`)); err != nil {
				return
			}
		}
		// The subscribe frame.
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		*got = append(*got, string(data))

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

func waitFor(t *testing.T, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestTailSubscribesAndCollectsDebugMessages(t *testing.T) {
	var sent []string
	push := []string{
		// The real envelope: an array of {topic,data} objects.
		`[{"topic":"debug","data":{"id":"n1","name":"debug 1","msg":"hello","format":"string"}}]`,
		// A heartbeat, which must not be mistaken for output.
		`[{"topic":"hb","data":1730000000}]`,
		// Two debug messages arriving in one frame.
		`[{"topic":"debug","data":{"id":"n2","msg":"a"}},{"topic":"debug","data":{"id":"n3","msg":"b"}}]`,
	}
	srv := fakeComms(t, false, push, &sent)

	tail := newDebugTail(strings.Replace(srv.URL, "http://", "ws://", 1)+"/comms", "", false, 100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tail.Run(ctx)

	if !waitFor(t, func() bool { return tail.Snapshot(100, time.Time{}).Received == 3 }) {
		t.Fatalf("expected 3 debug messages, got %d", tail.Snapshot(100, time.Time{}).Received)
	}

	if len(sent) != 1 || !strings.Contains(sent[0], `"subscribe"`) || !strings.Contains(sent[0], "debug") {
		t.Errorf("expected a single subscribe frame for the debug topic, got %v", sent)
	}

	snap := tail.Snapshot(100, time.Time{})
	for _, m := range snap.Messages {
		if strings.Contains(string(m.Data), "1730000000") {
			t.Error("a heartbeat was recorded as a debug message")
		}
	}
	if !strings.Contains(string(snap.Messages[0].Data), "hello") {
		t.Errorf("first message lost its payload: %s", snap.Messages[0].Data)
	}
}

func TestTailAuthenticatesWhenTokenIsSet(t *testing.T) {
	var sent []string
	srv := fakeComms(t, true, []string{`[{"topic":"debug","data":{"msg":"x"}}]`}, &sent)

	tail := newDebugTail(strings.Replace(srv.URL, "http://", "ws://", 1)+"/comms", "secret-token", false, 100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tail.Run(ctx)

	if !waitFor(t, func() bool { return tail.Snapshot(100, time.Time{}).Received == 1 }) {
		t.Fatal("no message collected; the auth handshake likely failed")
	}
	if len(sent) < 2 {
		t.Fatalf("expected an auth frame then a subscribe frame, got %v", sent)
	}
	if !strings.Contains(sent[0], `"auth"`) || !strings.Contains(sent[0], "secret-token") {
		t.Errorf("first frame should carry the token, got %q", sent[0])
	}
	if !strings.Contains(sent[1], `"subscribe"`) {
		t.Errorf("second frame should be the subscription, got %q", sent[1])
	}
}

// An unreachable Node-RED must never take the MCP server down with it: Run
// keeps retrying and Snapshot keeps answering.
func TestTailSurvivesAnUnreachableServer(t *testing.T) {
	tail := newDebugTail("ws://127.0.0.1:1/comms", "", false, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tail.Run(ctx)

	if !waitFor(t, func() bool { return tail.Snapshot(10, time.Time{}).LastError != "" }) {
		t.Error("expected the connection failure to be reported in the snapshot")
	}
	snap := tail.Snapshot(10, time.Time{})
	if snap.Connected {
		t.Error("Connected should be false while the server is unreachable")
	}
	if len(snap.Messages) != 0 {
		t.Error("no messages should have been collected")
	}
}

func TestTailStopsWhenContextIsCancelled(t *testing.T) {
	var sent []string
	srv := fakeComms(t, false, nil, &sent)

	tail := newDebugTail(strings.Replace(srv.URL, "http://", "ws://", 1)+"/comms", "", false, 10)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() { tail.Run(ctx); close(done) }()

	waitFor(t, func() bool { return tail.Snapshot(10, time.Time{}).Connected })
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
