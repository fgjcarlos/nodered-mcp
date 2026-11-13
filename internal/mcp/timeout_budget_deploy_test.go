package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// TestIssue339QueuedDeployIsRefusedDuringLongInstall covers what a long
// palette operation does to the writes that queue behind it.
//
// Every write takes the context-helper lock before it reaches the runtime,
// and a long install holds that lock for its whole duration. A queued
// deployment must therefore be refused cleanly: no request reaches the
// runtime, no backup is written, and the refusal says that no action was
// started. The install itself still completes once.
//
// This is the behaviour the "no action was started" message promises, and
// it is the reason a queued write is not an indeterminate outcome: it never
// began.
func TestIssue339QueuedDeployIsRefusedDuringLongInstall(t *testing.T) {
	// The two deployment writes get their own budget, so the default is
	// scaled down for the install only; the deployments are scaled to a
	// budget shorter than the install holds the lock, which is what makes
	// their wait for the lock expire.
	scaleToolBudget(t, "set_flows", 100*time.Millisecond)
	scaleToolBudget(t, "restore_backup", 100*time.Millisecond)

	var installPosts atomic.Int32
	var flowGets atomic.Int32
	var flowPosts atomic.Int32
	installStarted := make(chan struct{})
	s, backupDir := serverWithMockAndBackups(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/nodes":
			installPosts.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
			close(installStarted)
			// Hold the write lock well past the default deadline.
			time.Sleep(600 * time.Millisecond)
			_, _ = w.Write([]byte(`{"name":"node-red-node-test","version":"1.0.0","nodes":[]}`))
		case r.Method == "GET" && r.URL.Path == "/flows":
			flowGets.Add(1)
			_, _ = w.Write([]byte(`[{"id":"tabA","type":"tab"}]`))
		case r.Method == "POST" && r.URL.Path == "/flows":
			flowPosts.Add(1)
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write([]byte(`{"version":"5.0.0"}`))
		}
	})
	writeTestBackup(t, backupDir, "flows-test-000001.json", `[{"id":"tabA","type":"tab"}]`)

	installDone := make(chan json.RawMessage, 1)
	go func() {
		installDone <- registeredToolCall(t, s, "install_node", map[string]any{"module": "node-red-node-test"})
	}()
	select {
	case <-installStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("the install never reached the runtime")
	}

	// set_flows and restore_backup are the two full-deployment writes. Both
	// queue behind the install's lock and must be refused.
	deploy := registeredToolCall(t, s, "set_flows", map[string]any{
		"flows": []any{map[string]any{"id": "tabB", "type": "tab"}},
	})
	if !strings.Contains(string(deploy), "no action was started") {
		t.Fatalf("queued deploy result = %s, want a clean refusal", deploy)
	}
	restoreCall := registeredToolCall(t, s, "restore_backup", map[string]any{"backup": "flows-test-000001.json"})
	if !strings.Contains(string(restoreCall), "no action was started") {
		t.Fatalf("queued restore result = %s, want a clean refusal", restoreCall)
	}
	if flowGets.Load() != 0 || flowPosts.Load() != 0 {
		t.Fatalf("runtime saw %d GET /flows and %d POST /flows; a refused write must not reach the runtime", flowGets.Load(), flowPosts.Load())
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

// TestIssue339SlowRestoreCompletesOnce is the restore-side counterpart of
// TestIssue339SlowPaletteInstallCompletesOnce: a restore that outlives the
// default deadline must still complete, exactly once.
//
// Before the fix, restore_backup was cut at the default deadline and
// reported an indeterminate outcome — the same defect the palette tools had,
// in the category the verifier found inconsistent.
func TestIssue339SlowRestoreCompletesOnce(t *testing.T) {
	restore := scaleDefaultToolTimeout(50 * time.Millisecond)
	defer restore()

	var flowPosts atomic.Int32
	s, backupDir := serverWithMockAndBackups(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[{"id":"tabA","type":"tab"}]`))
		case r.Method == "POST" && r.URL.Path == "/flows":
			flowPosts.Add(1)
			// Drain the body so the server watches the connection, then
			// take longer than the default deadline.
			_, _ = io.Copy(io.Discard, r.Body)
			time.Sleep(300 * time.Millisecond)
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write([]byte(`{"version":"5.0.0"}`))
		}
	})
	writeTestBackup(t, backupDir, "flows-test-000001.json", `[{"id":"tabA","type":"tab"}]`)

	raw := registeredToolCall(t, s, "restore_backup", map[string]any{"backup": "flows-test-000001.json"})
	if flowPosts.Load() != 1 {
		t.Fatalf("POST /flows count = %d, want 1; response: %s", flowPosts.Load(), raw)
	}
	if strings.Contains(string(raw), "indeterminate") {
		t.Fatalf("the restore did not complete: %s", raw)
	}
}

// serverWithMockAndBackups is serverWithMock plus the backup directory it
// writes to, so a test can place a backup file a restore can read.
func serverWithMockAndBackups(t *testing.T, h http.HandlerFunc) (*Server, string) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	c, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: dir})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return New(c, Options{Version: "test"}), dir
}

// writeTestBackup places a backup file where the server's backup reader
// looks for it.
func writeTestBackup(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("writing backup: %v", err)
	}
}
