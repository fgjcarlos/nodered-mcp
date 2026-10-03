package nodered

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// This file is the regression for issue #312: every URL that reaches a
// log line or a returned error must be routed through RedactURL so a
// query-string token or userinfo never lands in operator-visible
// output. The captureHandler is local to the nodered package because
// the mcp package's equivalent is unexported; duplicating ~30 lines
// is the smallest cost for a self-contained regression.

// captureHandler is a minimal slog.Handler that records every record
// it sees so tests can assert on the rendered attribute values.
//
// The slice is guarded by a mutex because slog handlers can be
// invoked from any goroutine the production code happens to call
// from.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
	minLvl  slog.Level
}

func (h *captureHandler) Enabled(_ context.Context, lvl slog.Level) bool {
	return lvl >= h.minLvl
}

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

// withCapturedSlog swaps slog.SetDefault to a recording handler for
// the duration of the test. slog.SetDefault is process-global, so
// these tests must not be marked t.Parallel.
func withCapturedSlog(t *testing.T, minLvl slog.Level) *captureHandler {
	t.Helper()
	prev := slog.Default()
	h := &captureHandler{minLvl: minLvl}
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

// sensitiveURL carries a token in both the query string and the
// userinfo. Any logging path that lets either survive is a leak.
const sensitiveURL = "http://user:hunter2@127.0.0.1:1/?token=SECRET&q=v"

// findAttr returns the value of attr key on the first record whose
// message contains substr, or false if no record matches.
func (h *captureHandler) findAttr(msgSubstr, key string) (slog.Value, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if !strings.Contains(r.Message, msgSubstr) {
			continue
		}
		var found slog.Value
		var ok bool
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == key {
				found = a.Value
				ok = true
				return false
			}
			return true
		})
		if ok {
			return found, true
		}
	}
	return slog.Value{}, false
}

// assertNoLeaks fails the test if the value contains the synthetic
// token, the password, or a raw query string. The goal is "the URL is
// the redacted form", not "the token is in some other form we didn't
// notice", so this is a string check against the constant.
func assertNoLeaks(t *testing.T, got slog.Value) {
	t.Helper()
	s := got.String()
	for _, leak := range []string{"SECRET", "hunter2", "token=", "user:hunter2"} {
		if strings.Contains(s, leak) {
			t.Errorf("leaked %q in logged URL: %q", leak, s)
		}
	}
}

func TestLogRedaction_NewClient_BaseURL(t *testing.T) {
	cap := withCapturedSlog(t, slog.LevelDebug)
	if _, err := NewClient(Options{BaseURL: sensitiveURL}); err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	v, ok := cap.findAttr("nodered client created", "base_url")
	if !ok {
		t.Fatal("expected nodered client created log line")
	}
	assertNoLeaks(t, v)
}

