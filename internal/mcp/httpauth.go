package mcp

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
)

// requireBearer gates an HTTP handler behind a shared bearer token.
//
// The streamable HTTP transport exposes the full tool surface — including
// deploying flows and installing modules — to anything that can reach the
// port. This is the difference between "bound to a network interface" and
// "published to whoever finds it".
//
// It fails closed: an empty configured token denies every request rather than
// degrading into a pass-through. Whether to wrap at all is a configuration
// decision, made in config.validate.
func requireBearer(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" || !bearerMatches(r.Header.Get("Authorization"), token) {
			// The client is told what is required, never what was expected.
			w.Header().Set("WWW-Authenticate", `Bearer realm="nodered-mcp"`)
			slog.Warn("rejected unauthenticated MCP request",
				"remote", r.RemoteAddr, "path", r.URL.Path)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearerMatches reports whether an Authorization header carries the expected
// token. The comparison is constant-time: a byte-by-byte one leaks how much of
// a guess was correct, which is enough to recover a token one character at a
// time.
func bearerMatches(header, want string) bool {
	scheme, presented, found := strings.Cut(header, " ")
	// RFC 7235 makes the scheme name case-insensitive.
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	presented = strings.TrimSpace(presented)
	if presented == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(want)) == 1
}
