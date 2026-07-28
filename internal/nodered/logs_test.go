package nodered

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestGetRuntimeLogs_NotFoundOnStockNodeRed covers the most likely real
// case: as of 5.x the admin API has no /logs endpoint, so a 404 is the
// expected answer. The handler layer translates that to an operator
// hint; here we confirm the client surfaces it as a typed APIError and
// the IsLogsNotFound gate agrees.
func TestGetRuntimeLogs_NotFoundOnStockNodeRed(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/logs" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		http.Error(w, "Cannot GET /logs", http.StatusNotFound)
	})

	_, err := c.GetRuntimeLogs(context.Background(), 50)
	if err == nil {
		t.Fatal("expected 404, got nil")
	}
	if !IsLogsNotFound(err) {
		t.Errorf("IsLogsNotFound should be true, err=%v", err)
	}
}

// TestGetRuntimeLogs_EnvelopeShape covers the v2 admin API shape we
// would naturally expect: a {"logs":[...]} object with entries that
// already have ts/level/msg.
func TestGetRuntimeLogs_EnvelopeShape(t *testing.T) {
	const body = `{"logs":[
		{"ts":"2026-07-28T10:00:00Z","level":"info","msg":"started"},
		{"ts":"2026-07-28T10:00:01Z","level":"warn","msg":"deprecating foo"}
	]}`
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/logs" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("count"); got != "10" {
			t.Errorf("count query = %q, want 10", got)
		}
		_, _ = w.Write([]byte(body))
	})

	got, err := c.GetRuntimeLogs(context.Background(), 10)
	if err != nil {
		t.Fatalf("GetRuntimeLogs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(got), got)
	}
	if got[0].Message != "started" || got[0].Level != "info" {
		t.Errorf("entry 0 lost data: %+v", got[0])
	}
	if got[1].Level != "warn" {
		t.Errorf("entry 1 level = %q, want warn", got[1].Level)
	}
	if got[0].Timestamp.IsZero() {
		t.Errorf("entry 0 timestamp should be set, got %+v", got[0].Timestamp)
	}
}

// TestGetRuntimeLogs_BareArray covers the alternative shape: the
// endpoint returns a raw JSON array, no envelope.
func TestGetRuntimeLogs_BareArray(t *testing.T) {
	const body = `[
		{"timestamp":"2026-07-28T10:00:00Z","level":"ERROR","msg":"boot failed"},
		{"timestamp":"2026-07-28T10:00:01Z","level":"WARN","msg":"retry"}
	]`
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	got, err := c.GetRuntimeLogs(context.Background(), 10)
	if err != nil {
		t.Fatalf("GetRuntimeLogs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	// Levels should be normalised to the {"info","warn","error"} vocabulary.
	if got[0].Level != "error" {
		t.Errorf("entry 0 level = %q, want error (normalised)", got[0].Level)
	}
	if got[1].Level != "warn" {
		t.Errorf("entry 1 level = %q, want warn (normalised)", got[1].Level)
	}
}

// TestGetRuntimeLogs_PlainText covers the fallback: the endpoint
// returns a plain-text body, one line per entry.
func TestGetRuntimeLogs_PlainText(t *testing.T) {
	const body = "first line\nsecond line\n\nthird line\n"
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	got, err := c.GetRuntimeLogs(context.Background(), 10)
	if err != nil {
		t.Fatalf("GetRuntimeLogs: %v", err)
	}
	// The blank middle line is dropped.
	if len(got) != 3 {
		t.Fatalf("expected 3 non-blank lines, got %d: %+v", len(got), got)
	}
	if got[0].Message != "first line" {
		t.Errorf("entry 0 msg = %q, want first line", got[0].Message)
	}
	if !got[0].Timestamp.IsZero() {
		t.Errorf("plain-text body has no timestamps, got %+v", got[0].Timestamp)
	}
}

// TestGetRuntimeLogs_EmptyBody covers a 200 OK with an empty body —
// the operator asked for logs, the runtime had none. An empty slice
// is the right answer; the handler decides how to phrase that.
func TestGetRuntimeLogs_EmptyBody(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(""))
	})

	got, err := c.GetRuntimeLogs(context.Background(), 10)
	if err != nil {
		t.Fatalf("GetRuntimeLogs: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 entries, got %d", len(got))
	}
}

// TestGetRuntimeLogs_ClampsCount covers the safety bound: a count
// above the cap is clamped to 1000 so a runaway caller cannot ask for
// the whole history.
func TestGetRuntimeLogs_ClampsCount(t *testing.T) {
	var gotCount string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotCount = r.URL.Query().Get("count")
		_, _ = w.Write([]byte("[]"))
	})

	if _, err := c.GetRuntimeLogs(context.Background(), 5000); err != nil {
		t.Fatalf("GetRuntimeLogs: %v", err)
	}
	if gotCount != "1000" {
		t.Errorf("count = %q, want 1000 (clamped)", gotCount)
	}
}

