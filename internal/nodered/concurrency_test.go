package nodered

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentMutationsAreSerialized covers issue #28: multiple
// tools/call requests fired in parallel used to race on the read-modify-
// write path. The serialized version still allows each call to complete
// successfully (no error from lost-write or 409 conflict), and the order
// is well-defined rather than dependent on goroutine scheduling.
//
// Without writeMu this test was timing-sensitive: a slow handler caused
// one handler to overwrite another's write while both reported success.
func TestConcurrentMutationsAreSerialized(t *testing.T) {
	var writes atomic.Int64
	var runtimeMu sync.Mutex
	tab := []byte(`{"id":"tabA","label":"A","nodes":[{"id":"n1","type":"inject","z":"tabA","x":1,"y":1,"wires":[]}]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runtimeMu.Lock()
		defer runtimeMu.Unlock()
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/tabA":
			// Slightly slow so the goroutines pile up at the lock, not at the socket.
			time.Sleep(2 * time.Millisecond)
			_, _ = w.Write(tab)
		case r.Method == "GET" && r.URL.Path == "/flows":
			// Backup snapshot fetch.
			_, _ = w.Write(testRevisionedTab(t, string(tab)))
		case r.Method == "POST" && r.URL.Path == "/flows":
			// Retain each edit so a missing lock also loses nodes visibly.
			body, _ := io.ReadAll(r.Body)
			tab = testNestedDeployment(t, body, "tabA")
			writes.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	const N = 10
	var wg sync.WaitGroup
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Unique id per goroutine so the duplicate-id guard cannot fire;
			// we are testing serialization, not the guard.
			node := []byte(`{"id":"concurrent_` + intToStr(i) +
				`","type":"inject","z":"tabA","x":1,"y":1,"wires":[]}`)
			if err := c.AddNode(context.Background(), "tabA", []byte(node)); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent AddNode failed: %v", err)
	}

	if got := nodeCount(t, RawFlow(tab)); got != N+1 {
		t.Errorf("expected %d retained nodes, got %d", N+1, got)
	}
	if got := writes.Load(); got != int64(N) {
		t.Errorf("expected %d POSTs, got %d (some were lost to a race)", N, got)
	}
}

// intToStr avoids importing strconv just for one test helper.
func intToStr(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
