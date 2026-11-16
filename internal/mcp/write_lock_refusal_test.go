package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestWriteLockRefusalIsNotAnIndeterminateOutcome covers issue #347: a
// write that gives up waiting for the client write lock sent nothing to the
// runtime, so the tool reports the refusal. An indeterminate outcome would
// tell the operator to inspect the runtime before repeating the write, and
// that advice is wrong here: nothing happened.
func TestWriteLockRefusalIsNotAnIndeterminateOutcome(t *testing.T) {
	armed := make(chan struct{})
	release := make(chan struct{})
	var armedOnce, releaseOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/nodes" {
			armedOnce.Do(func() { close(armed) })
			<-release
			_, _ = w.Write([]byte(`{"name":"example"}`))
			return
		}
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer srv.Close()
	defer releaseOnce.Do(func() { close(release) })

	s := newTestServerAt(t, srv.URL, t.TempDir())

	// A direct client call holds the write lock while it parks in the
	// runtime, so the tool call below finds the lock held.
	held := make(chan error, 1)
	go func() {
		_, err := s.nrClient.InstallNode(context.Background(), "example", "")
		held <- err
	}()
	select {
	case <-armed:
	case <-time.After(time.Second):
		t.Fatal("the install call never reached the runtime")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	res, err := callHelperTool(ctx, s, "install_node", map[string]any{"module": "example"})
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("the refused write reported success: %+v", res)
	}
	text := resultFirstText(res)
	if !strings.Contains(text, "waiting for write lock: context deadline exceeded; no action was started") {
		t.Errorf("result = %q, want the write-lock refusal", text)
	}
	if strings.Contains(text, "indeterminate") {
		t.Errorf("result = %q, want no indeterminate outcome: nothing reached the runtime", text)
	}

	releaseOnce.Do(func() { close(release) })
	if err := <-held; err != nil {
		t.Fatalf("the write that held the lock failed: %v", err)
	}
}
