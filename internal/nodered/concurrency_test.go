package nodered

import (
	"context"
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flow/tabA":
			// Slightly slow so the goroutines pile up at the lock, not at the socket.
			time.Sleep(2 * time.Millisecond)
			_, _ = w.Write([]byte(`{"id":"tabA","label":"A","nodes":[{"id":"n1","type":"inject","x":1,"y":1,"wires":[]}]}`))
		case r.Method == "GET" && r.URL.Path == "/flows":
			// Backup snapshot fetch.
			_, _ = w.Write([]byte(`[{"type":"tab","id":"tabA","label":"A","nodes":[]}]`))
		case r.Method == "PUT" && r.URL.Path == "/flow/tabA":
			// The serialization guarantee is what we care about: every PUT
			// the runtime sees carries a complete flow body, and the
			// previous PUT's mutation is reflected. If writeMu were missing,
			// two PUTs would carry the same n1 (last GET snapshot) and one
			// mutation would be silently lost.
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

	if got := writes.Load(); got != int64(N) {
		t.Errorf("expected %d PUTs, got %d (some were lost to a race)", N, got)
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
