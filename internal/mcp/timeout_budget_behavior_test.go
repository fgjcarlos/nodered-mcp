package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// TestIssue339SlowPaletteInstallCompletesOnce is the acceptance regression
// for issue #339: an install that outlives the default tool deadline, but
// stays inside the palette budget, must complete — exactly once.
//
// With the old flat 15-second wrapper the install was cut short at the
// deadline and the tool reported an indeterminate outcome, even though the
// runtime kept processing the npm install. A second POST would have been a
// second npm install, so the count matters as much as the completion.
//
// The default deadline is scaled down for this test so the simulated install
// stays inside CI time; the real budgets (15s default, 5min palette) are
// pinned by TestIssue339ToolBudgets.
func TestIssue339SlowPaletteInstallCompletesOnce(t *testing.T) {
	restore := scaleDefaultToolTimeout(50 * time.Millisecond)
	defer restore()

	const installWork = 300 * time.Millisecond
	var posts atomic.Int32
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/nodes" {
			posts.Add(1)
			time.Sleep(installWork)
			_, _ = w.Write([]byte(`{"name":"node-red-node-test","version":"1.0.0","nodes":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"version":"5.0.0"}`))
	})

	raw := registeredToolCall(t, s, "install_node", map[string]any{"module": "node-red-node-test"})
	if posts.Load() != 1 {
		t.Fatalf("POST /nodes count = %d, want 1; response: %s", posts.Load(), raw)
	}
	var envelope struct {
		Result mcp.CallToolResult `json:"result"`
		Error  json.RawMessage    `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Error) != 0 || envelope.Result.IsError {
		t.Fatalf("install did not complete: %s", raw)
	}
	if text := resultFirstText(&envelope.Result); !strings.Contains(text, "node-red-node-test") {
		t.Fatalf("install result = %q, want the installed module", text)
	}
}

// TestIssue339ClientCancellationStopsPaletteInstall proves the client can
// cancel an in-flight palette install and that the cancellation reaches the
// runtime call instead of leaving it hanging.
//
// The MCP server cancels the request context when the client sends
// notifications/cancelled, so the abort has to travel from there into the
// HTTP request against Node-RED. The outcome of a cancelled install is
// reported as indeterminate on purpose: the runtime may still be running the
// npm install after the client walks away, so the tool must not invite the
// caller to simply repeat it.
func TestIssue339ClientCancellationStopsPaletteInstall(t *testing.T) {
	var posts atomic.Int32
	runtimeCancelled := make(chan struct{})
	postReceived := make(chan struct{})
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/nodes" {
			posts.Add(1)
			// The body must be consumed: the Go HTTP server only watches the
			// connection for a client disconnect once the request body has
			// been read to the end, so a handler that ignores the body never
			// observes the abort. A real runtime reads it.
			_, _ = io.Copy(io.Discard, r.Body)
			close(postReceived)
			select {
			case <-r.Context().Done():
				close(runtimeCancelled)
			case <-time.After(10 * time.Second):
			}
			return
		}
		_, _ = w.Write([]byte(`{"version":"5.0.0"}`))
	})

	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 7, "method": "tools/call",
		"params": map[string]any{"name": "install_node", "arguments": map[string]any{"module": "node-red-node-test"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	responses := make(chan json.RawMessage, 1)
	go func() {
		response := s.mcpServer.HandleMessage(context.Background(), request)
		encoded, err := json.Marshal(response)
		if err != nil {
			t.Error(err)
			return
		}
		responses <- encoded
	}()

	select {
	case <-postReceived:
	case <-time.After(10 * time.Second):
		t.Fatal("the install never reached the runtime")
	}

	cancel, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "method": "notifications/cancelled",
		"params": map[string]any{"requestId": 7},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.mcpServer.HandleMessage(context.Background(), cancel)

	select {
	case raw := <-responses:
		if posts.Load() != 1 {
			t.Fatalf("POST /nodes count = %d, want 1; response: %s", posts.Load(), raw)
		}
		if !strings.Contains(string(raw), "indeterminate") {
			t.Fatalf("cancelled install result = %s, want an indeterminate outcome", raw)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the cancelled install never returned")
	}
	select {
	case <-runtimeCancelled:
	case <-time.After(5 * time.Second):
		t.Error("the runtime request was not cancelled; the abort did not reach Node-RED")
	}
}

// scaleDefaultToolTimeout shrinks the default tool deadline for one test and
// returns the function that puts it back. It exists so a test can simulate
// work that outlives the default deadline without sleeping for the real
// 15 seconds.
//
// The package tests are not parallel, so swapping the package variable is
// safe here.
func scaleDefaultToolTimeout(d time.Duration) func() {
	previous := toolTimeout
	toolTimeout = d
	return func() { toolTimeout = previous }
}

// scaleToolBudget gives one tool a different budget for the duration of a
// test. It is how a test drives a single operation past its own deadline.
//
// The package tests are not parallel, so swapping the package map is safe
// here.
func scaleToolBudget(t *testing.T, tool string, d time.Duration) {
	t.Helper()
	previous, had := toolBudgets[tool]
	toolBudgets[tool] = d
	t.Cleanup(func() {
		if had {
			toolBudgets[tool] = previous
			return
		}
		delete(toolBudgets, tool)
	})
}
