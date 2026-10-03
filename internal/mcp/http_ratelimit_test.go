package mcp

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

func TestRunHTTP_RateLimit_BurstThen429(t *testing.T) {
	c, err := nodered.NewClient(nodered.Options{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	srv := New(c, Options{
		Version:        "test",
		HTTPRatePerSec: 0.1,
		HTTPRateBurst:  5,
	})
	addr, stop := runHTTPSmoke(t, srv, "")
	defer stop()

	const totalRequests = 50
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"rate","version":"0.0.0"}}}`

	var firstOK, firstThrottled int
	for i := 0; i < totalRequests; i++ {
		code, headers, _ := postJSON(t, addr, body)
		switch {
		case code == http.StatusTooManyRequests:
			if firstThrottled == 0 {
				firstThrottled = i + 1
				if got := headers.Get("Retry-After"); got == "" {
					t.Errorf("request #%d returned 429 but no Retry-After header", i+1)
				}
			}
		case code == http.StatusOK:
			if firstOK == 0 {
				firstOK = i + 1
			}
		default:
			t.Fatalf("request #%d: expected 200 or 429, got %d", i+1, code)
		}
	}

	if firstOK == 0 {
		t.Fatalf("none of %d requests reached 200; rate limit is over-aggressive", totalRequests)
	}
	if firstThrottled == 0 {
		t.Fatalf("none of %d requests returned 429; rate limit is not firing", totalRequests)
	}
	if firstThrottled <= firstOK {
		t.Fatalf("429 fired on request #%d but the first 200 was #%d; the limiter should reject AFTER the burst, not before",
			firstThrottled, firstOK)
	}
}

// TestRunHTTP_RateLimit_PerSourceIP verifies that each source IP has its own
// independent token bucket. The test drives the middleware directly via
// httptest.NewRecorder so it does not require a second bindable loopback
// alias (e.g. 127.0.0.2) which is not available on macOS CI runners.
func TestRunHTTP_RateLimit_PerSourceIP(t *testing.T) {
	limiter := newPerIPLimiter(rate.Limit(0.1), 10)
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rateLimitByIP(limiter, inner)

	serveAs := func(remoteAddr string) int {
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader("{}"))
		req.RemoteAddr = remoteAddr
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}

	// IP A: exhaust the burst of 10 + a few more, should hit 429.
	aSawThrottle := false
	for i := 0; i < 15; i++ {
		code := serveAs("203.0.113.10:55555")
		if code == http.StatusTooManyRequests {
			aSawThrottle = true
		} else if code != http.StatusOK {
			t.Fatalf("IP A request #%d: expected 200 or 429, got %d", i+1, code)
		}
	}
	if !aSawThrottle {
		t.Fatalf("IP A never got throttled after 15 requests; per-IP limiter not firing")
	}

	// IP B: 5 requests — must all pass because IP B has its own full bucket.
	for i := 0; i < 5; i++ {
		code := serveAs("203.0.113.20:66666")
		if code != http.StatusOK {
			t.Fatalf("IP B request #%d: expected 200 (independent bucket), got %d", i+1, code)
		}
	}
}

func TestRunHTTP_RateLimit_Disabled(t *testing.T) {
	c, err := nodered.NewClient(nodered.Options{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	srv := New(c, Options{
		Version:          "test",
		HTTPRateDisabled: true,
	})
	addr, stop := runHTTPSmoke(t, srv, "")
	defer stop()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"off","version":"0.0.0"}}}`
	for i := 0; i < 120; i++ {
		code, _, _ := postJSON(t, addr, body)
		if code != http.StatusOK {
			t.Fatalf("disabled-rate-limit request #%d: expected 200, got %d", i+1, code)
		}
	}
}

func TestClientIP_StripsPort(t *testing.T) {
	cases := []struct {
		remote string
		want   string
	}{
		{"127.0.0.1:54321", "127.0.0.1"},
		{"10.0.0.1:443", "10.0.0.1"},
		{"[::1]:8080", "::1"},
		{"", "unknown"},
		{"no-port-here", "no-port-here"},
	}
	for _, tc := range cases {
		t.Run(tc.remote, func(t *testing.T) {
			req := mustRequest("POST", "/mcp", tc.remote)
			if got := clientIP(req); got != tc.want {
				t.Errorf("clientIP(%q) = %q, want %q", tc.remote, got, tc.want)
			}
		})
	}
}

func TestPerIPLimiter_BurstThenBlock(t *testing.T) {
	lim := newPerIPLimiter(rate.Limit(0.1), 3)

	if !lim.get("1.1.1.1").Allow() {
		t.Fatal("first request must pass (bucket starts full)")
	}
	if !lim.get("1.1.1.1").Allow() {
		t.Fatal("second request must pass")
	}
	if !lim.get("1.1.1.1").Allow() {
		t.Fatal("third request must pass (last of burst)")
	}
	if lim.get("1.1.1.1").Allow() {
		t.Fatal("fourth request must be rejected (burst exhausted)")
	}
	if !lim.get("2.2.2.2").Allow() {
		t.Fatal("second IP must have its own bucket; first request must pass")
	}
}

