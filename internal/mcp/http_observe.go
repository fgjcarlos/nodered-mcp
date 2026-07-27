package mcp

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// logRequests logs every incoming HTTP request after the handler returns.
// mcp-go's streamable HTTP transport writes no per-request line of its own,
// so a handler that closes the connection without responding leaves the
// operator blind — this wrapper makes the failure mode visible.
//
// It also wraps ResponseWriter so the handler's eventual status code is
// reported. The wrapper is allocation-free in the common path: the
// buffered status field is what we log on return.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		slog.Debug("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"remote", r.RemoteAddr,
			"host", r.Host,
		)
	})
}

// recoverPanics turns a handler panic into a logged error plus a 500
// response, rather than letting the connection die silently. mcp-go's
// streamable handler is wrapped with one, and a panic inside it would
// otherwise show up to the client as ECONNRESET with no server-side log.
func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("http handler panicked",
					"method", r.Method,
					"path", r.URL.Path,
					"remote", r.RemoteAddr,
					"panic", rec,
					"stack", string(debug.Stack()),
				)
				// Best-effort: the writer may already have been partially
				// flushed, but if nothing has been written yet this surfaces
				// a real response instead of a closed socket.
				if w.Header().Get("Content-Type") == "" {
					http.Error(w, "internal server error", http.StatusInternalServerError)
				}
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusWriter wraps a ResponseWriter to capture the status code for logging.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}
