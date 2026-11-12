package mcp

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// TestLogRequests_PreservesStreamingCapabilities pins issue #335: the
// logging wrapper must not hide http.Flusher (mcp-go gates SSE on it) and
// must expose Unwrap so http.ResponseController reaches the real writer.
func TestLogRequests_PreservesStreamingCapabilities(t *testing.T) {
	var sawFlusher, flushed bool
	var unwrapped http.ResponseWriter
	rr := httptest.NewRecorder()
	handler := logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		sawFlusher = ok
		if ok {
			f.Flush()
			flushed = rr.Flushed
		}
		if u, ok := w.(interface{ Unwrap() http.ResponseWriter }); ok {
			unwrapped = u.Unwrap()
		}
	}))
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/mcp", nil))

	if !sawFlusher {
		t.Fatal("logRequests hid http.Flusher from the inner handler")
	}
	if !flushed {
		t.Fatal("Flush on the wrapper did not reach the underlying writer")
	}
	if unwrapped != rr {
		t.Fatalf("Unwrap: got %T, want the original writer", unwrapped)
	}
}

// TestRunHTTP_StreamingGETDeliversEvents is the issue #335 acceptance
// smoke: over a real connection through the production chain, the GET
// listening stream opens with 200 text/event-stream (not 405 "Streaming
// unsupported") and a server notification arrives flushed while the
// stream is still open.
func TestRunHTTP_StreamingGETDeliversEvents(t *testing.T) {
	c, err := nodered.NewClient(nodered.Options{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	srv := New(c, Options{Version: "test"})
	addr, stop := runHTTPSmoke(t, srv, "")
	defer stop()

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	req := "GET /mcp HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Accept: text/event-stream\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(conn)
	code, headers, payload := readHTTPResponse(t, br)
	if code != http.StatusOK {
		t.Fatalf("GET stream: expected 200, got %d (body=%q)", code, payload)
	}
	if ct := headers.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("GET stream: unexpected Content-Type %q", ct)
	}

	// Headers arrived before the handler returned, so they were flushed.
	// Now push a notification and require it on the open stream.
	srv.mcpServer.SendNotificationToAllClients("notifications/issue335", map[string]any{"ok": true})
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("notification never arrived on the open stream: %v", err)
		}
		if strings.HasPrefix(line, "data:") && strings.Contains(line, "notifications/issue335") {
			return
		}
	}
}
