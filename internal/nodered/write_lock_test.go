package nodered

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestWriteLockWaitAbandonsWhenContextIsDone covers issue #347: a write
// that arrives while another write holds the guard must give up once its
// own context is done, instead of waiting past the deadline it was
// promised. The refusal says that no action was started, and the second
// write never reaches the runtime.
func TestWriteLockWaitAbandonsWhenContextIsDone(t *testing.T) {
	cases := []struct {
		name    string
		context func() (context.Context, context.CancelFunc)
		wantErr string
	}{
		{
			name: "canceled",
			context: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			wantErr: "waiting for write lock: context canceled; no action was started",
		},
		{
			name: "deadline exceeded",
			context: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 20*time.Millisecond)
			},
			wantErr: "waiting for write lock: context deadline exceeded; no action was started",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var posts, gets atomic.Int64
			entered := make(chan struct{})
			release := make(chan struct{})

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && r.URL.Path == "/flows":
					gets.Add(1)
					_, _ = w.Write(testRevisionedTab(t, `{"id":"tabA","type":"tab","label":"A"}`))
				case r.Method == "POST" && r.URL.Path == "/flow":
					posts.Add(1)
					close(entered)
					<-release
					_, _ = w.Write([]byte(`{"id":"tabNew"}`))
				default:
					http.Error(w, "unexpected", http.StatusInternalServerError)
				}
			}))
			t.Cleanup(srv.Close)

			c, err := NewClient(Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}

			// The first write takes the guard and parks inside the runtime
			// call, so the guard is held for the whole test.
			firstErr := make(chan error, 1)
			go func() {
				_, err := c.CreateFlow(context.Background(), RawFlow(`{"id":"tabA","type":"tab","label":"A"}`))
				firstErr <- err
			}()

			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("the first write never reached the runtime")
			}

			// The second write finds the guard held and its own context
			// done: it has to give up instead of waiting.
			ctx, cancel := tc.context()
			defer cancel()
			secondErr := make(chan error, 1)
			go func() {
				_, err := c.CreateFlow(ctx, RawFlow(`{"id":"tabB","type":"tab","label":"B"}`))
				secondErr <- err
			}()

			select {
			case err := <-secondErr:
				if err == nil {
					t.Fatal("the second write succeeded while another write held the guard")
				}
				if err.Error() != tc.wantErr {
					t.Fatalf("refusal = %q, want %q", err.Error(), tc.wantErr)
				}
			case <-time.After(2 * time.Second):
				close(release)
				<-firstErr
				t.Fatal("the second write waited for the guard past its own deadline")
			}

			if got := posts.Load(); got != 1 {
				t.Errorf("POSTs to the runtime = %d, want 1 (the refused write must not reach it)", got)
			}
			if got := gets.Load(); got != 1 {
				t.Errorf("GETs of the flows = %d, want 1 (the refused write must not snapshot anything)", got)
			}

			// The guard survives the refusal: the write that held it still
			// completes.
			close(release)
			select {
			case err := <-firstErr:
				if err != nil {
					t.Fatalf("the first write failed: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the first write never completed")
			}
		})
	}
}

// TestWriteGuardRefusesBeforeAnyRequest pins the same contract for the
// guard itself: an already-done context is refused before the runtime is
// contacted, so a canceled write cannot start a mutation.
func TestWriteGuardRefusesBeforeAnyRequest(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = c.CreateFlow(ctx, RawFlow(`{"id":"tabA","type":"tab","label":"A"}`))
	if err == nil {
		t.Fatal("CreateFlow with a canceled context succeeded")
	}
	if !strings.Contains(err.Error(), "no action was started") {
		t.Errorf("refusal = %q, want it to say that no action was started", err.Error())
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("requests to the runtime = %d, want 0", got)
	}
}
