package mcp

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// captureHandler is a minimal slog.Handler that records every record
// passed through it. Tests assert on the captured slice rather than
// parsing rendered text — the handler emits a stable shape regardless
// of whether the production logger uses JSON, text, or anything else.
//
// The slice is guarded by a mutex because slog handlers can be called
// from multiple goroutines (runHTTP logs from the listener goroutine
// and the shutdown goroutine).
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

func (h *captureHandler) findMsg(substr string) (slog.Record, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if strings.Contains(r.Message, substr) {
			return r, true
		}
	}
	return slog.Record{}, false
}

func (h *captureHandler) attr(r slog.Record, key string) (slog.Value, bool) {
	var found bool
	var val slog.Value
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			found = true
			val = a.Value
			return false
		}
		return true
	})
	return val, found
}

// withCapturedSlog swaps slog.SetDefault to a recording handler for the
// duration of the test. Returns the handler plus a cleanup that restores
// the original default logger. slog.SetDefault is process-global, so
// callers must not assume parallel safety with other slog-emitting tests.
func withCapturedSlog(t *testing.T, minLvl slog.Level) *captureHandler {
	t.Helper()
	prev := slog.Default()
	h := &captureHandler{minLvl: minLvl}
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

// freeLoopbackAddr returns a host:port bound to a real loopback
// interface and immediately released. The caller is expected to hand it
// to runHTTP; the kernel will reuse the port inside the race window.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("probe close: %v", err)
	}
	return addr
}

// Issue #89: the loopback-without-token configuration must emit a
// startup warning that names the reverse-proxy trap. The warning is
// the operator's only signal that their deploy is "safe by bind, but
// not safe by topology" — a silent fail here is the bug we are
// closing.
func TestRunHTTP_LoopbackNoAuth_EmitsReverseProxyWarning(t *testing.T) {
	addr := freeLoopbackAddr(t)
	cap := withCapturedSlog(t, slog.LevelInfo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := New(&nodered.Client{}, Options{})
	errCh := make(chan error, 1)
	go func() { errCh <- srv.runHTTP(ctx, addr, "", nil) }()

	// Give the listener a moment to log the startup lines. We do
	// not race a fixed deadline against the listener; we wait on
	// the warning specifically.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := cap.findMsg("http transport has no authentication"); ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-errCh

	rec, ok := cap.findMsg("http transport has no authentication")
	if !ok {
		t.Fatalf("expected loopback-no-auth warning, captured records: %+v", cap.records)
	}

	// The reverse-proxy trap must be in the structured fields, not
	// just the message: operators tailing logs in JSON formatters
	// (or alerting on specific attribute values) rely on it being
	// machine-readable.
	if v, ok := cap.attr(rec, "addr"); !ok || v.String() != addr {
		t.Errorf("expected addr=%q in warning, got %v (present=%v)", addr, v, ok)
	}
	if v, ok := cap.attr(rec, "risk"); !ok || !strings.Contains(v.String(), "reverse proxy") {
		t.Errorf("expected risk to mention reverse proxy, got %q (present=%v)", v.String(), ok)
	}
	if v, ok := cap.attr(rec, "mitigation"); !ok || !strings.Contains(v.String(), "MCP_HTTP_TOKEN") {
		t.Errorf("expected mitigation to name MCP_HTTP_TOKEN, got %q (present=%v)", v.String(), ok)
	}
}

// When the operator has explicitly acknowledged the loopback-without-
// token trade-off (AllowInsecureLoopback=true), the warning is still
// emitted — but it carries an "acknowledged" marker and the message is
// framed as operator-acknowledged rather than the default nag.
func TestRunHTTP_LoopbackNoAuth_AcknowledgedFlagChangesWarningShape(t *testing.T) {
	addr := freeLoopbackAddr(t)
	cap := withCapturedSlog(t, slog.LevelInfo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := New(&nodered.Client{}, Options{AllowInsecureLoopback: true})
	errCh := make(chan error, 1)
	go func() { errCh <- srv.runHTTP(ctx, addr, "", nil) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := cap.findMsg("operator-acknowledged"); ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-errCh

	rec, ok := cap.findMsg("operator-acknowledged")
	if !ok {
		t.Fatalf("expected acknowledged-framed warning, captured records: %+v", cap.records)
	}
	if v, ok := cap.attr(rec, "acknowledged_via"); !ok || !strings.Contains(v.String(), "AllowInsecureLoopback") {
		t.Errorf("expected acknowledged_via attribute naming AllowInsecureLoopback, got %q (present=%v)", v.String(), ok)
	}

	// The un-acknowledged nag must NOT also fire — that would be
	// two warnings for the same condition, which defeats the
	// purpose of the opt-out.
	if _, ok := cap.findMsg("loopback bind assumed safe"); ok {
		t.Error("un-acknowledged warning fired alongside the acknowledged one; opt-out should suppress the nag")
	}
}

// When a bearer token is configured the loopback warning must NOT
// fire: auth is in place, the operator has nothing to worry about.
func TestRunHTTP_LoopbackWithToken_NoWarning(t *testing.T) {
	addr := freeLoopbackAddr(t)
	cap := withCapturedSlog(t, slog.LevelInfo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := New(&nodered.Client{}, Options{})
	errCh := make(chan error, 1)
	go func() { errCh <- srv.runHTTP(ctx, addr, "s3cret", nil) }()

	// Wait long enough for the listener to be ready and the
	// startup logs to be flushed.
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-errCh

	if _, ok := cap.findMsg("http transport has no authentication"); ok {
		t.Error("loopback-no-auth warning fired even though a bearer token was configured")
	}
}
