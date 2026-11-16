package mcp

// Issue #340 — the Node-RED version probe must work against a real
// local Node-RED, must be resolved before the operations that depend
// on it, and must recover from a failed probe.
//
// The old code skipped the probe for any loopback URL, which also
// skipped the shipped default (http://localhost:1880): a real local
// runtime was never probed, so the runtime version gate never
// engaged until some other operation happened to warm the cache.
//
// The fixtures below use httptest, so their URL is a loopback
// address — exactly the shape the old heuristic mistook for "test
// fixture". They are the regression: under the old behaviour these
// calls behaved as if no version had been detected.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// TestStartupProbe_DetectsLocalRuntime proves the startup probe is
// not skipped for a loopback URL: a local Node-RED's version is
// detected when the server boots.
func TestStartupProbe_DetectsLocalRuntime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/settings" {
			_, _ = w.Write([]byte(`{"version":"5.0.8"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	c, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	New(c, Options{Version: "test"})

	deadline := time.Now().Add(2 * time.Second)
	for {
		v := c.CachedNodeRedVersion()
		if v.Known {
			if v.String() != "5.0.8" {
				t.Fatalf("got %+v, want 5.0.8", v)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("startup probe did not detect the local runtime, got %+v", v)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestHandleSetContext_LocalRuntimeTooOld_RefusedWithoutSideEffects
// is the acceptance criterion "versión antigua se rechaza sin
// efectos secundarios" against a local runtime: the gate resolves
// the version itself and refuses the call before anything is
// provisioned or written.
//
// The fixture URL is a loopback address. Under the old heuristic the
// probe was skipped, the version stayed unknown and the call went
// through to the runtime.
func TestHandleSetContext_LocalRuntimeTooOld_RefusedWithoutSideEffects(t *testing.T) {
	var writes int32
	_, s := versionedFixture(t, "4.0.0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
			atomic.AddInt32(&writes, 1)
			t.Errorf("refused set_context should not hit %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	// Deliberately no primeVersionCache: the gate must resolve the
	// version on its own.

	res, err := s.handleSetContext(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"scope": "global",
			"key":   "x",
			"value": `"1"`,
		}},
	})
	if err != nil {
		t.Fatalf("handler returned err=%v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result on a too-old local runtime, got %+v", res)
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "4.0.0") || !strings.Contains(tc.Text, "5.0.0") {
		t.Errorf("refusal should name the detected and required versions, got %q", tc.Text)
	}
	if got := atomic.LoadInt32(&writes); got != 0 {
		t.Errorf("refused call reached the runtime %d times, want 0", got)
	}
	if s.ctxHelper != nil {
		t.Errorf("refused call provisioned the helper: %+v", s.ctxHelper)
	}
}

// TestHandleSetContext_LocalRuntimeSupported_NoUnknownNotice is the
// acceptance criterion "Node-RED local se detecta": a supported
// local runtime is detected, so the call succeeds without the
// "version was not detected" notice.
func TestHandleSetContext_LocalRuntimeSupported_NoUnknownNotice(t *testing.T) {
	_, s := versionedFixture(t, "5.0.8", setcontextSupportedHandler())

	res, err := s.handleSetContext(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"scope": "global",
			"key":   "k",
			"value": `"v"`,
		}},
	})
	if err != nil {
		t.Fatalf("handler returned err=%v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("expected success on a supported local runtime, got %+v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "Set context") {
		t.Errorf("expected the success text, got %q", tc.Text)
	}
	if strings.Contains(tc.Text, "version was not detected") {
		t.Errorf("the local runtime was not detected, got %q", tc.Text)
	}
}

// TestHandleInjectNode_LocalRuntimeTooOld_PayloadRefused mirrors the
// set_context case for the other version-dependent operation: the
// payload path is refused against a too-old local runtime, and the
// refusal reaches no endpoint beyond the read-only version probe.
func TestHandleInjectNode_LocalRuntimeTooOld_PayloadRefused(t *testing.T) {
	var writes int32
	_, s := versionedFixture(t, "4.0.0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
			atomic.AddInt32(&writes, 1)
			t.Errorf("refused inject_node(payload) should not hit %s %s", r.Method, r.URL.Path)
		}
		if r.Method == http.MethodGet && r.URL.Path == "/flows" {
			t.Errorf("refused inject_node(payload) should not probe /flows")
		}
		w.WriteHeader(http.StatusOK)
	}))

	res, err := s.handleInjectNode(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"id":      "n1",
			"payload": map[string]any{"foo": 1},
		}},
	})
	if err != nil {
		t.Fatalf("handler returned err=%v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result on a too-old local runtime, got %+v", res)
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "4.0.0") || !strings.Contains(tc.Text, "5.0.0") {
		t.Errorf("refusal should name the detected and required versions, got %q", tc.Text)
	}
	if got := atomic.LoadInt32(&writes); got != 0 {
		t.Errorf("refused call reached the runtime %d times, want 0", got)
	}
}