// TestGetRuntimeLogs_DefaultCount covers the default applied when the
// caller passes zero (or a negative). The audit accepted 100 as the
// default.
func TestGetRuntimeLogs_DefaultCount(t *testing.T) {
	var gotCount string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotCount = r.URL.Query().Get("count")
		_, _ = w.Write([]byte("[]"))
	})

	if _, err := c.GetRuntimeLogs(context.Background(), 0); err != nil {
		t.Fatalf("GetRuntimeLogs: %v", err)
	}
	if gotCount != "100" {
		t.Errorf("count = %q, want 100 (default)", gotCount)
	}
}

// TestGetRuntimeLogs_RejectsServerError covers the 5xx path: any
// non-2xx surfaces as an *APIError. The handler does not need a
// special hint here (a 5xx is "Node-RED is broken", not "Node-RED
// does not support this"), so the existing APIError plumbing is
// enough.
func TestGetRuntimeLogs_RejectsServerError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	_, err := c.GetRuntimeLogs(context.Background(), 10)
	if err == nil {
		t.Fatal("expected 500, got nil")
	}
	if IsLogsNotFound(err) {
		t.Errorf("a 5xx must not be misclassified as a not-found")
	}
}

// TestParseRuntimeLogs_FallbackToText covers the case where the body
// starts with { or [ but does not parse as logs: the parser falls back
// to a single-line text view rather than returning an error. This is
// the kind of permissiveness the issue text asked for: the runtime
// owns the schema, we surface what we can.
func TestParseRuntimeLogs_FallbackToText(t *testing.T) {
	// A single number wrapped in an object: not a known shape, but
	// not a transport error either.
	body := []byte(`{"unrelated":"object"}`)
	got := parseRuntimeLogs(body)
	if len(got) == 0 {
		t.Errorf("expected the body to fall through to the text path, got 0 entries")
	}
}

// TestParseRuntimeLogs_PreservesOrder covers the audit's "newest last"
// preference: lines come out in the order they arrived, so the caller
// can take the last N.
func TestParseRuntimeLogs_PreservesOrder(t *testing.T) {
	body := []byte(`[{"msg":"a"},{"msg":"b"},{"msg":"c"}]`)
	got := parseRuntimeLogs(body)
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	for i, want := range []string{"a", "b", "c"} {
		if got[i].Message != want {
			t.Errorf("position %d msg = %q, want %q", i, got[i].Message, want)
		}
	}
}

// TestNormaliseLogLevel covers the level vocabulary mapping. The
// MCP-side filter only accepts {"info","warn","error"}, so anything
// outside that must be coerced before it reaches the caller.
func TestNormaliseLogLevel(t *testing.T) {
	tests := []struct{ in, want string }{
		{"info", "info"},
		{"INFO", "info"},
		{"Information", "info"},
		{"warn", "warn"},
		{"WARNING", "warn"},
		{"error", "error"},
		{"FATAL", "error"},
		{"critical", "error"},
		{"debug", "debug"}, // unrecognised — preserved
		{"trace", "trace"}, // unrecognised — preserved
		{"  ERROR  ", "error"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := normaliseLogLevel(tc.in); got != tc.want {
				t.Errorf("normaliseLogLevel(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestPickTime covers the three shapes we accept: an RFC 3339 string,
// a unix-millis number, and an absent key. Anything else falls
// through.
func TestPickTime(t *testing.T) {
	t.Run("rfc3339", func(t *testing.T) {
		m := map[string]any{"ts": "2026-07-28T10:00:00Z"}
		got := pickTime(m, "ts", "timestamp")
		if got.IsZero() {
			t.Errorf("expected non-zero time, got zero")
		}
		if got.Year() != 2026 {
			t.Errorf("year = %d, want 2026", got.Year())
		}
	})
	t.Run("epoch ms", func(t *testing.T) {
		m := map[string]any{"ts": float64(1753706400000)}
		got := pickTime(m, "ts")
		want := time.UnixMilli(1753706400000)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
	t.Run("absent", func(t *testing.T) {
		m := map[string]any{"other": "x"}
		if got := pickTime(m, "ts"); !got.IsZero() {
			t.Errorf("absent key must yield zero time, got %v", got)
		}
	})
}

// TestGetRuntimeLogs_AppliesBearerAuth covers the same auth guarantee
// every other endpoint in this package has: the bearer token is sent
// in the Authorization header. Confirms /logs is not silently skipped
// by do().
func TestGetRuntimeLogs_AppliesBearerAuth(t *testing.T) {
	var gotAuth string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("[]"))
	})
	if _, err := c.GetRuntimeLogs(context.Background(), 10); err != nil {
		t.Fatalf("GetRuntimeLogs: %v", err)
	}
	if !strings.Contains(gotAuth, "test-token") {
		t.Errorf("Authorization header missing the test token, got %q", gotAuth)
	}
}
