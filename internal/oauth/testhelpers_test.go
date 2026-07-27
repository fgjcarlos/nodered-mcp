package oauth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newJWKSServer returns an httptest server that always serves the
// supplied body verbatim. Used by tests that need a JWKS endpoint
// without caring about request shape.
func newJWKSServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}