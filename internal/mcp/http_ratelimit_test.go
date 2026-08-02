package mcp

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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
