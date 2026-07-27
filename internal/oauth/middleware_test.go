package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ctxKey is unexported so only this package can put Claims on a context.
// Other packages read it via FromContext and cannot collide.
type ctxKey struct{}

var claimsKey = ctxKey{}

// WithClaims returns a context carrying the supplied Claims. Used by
// RequireOAuth so downstream handlers can identify the caller.
func WithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, c)
}

// FromContext extracts the Claims previously stashed by WithClaims, if
// any. Returns nil when the context has none, which is the right signal
// for "this request was not authenticated".
func FromContext(ctx context.Context) *Claims {
	if ctx == nil {
		return nil
	}
	if c, ok := ctx.Value(claimsKey).(*Claims); ok {
		return c
	}
	return nil
}

// ErrNoClaims is returned by FromContextError when no Claims are present.
// Handlers that require authentication can use errors.Is to detect the
// "should not happen" case.
var ErrNoClaims = errors.New("oauth: no claims on context")

// FromContextError is FromContext but returns an error when no Claims
// are present. Prefer FromContext when a missing-Claims path is a
// legitimate "anonymous" code path; use this when it is a bug.
func FromContextError(ctx context.Context) (*Claims, error) {
	c := FromContext(ctx)
	if c == nil {
		return nil, ErrNoClaims
	}
	return c, nil
}

func okHandler() (http.Handler, *bool) {
	reached := false
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}), &reached
}

func TestRequireOAuthAcceptsValidToken(t *testing.T) {
	key := makeRSAKey(t)
	v, cleanup := keysetFromKey(t, "k1", key, "https://issuer/", "nodered-mcp")
	defer cleanup()

	tok := signForTest(t, key, "k1", "https://issuer/", "nodered-mcp", "u1",
		[]string{"mcp:read"}, futureExp())
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()

	// Capture the context the middleware actually saw. The original
	// req.Context() is not modified — the middleware writes onto a new
	// request via WithContext — so the only way to read claims here is
	// to capture them inside the handler.
	var seen *Claims
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	RequireOAuth(v, next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if seen == nil {
		t.Fatal("Claims were not attached to the downstream request context")
	}
	if seen.Subject != "u1" {
		t.Errorf("sub = %q, want u1", seen.Subject)
	}
}

func TestRequireOAuthRejectsBadRequests(t *testing.T) {
	key := makeRSAKey(t)
	v, cleanup := keysetFromKey(t, "k1", key, "https://issuer/", "nodered-mcp")
	defer cleanup()

	tests := []struct {
		name, header string
	}{
		{"no header", ""},
		{"wrong scheme", "Basic abc"},
		{"empty bearer", "Bearer "},
		{"garbage bearer", "Bearer not-a-jwt"},
		{"wrong issuer", "Bearer " + signForTest(t, key, "k1",
			"https://attacker/", "nodered-mcp", "u", nil, futureExp())},
		{"wrong audience", "Bearer " + signForTest(t, key, "k1",
			"https://issuer/", "other-app", "u", nil, futureExp())},
		{"expired", "Bearer " + signForTest(t, key, "k1",
			"https://issuer/", "nodered-mcp", "u", nil, pastExp())},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/mcp", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			next, reached := okHandler()

			RequireOAuth(v, next).ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d", rec.Code)
			}
			if *reached {
				t.Error("rejected request reached the MCP handler")
			}
			if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
				t.Errorf("missing WWW-Authenticate header: %q", got)
			}
		})
	}
}

// A nil verifier is a configuration mistake, not a request error. The
// response is 401 but the log line is ERROR, not warn — that is the
// difference between "client sent the wrong thing" and "the server is
// misconfigured".
func TestRequireOAuthWithNilVerifierDeniesEverything(t *testing.T) {
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer anything")
	rec := httptest.NewRecorder()
	next, reached := okHandler()

	RequireOAuth(nil, next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized || *reached {
		t.Errorf("nil verifier must deny, got %d", rec.Code)
	}
}

// Sanity for the case-insensitive scheme parsing — same property as
// the bearer middleware, since both share the same header shape.
func TestRequireOAuthAcceptsAnyCaseScheme(t *testing.T) {
	key := makeRSAKey(t)
	v, cleanup := keysetFromKey(t, "k1", key, "https://issuer/", "nodered-mcp")
	defer cleanup()

	tok := signForTest(t, key, "k1", "https://issuer/", "nodered-mcp", "u",
		nil, futureExp())

	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		t.Run(scheme, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/mcp", nil)
			req.Header.Set("Authorization", scheme+" "+tok)
			rec := httptest.NewRecorder()
			next, reached := okHandler()
			RequireOAuth(v, next).ServeHTTP(rec, req)
			if !*reached {
				t.Errorf("scheme %q was rejected, got %d", scheme, rec.Code)
			}
		})
	}
}

func futureExp() (t time.Time) { return time.Now().Add(5 * time.Minute) }
func pastExp() (t time.Time)   { return time.Now().Add(-time.Minute) }