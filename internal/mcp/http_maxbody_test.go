package mcp

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// TestRunHTTP_MaxBody_OversizedRequestRejected covers issue #86: a
// request body larger than the configured cap must come back 413, not
// silently buffer the entire payload into memory. We pick a 1 KiB cap
// so the test can advertise 64 KiB cheaply and stay snappy in CI; the
// production cap is 32 MiB (see config.Load).
//
// The Content-Length pre-check in maxBodyHandler fires before any
// handler reads the body, so the test does not have to actually
// deliver the bytes — Go's http.Server is asked only to read headers.
func TestRunHTTP_MaxBody_OversizedRequestRejected(t *testing.T) {
	c, err := nodered.NewClient(nodered.Options{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	srv := New(c, Options{Version: "test", HTTPMaxBody: 1024})
	addr, stop := runHTTPSmoke(t, srv, "")
	defer stop()

	// 64 KiB > 1 KiB cap. We do NOT have to actually write the body —
	// the server rejects on the Content-Length header.
	bodyLen := 64 * 1024
	req := "POST /mcp HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Content-Type: application/json\r\n" +
		fmt.Sprintf("Content-Length: %d\r\n", bodyLen) +
		"Accept: application/json, text/event-stream\r\n" +
		"Connection: close\r\n\r\n"
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write headers: %v", err)
	}

	code, _, _ := readHTTPResponse(t, bufio.NewReader(conn))
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: expected 413, got %d", code)
	}
}

// TestRunHTTP_MaxBody_AcceptsBodyUnderLimit is the happy-path
// counterpart of OversizedRequestRejected: an MCP request whose body
// sits well below the cap must reach the handler and come back 200
// with the server identity in the response. Same 1 KiB cap so the
// contrast with the oversized test is just the body size.
func TestRunHTTP_MaxBody_AcceptsBodyUnderLimit(t *testing.T) {
	c, err := nodered.NewClient(nodered.Options{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	srv := New(c, Options{Version: "test", HTTPMaxBody: 1024})
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
	code, _, payload := readHTTPResponse(t, bufio.NewReader(conn))
	if code != http.StatusOK {
		t.Fatalf("under-limit body: expected 200, got %d (body=%q)", code, payload)
	}
	if !strings.Contains(string(payload), "nodered-mcp") {
		t.Fatalf("under-limit body: response did not mention server identity (body=%q)", payload)
	}
}

// TestRunHTTP_MaxBody_DefaultsApplied is the zero-Options path: a
// caller who passes HTTPMaxBody=0 (or omits the field entirely) must
// still get a bounded handler, not an unbounded mux. The smoke just
// needs to reach a 200 — what we are proving here is that the wiring
// did not crash on the zero value and that requests below 32 MiB keep
// flowing.
func TestRunHTTP_MaxBody_DefaultsApplied(t *testing.T) {
	c, err := nodered.NewClient(nodered.Options{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	srv := New(c, Options{Version: "test"})
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
	code, _, payload := readHTTPResponse(t, bufio.NewReader(conn))
	if code != http.StatusOK {
		t.Fatalf("default cap: expected 200, got %d (body=%q)", code, payload)
	}
}
