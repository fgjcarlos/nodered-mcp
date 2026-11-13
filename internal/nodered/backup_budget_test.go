package nodered

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestRestoreFlows_UsesTheFullDeployBudget is the client-side regression for
// issue #339: a restore is a full deployment (POST /flows), so it gets the
// full-deploy budget.
//
// The client used to apply no budget of its own to a restore, which left the
// generic 30s default in charge for a caller that passes no deadline — less
// than a full deploy is allowed. The MCP layer now grants the same budget
// for restore_backup; this test pins the client half of that agreement.
//
// The deadline is read from the request the client sends, because a client
// deadline does not travel over the wire.
func TestRestoreFlows_UsesTheFullDeployBudget(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[{"id":"cur","type":"tab","label":"Current"}]`))
		case r.Method == "POST" && r.URL.Path == "/flows":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c, _ := NewClient(Options{BaseURL: srv.URL, Token: "t", BackupDir: dir})
	recorder := &deadlineRecorder{base: c.httpClient.Transport}
	c.httpClient.Transport = recorder

	backup := RawFlow(`[{"id":"old","type":"tab","label":"Restored"}]`)
	if err := c.RestoreFlows(context.Background(), backup); err != nil {
		t.Fatalf("RestoreFlows: %v", err)
	}
	if recorder.deadline.IsZero() {
		t.Fatal("the restore request carried no deadline")
	}
	remaining := time.Until(recorder.deadline)
	if remaining < SettingsStateTimeout-time.Minute || remaining > SettingsStateTimeout {
		t.Errorf("restore request had %s left, want about %s", remaining, SettingsStateTimeout)
	}
}

// deadlineRecorder captures the deadline of the last request it saw.
type deadlineRecorder struct {
	base     http.RoundTripper
	deadline time.Time
}

func (d *deadlineRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	if deadline, ok := r.Context().Deadline(); ok {
		d.deadline = deadline
	}
	base := d.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(r)
}
