package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestIssue339BudgetKeysAreRegisteredTools is the drift guard for issue
// #339: the budget table is keyed by tool name, so a renamed tool would
// silently fall back to the 15-second default — the exact defect this issue
// fixed, coming back through the back door.
//
// The test therefore checks the table against the live registry instead of
// against the same literals the table is written with.
func TestIssue339BudgetKeysAreRegisteredTools(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"5.0.0"}`))
	})
	registered := map[string]bool{}
	for _, tool := range s.tools {
		registered[tool.Name] = true
	}
	for name := range toolBudgets {
		if !registered[name] {
			t.Errorf("toolBudgets names %q, which is not a registered tool; it would never get its budget", name)
		}
	}
}

// TestIssue339ExpiredPaletteInstallIsNotRepeated pins the guarantee that an
// install whose own budget expires is never replayed: the operation may
// still be running in the runtime, so a second POST would be a second npm
// install.
//
// The palette budget is scaled to 100ms and the runtime holds the request
// open, so the attempt expires with the runtime still working.
func TestIssue339ExpiredPaletteInstallIsNotRepeated(t *testing.T) {
	scaleToolBudget(t, "install_node", 100*time.Millisecond)

	var posts atomic.Int32
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/nodes" {
			posts.Add(1)
			// Drain the body so the server watches the connection for the
			// client disconnect, then hold the request open past the
			// budget: the runtime keeps working after the client gives up.
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done()
			return
		}
		_, _ = w.Write([]byte(`{"version":"5.0.0"}`))
	})

	raw := registeredToolCall(t, s, "install_node", map[string]any{"module": "node-red-node-test"})
	if posts.Load() != 1 {
		t.Fatalf("POST /nodes count = %d, want 1; an expired install must not be replayed; response: %s", posts.Load(), raw)
	}
	if !strings.Contains(string(raw), "indeterminate") {
		t.Fatalf("expired install result = %s, want an indeterminate outcome", raw)
	}
}

// TestIssue339WriteDuringLongInstallIsRefused covers the concurrency
// question issue #339 raises: a long install holds the write locks for its
// whole duration, so what happens to a write that arrives during it.
//
// The answer must be a clean refusal: the waiting write reports that no
// action was started and sends nothing to the runtime, while the install
// itself still completes once. A write that was applied half-way, or an
// install that got replayed, would both be wrong.
func TestIssue339WriteDuringLongInstallIsRefused(t *testing.T) {
	restore := scaleDefaultToolTimeout(100 * time.Millisecond)
	defer restore()

	var installPosts atomic.Int32
	var flowPosts atomic.Int32
	installStarted := make(chan struct{})
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/nodes":
			installPosts.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
			close(installStarted)
			// Hold the write lock well past the default deadline.
			time.Sleep(600 * time.Millisecond)
			_, _ = w.Write([]byte(`{"name":"node-red-node-test","version":"1.0.0","nodes":[]}`))
		case r.Method == "POST" && r.URL.Path == "/flow":
			flowPosts.Add(1)
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write([]byte(`{"version":"5.0.0"}`))
		}
	})

	installDone := make(chan json.RawMessage, 1)
	go func() {
		installDone <- registeredToolCall(t, s, "install_node", map[string]any{"module": "node-red-node-test"})
	}()
	select {
	case <-installStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("the install never reached the runtime")
	}

	raw := registeredToolCall(t, s, "create_flow", map[string]any{
		"tab": map[string]any{"label": "T"},
	})
	if !strings.Contains(string(raw), "no action was started") {
		t.Fatalf("concurrent write result = %s, want a clean refusal", raw)
	}
	if flowPosts.Load() != 0 {
		t.Fatalf("POST /flow count = %d, want 0; a refused write must not reach the runtime", flowPosts.Load())
	}

	select {
	case install := <-installDone:
		if installPosts.Load() != 1 {
			t.Fatalf("POST /nodes count = %d, want 1; response: %s", installPosts.Load(), install)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the install never returned")
	}
}