func TestLogRedaction_DoRequest_URL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	// baseURL with a token in the query: doURL builds u = baseURL+path,
	// which preserves the token. The log line must not.
	cap := withCapturedSlog(t, slog.LevelDebug)
	c, err := NewClient(Options{BaseURL: srv.URL + "/?token=SECRET"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.do(context.Background(), http.MethodGet, "/foo", nil, nil); err != nil {
		t.Fatalf("do: %v", err)
	}
	v, ok := cap.findAttr("nodered request", "url")
	if !ok {
		t.Fatal("expected nodered request log line")
	}
	assertNoLeaks(t, v)
}

func TestLogRedaction_GetRaw_URL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	cap := withCapturedSlog(t, slog.LevelDebug)
	c, err := NewClient(Options{BaseURL: srv.URL + "/?token=SECRET"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.getRaw(context.Background(), "/raw"); err != nil {
		t.Fatalf("getRaw: %v", err)
	}
	v, ok := cap.findAttr("nodered raw request", "url")
	if !ok {
		t.Fatal("expected nodered raw request log line")
	}
	assertNoLeaks(t, v)
}

func TestLogRedaction_GetRaw_ErrorWrapsRedactedURL(t *testing.T) {
	// Point at a closed port so httpClient.Do returns an error.
	// The wrapped error message must not include the query string.
	c, err := NewClient(Options{BaseURL: "http://127.0.0.1:1/?token=SECRET"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.getRaw(context.Background(), "/foo")
	if err == nil {
		t.Fatal("expected error from closed port")
	}
	msg := err.Error()
	if strings.Contains(msg, "SECRET") || strings.Contains(msg, "token=") {
		t.Errorf("error leaked token: %q", msg)
	}
}

func TestLogRedaction_DoURL_ConnectivityErrorWrapsRedactedURL(t *testing.T) {
	// Same shape as getRaw's wrap: the connectivity error must
	// not carry the query string either.
	c, err := NewClient(Options{BaseURL: "http://127.0.0.1:1/?token=SECRET"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	err = c.do(context.Background(), http.MethodGet, "/foo", nil, nil)
	if err == nil {
		t.Fatal("expected error from closed port")
	}
	if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "token=") {
		t.Errorf("error leaked token: %q", err.Error())
	}
}

func TestLogRedaction_DebugTail_ConnectURL(t *testing.T) {
	// commsURL preserves the query string of the base URL because
	// it only rewrites the scheme and the path. The "debug tail
	// connected" line (slog.Info) must not echo a token.
	//
	// The log call lives in (*DebugTail).logConnected (extracted
	// from session() so this regression can exercise it without
	// dialing a real WebSocket). If a future refactor drops the
	// RedactURL wrapper, the test fails because the rendered URL
	// will contain the synthetic token.
	cap := withCapturedSlog(t, slog.LevelInfo)

	c, err := NewClient(Options{BaseURL: "http://127.0.0.1:1/?token=SECRET"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	tail, err := NewDebugTail(c, 1)
	if err != nil {
		t.Fatalf("NewDebugTail: %v", err)
	}
	tail.setState(true, nil)
	tail.logConnected()

	v, ok := cap.findAttr("debug tail connected", "url")
	if !ok {
		t.Fatal("expected debug tail connected log line")
	}
	assertNoLeaks(t, v)
}

func TestLogRedaction_StatusTail_ConnectURL(t *testing.T) {
	cap := withCapturedSlog(t, slog.LevelInfo)

	c, err := NewClient(Options{BaseURL: "http://127.0.0.1:1/?token=SECRET"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	tail, err := NewStatusTail(c)
	if err != nil {
		t.Fatalf("NewStatusTail: %v", err)
	}
	tail.setState(true, nil)
	tail.logConnected()

	v, ok := cap.findAttr("status tail connected", "url")
	if !ok {
		t.Fatal("expected status tail connected log line")
	}
	assertNoLeaks(t, v)
}

func TestLogRedaction_WebSocketDialError(t *testing.T) {
	// Both tails return a wrap of the dial error that names the
	// wsURL. A failed dial against a closed port exercises that
	// branch. We assert on the message text directly rather than
	// threading the error through the test.
	//
	// The dial uses a 1-second budget so a hung dial doesn't
	// stall the test; the closed port should fail fast.
	cases := []struct {
		name string
		dial func(t *testing.T) error
	}{
		{
			name: "debug",
			dial: func(t *testing.T) error {
				t.Helper()
				c, err := NewClient(Options{BaseURL: "http://127.0.0.1:1/?token=SECRET"})
				if err != nil {
					t.Fatalf("NewClient: %v", err)
				}
				tail, err := NewDebugTail(c, 1)
				if err != nil {
					t.Fatalf("NewDebugTail: %v", err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				return tail.session(ctx)
			},
		},
		{
			name: "status",
			dial: func(t *testing.T) error {
				t.Helper()
				c, err := NewClient(Options{BaseURL: "http://127.0.0.1:1/?token=SECRET"})
				if err != nil {
					t.Fatalf("NewClient: %v", err)
				}
				tail, err := NewStatusTail(c)
				if err != nil {
					t.Fatalf("NewStatusTail: %v", err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				return tail.session(ctx)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.dial(t)
			if err == nil {
				t.Fatal("expected dial error against closed port")
			}
			msg := err.Error()
			if strings.Contains(msg, "SECRET") || strings.Contains(msg, "token=") {
				t.Errorf("%s dial error leaked token: %q", tc.name, msg)
			}
		})
	}
}

// Sanity: redactURL is exported as RedactURL and is callable from
// outside the package. The mcp/config packages need this.
func TestRedactURL_Exported(t *testing.T) {
	got := RedactURL("http://h/p?token=SECRET")
	if strings.Contains(got, "SECRET") {
		t.Errorf("RedactURL leaked token: %q", got)
	}
}
