package oauth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingJWKSServer serves a JWKS document and counts the downloads it
// receives. The counter is the observable effect these regressions pin:
// every download is a request the identity provider has to answer, and the
// kid in an unverified token header is chosen by the requester.
type countingJWKSServer struct {
	*httptest.Server
	downloads atomic.Int64
	body      atomic.Value // string
	delay     time.Duration
}

func newCountingJWKSServer(t *testing.T, body string) *countingJWKSServer {
	t.Helper()
	s := &countingJWKSServer{}
	s.body.Store(body)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.downloads.Add(1)
		if s.delay > 0 {
			time.Sleep(s.delay)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(s.body.Load().(string)))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *countingJWKSServer) serve(body string) { s.body.Store(body) }

func (s *countingJWKSServer) downloadsCount() int64 { return s.downloads.Load() }

// A battery of invalid tokens must not turn into one JWKS download per
// request: the kid comes from an unverified token header, so the requester
// chooses it.
func TestKeyByIDBoundsDownloadsForUnknownKids(t *testing.T) {
	key := makeRSAKey(t)
	srv := newCountingJWKSServer(t, encodeJWK(t, "k1", key))
	ks := NewKeyset(srv.URL, 15*time.Minute)

	const tokens = 50
	for i := 0; i < tokens; i++ {
		if _, err := ks.KeyByID(fmt.Sprintf("unknown-%d", i)); err == nil {
			t.Fatalf("unknown kid %d must fail", i)
		}
	}

	// One download fills the cache, one more covers the rotation signal;
	// the rest of the battery must be served from the cache.
	if got := srv.downloadsCount(); got > 2 {
		t.Errorf("a battery of %d unknown kids caused %d JWKS downloads, want at most 2", tokens, got)
	}
}

// Simultaneous misses must share one download instead of each fetching.
func TestKeyByIDCoalescesConcurrentDownloadsForUnknownKids(t *testing.T) {
	key := makeRSAKey(t)
	srv := newCountingJWKSServer(t, encodeJWK(t, "k1", key))
	srv.delay = 20 * time.Millisecond
	ks := NewKeyset(srv.URL, 15*time.Minute)

	const tokens = 20
	var wg sync.WaitGroup
	for i := 0; i < tokens; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := ks.KeyByID(fmt.Sprintf("unknown-%d", i)); err == nil {
				t.Errorf("unknown kid %d must fail", i)
			}
		}(i)
	}
	wg.Wait()

	if got := srv.downloadsCount(); got > 2 {
		t.Errorf("%d concurrent unknown kids caused %d JWKS downloads, want at most 2", tokens, got)
	}
}

// Bounding the misses must not kill rotation: once the window elapses, the
// next miss refreshes again and finds the new key.
func TestKeyByIDRefreshesAgainAfterMissWindow(t *testing.T) {
	key1 := makeRSAKey(t)
	key2 := makeRSAKey(t)
	srv := newCountingJWKSServer(t, encodeJWK(t, "k1", key1))
	ks := NewKeyset(srv.URL, 15*time.Minute)

	now := time.Unix(1_700_000_000, 0)
	ks.now = func() time.Time { return now }

	if _, err := ks.KeyByID("k1"); err != nil {
		t.Fatalf("prime: %v", err)
	}

	// The IdP rotates and publishes a new kid.
	srv.serve(encodeJWK(t, "k2", key2))
	if _, err := ks.KeyByID("k2"); err != nil {
		t.Fatalf("rotated kid must be picked up: %v", err)
	}

	// A rotation inside the miss window is not fetched yet.
	srv.serve(encodeJWK(t, "k3", key2))
	if _, err := ks.KeyByID("k3"); err == nil {
		t.Fatal("kid inside the miss window must not be found")
	}

	// Once the window has elapsed, the next miss refreshes and finds it.
	now = now.Add(2 * time.Minute)
	got, err := ks.KeyByID("k3")
	if err != nil {
		t.Fatalf("kid after the miss window must be picked up: %v", err)
	}
	if got.ID != "k3" {
		t.Errorf("kid = %q, want k3", got.ID)
	}
}