// TestPerIPLimiter_AmortizedSweep is the RED/GREEN regression for issue #313:
// per-IP churn must not cost O(n) on every new-key insert. With the old
// code, once len(limiters) > evictEvery, every new-IP call iterates the
// entire map. Inserting N=5000 fresh IPs therefore does roughly Σ_{i=1024}^{N} i
// map comparisons (~12M for N=5000), which takes hundreds of milliseconds.
// With the amortized sweep, total comparisons are bounded by N/1024 * N
// (~25k for the same workload), which finishes in milliseconds.
func TestPerIPLimiter_AmortizedSweep(t *testing.T) {
	const N = 5000
	lim := newPerIPLimiter(rate.Limit(float64(N)/float64(time.Second)*4), N)

	start := time.Now()
	for i := 0; i < N; i++ {
		// Use a unique key per call so every get() is a new-key insert
		// (the slow path that triggers the scan under the old implementation).
		lim.get(fmt.Sprintf("198.51.100.%d-%d", i/256, i))
	}
	elapsed := time.Since(start)

	// Old code: ~12M map comparisons under the global mutex -> hundreds of ms.
	// New code: amortized O(1) -> single-digit ms on the same workload.
	// A threshold of 150ms leaves comfortable headroom for CI while still
	// failing the old implementation (observed ~400-700ms on Linux CI).
	const maxAllowed = 150 * time.Millisecond
	if elapsed > maxAllowed {
		t.Fatalf("churn of %d new IPs took %v, want < %v; per-IP maintenance is not amortized", N, elapsed, maxAllowed)
	}
}

// TestPerIPLimiter_EvictsStaleEntries verifies the TTL eviction still fires
// for entries older than limiterTTL even though the scan is now throttled.
// We pre-seed an entry, rewind both its `last` field and the limiter's
// `lastSweep` directly (same package), then insert enough new IPs to trip
// the amortized sweep and assert the stale entry is gone.
func TestPerIPLimiter_EvictsStaleEntries(t *testing.T) {
	lim := newPerIPLimiter(rate.Limit(1000), 100)

	// Seed a victim and rewind its `last` so it appears older than TTL.
	lim.get("victim.example:1")
	lim.mu.Lock()
	lim.limiters["victim.example:1"].last = time.Now().Add(-2 * limiterTTL)
	// Force the amortized sweep gate open: pretend the last sweep ran a long
	// time ago, so the next insert with a full counter triggers eviction.
	lim.lastSweep = time.Now().Add(-2 * limiterTTL)
	lim.insertsSinceSweep = evictEvery - 1
	lim.mu.Unlock()

	// One more new-IP insert fills the counter and trips the sweep.
	lim.get("seed-1:1")

	lim.mu.Lock()
	if _, ok := lim.limiters["victim.example:1"]; ok {
		lim.mu.Unlock()
		t.Fatal("stale entry was not evicted; the amortized sweep skipped past it")
	}
	lim.mu.Unlock()
}

// TestPerIPLimiter_RaceSafe hammers get() concurrently from many goroutines on
// both shared and disjoint IPs. The race detector must not flag anything, and
// every IP must end up with exactly one entry.
func TestPerIPLimiter_RaceSafe(t *testing.T) {
	lim := newPerIPLimiter(rate.Limit(1000), 100)

	const goroutines = 16
	const callsPer = 500

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < callsPer; i++ {
				// Half the calls reuse a hot IP, half use a unique IP.
				if i%2 == 0 {
					lim.get(fmt.Sprintf("shared-%d:1", g%4))
				} else {
					lim.get(fmt.Sprintf("g%d-i%d:1", g, i))
				}
			}
		}(g)
	}
	wg.Wait()

	lim.mu.Lock()
	defer lim.mu.Unlock()
	// At minimum the unique-IP entries must be present (no entry was lost to a race).
	const wantMin = goroutines * callsPer / 2
	if len(lim.limiters) < wantMin {
		t.Fatalf("map has %d entries, expected at least %d", len(lim.limiters), wantMin)
	}
}

func postJSON(t *testing.T, addr, body string) (int, http.Header, []byte) {
	t.Helper()
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req := "POST /mcp HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Content-Type: application/json\r\n" +
		fmt.Sprintf("Content-Length: %d\r\n", len(body)) +
		"Accept: application/json, text/event-stream\r\n" +
		"Connection: close\r\n\r\n" + body
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}
	return readHTTPResponse(t, bufio.NewReader(conn))
}

func mustRequest(method, target, remoteAddr string) *http.Request {
	r, err := http.NewRequest(method, target, strings.NewReader(""))
	if err != nil {
		panic(err)
	}
	r.RemoteAddr = remoteAddr
	return r
}
