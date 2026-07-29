package mcp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// runHTTPSmoke wires the production runHTTP against a random loopback
// port and blocks until the listener accepts (or the test environment
// is broken). Returns the bound addr and a Shutdown closure. The addr
// is a host:port string suitable for net.Dial.
//
// The probe pattern (dial in a tight loop until success) is the same
// shape that httptest.NewServer uses internally. It is robust against
// a slow start because we don't race a fixed deadline: we wait up to
// 5s, fail the test if the listener never opens, and otherwise hand
// control back to the caller.
func runHTTPSmoke(t *testing.T, s *Server, token string) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	addr := "127.0.0.1:0"
	// We need to know the picked port. Net.Listen on :0 returns a
	// listener with the chosen port; we close it and let runHTTP
	// re-bind on the same host:port. The kernel will reuse the
	// port within the 5s window with overwhelming probability
	// in a CI environment.
	probe, err := net.Listen("tcp", addr)
	if err != nil {
		cancel()
		t.Fatalf("probe listen: %v", err)
	}
	bound := probe.Addr().String()
	probe.Close()

	// 5s race window for the re-bind + accept. CI runners can be
	// slow on the first connection, but 5s is well above any sane
	// startup time for a TCP listener in Go.
	deadline := time.Now().Add(5 * time.Second)
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.runHTTP(ctx, bound, token, nil)
	}()
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", bound, 100*time.Millisecond)
		if err == nil {
			c.Close()
			return bound, func() {
				cancel()
				// Drain the err channel so the goroutine can
				// exit cleanly under -race. We do not assert
				// the value here: the test's own assertions
				// already drive any failure reporting.
				<-errCh
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-errCh
	t.Fatalf("server did not accept on %s within 5s", bound)
	return "", nil // unreachable
}

// readHTTPResponse parses a single HTTP/1.1 response off a buffered
// connection. The streamable HTTP transport replies with chunked or
// fixed-length JSON-RPC envelopes; we only care about status, headers,
// and (when Content-Length is present) the body.
func readHTTPResponse(t *testing.T, r *bufio.Reader) (int, http.Header, []byte) {
	t.Helper()
	statusLine, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	parts := strings.SplitN(strings.TrimSpace(statusLine), " ", 3)
	if len(parts) < 2 {
		t.Fatalf("malformed status line: %q", statusLine)
	}
	var code int
	fmt.Sscanf(parts[1], "%d", &code)
	headers := http.Header{}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read header: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		headers.Add(strings.TrimSpace(line[:colon]), strings.TrimSpace(line[colon+1:]))
	}
	cl := 0
	if v := headers.Get("Content-Length"); v != "" {
		fmt.Sscanf(v, "%d", &cl)
	}
	var body []byte
	if cl > 0 {
		body = make([]byte, cl)
		if _, err := io.ReadFull(r, body); err != nil {
			t.Fatalf("read body: %v", err)
		}
	}
	return code, headers, body
}

// TestRunHTTP_SlowlorisIsDisconnected proves the headline fix: a
// client that opens a connection and stalls before sending complete
// headers gets dropped by the configured ReadHeaderTimeout, not by
// the operator or by a goroutine leak. The ceiling is 10s; we
// allow up to 13s for scheduling jitter and shutdown overhead.
func TestRunHTTP_SlowlorisIsDisconnected(t *testing.T) {
	c, err := nodered.NewClient(nodered.Options{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	srv := New(c, "test", false, false)
	addr, stop := runHTTPSmoke(t, srv, "")
	defer stop()

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send the request line + Host header, then stall. We never
	// finish the headers block; ReadHeaderTimeout should fire.
	if _, err := conn.Write([]byte("POST /mcp HTTP/1.1\r\nHost: " + addr + "\r\n")); err != nil {
		t.Fatalf("partial write: %v", err)
	}

	// Poll for either a closed connection (read returns non-timeout
	// error) within 13s.
	deadline := time.Now().Add(13 * time.Second)
	closed := false
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		var b [1]byte
		if _, err := conn.Read(b[:]); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			closed = true
			break
		}
	}
	if !closed {
		t.Fatalf("ReadHeaderTimeout did not disconnect the stalling client within 13s")
	}
}

// TestRunHTTP_InitializeLoopbackNoAuth is the happy path smoke: a
// real JSON-RPC `initialize` over loopback, no bearer, must come
// back with a 200 and a server-info payload.
func TestRunHTTP_InitializeLoopbackNoAuth(t *testing.T) {
	c, err := nodered.NewClient(nodered.Options{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	srv := New(c, "test", false, false)
	addr, stop := runHTTPSmoke(t, srv, "")
	defer stop()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"smoke","version":"0.0.0"}}}`
	req := "POST /mcp HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Content-Type: application/json\r\n" +
		fmt.Sprintf("Content-Length: %d\r\n", len(body)) +
		"Accept: application/json, text/event-stream\r\n" +
		"Connection: close\r\n\r\n" + body
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}
	br := bufio.NewReader(conn)
	code, headers, payload := readHTTPResponse(t, br)
	if code != http.StatusOK {
		t.Fatalf("initialize: expected 200, got %d (body=%q)", code, payload)
	}
	ct := headers.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") && !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("initialize: unexpected Content-Type %q (body=%q)", ct, payload)
	}
	if !strings.Contains(string(payload), "jsonrpc") || !strings.Contains(string(payload), "nodered-mcp") {
		t.Fatalf("initialize: response did not mention server identity (body=%q)", payload)
	}
}

// TestRunHTTP_InitializeBearerRejectedWithoutToken covers the auth
// half of the issue's acceptance: a bearer-less request against a
// bearer-only server must come back 401.
func TestRunHTTP_InitializeBearerRejectedWithoutToken(t *testing.T) {
	c, err := nodered.NewClient(nodered.Options{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	srv := New(c, "test", false, false)
	addr, stop := runHTTPSmoke(t, srv, "supersecrettoken")
	defer stop()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"smoke","version":"0.0.0"}}}`
	req := "POST /mcp HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Content-Type: application/json\r\n" +
		fmt.Sprintf("Content-Length: %d\r\n", len(body)) +
		"Connection: close\r\n\r\n" + body
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}
	br := bufio.NewReader(conn)
	code, _, _ := readHTTPResponse(t, br)
	if code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: expected 401, got %d", code)
	}
}

// TestRunHTTP_InitializeBearerAccepted covers the auth-OK half: a
// bearer request with the configured token must pass the gate.
func TestRunHTTP_InitializeBearerAccepted(t *testing.T) {
	c, err := nodered.NewClient(nodered.Options{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	srv := New(c, "test", false, false)
	addr, stop := runHTTPSmoke(t, srv, "supersecrettoken")
	defer stop()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"smoke","version":"0.0.0"}}}`
	req := "POST /mcp HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Authorization: Bearer supersecrettoken\r\n" +
		"Content-Type: application/json\r\n" +
		fmt.Sprintf("Content-Length: %d\r\n", len(body)) +
		"Accept: application/json, text/event-stream\r\n" +
		"Connection: close\r\n\r\n" + body
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}
	br := bufio.NewReader(conn)
	code, _, payload := readHTTPResponse(t, br)
	if code != http.StatusOK {
		t.Fatalf("bearer init: expected 200, got %d (body=%q)", code, payload)
	}
}
