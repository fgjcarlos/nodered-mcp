package oauth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
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

// RequireOAuth wraps an HTTP handler behind JWT-bearer authentication.
//
// It fails closed in two ways: a nil verifier denies everything rather
// than degrading to a pass-through, and a malformed/expired/wrong-issuer
// token is denied rather than logged-through. The failure reason is
// logged with the remote address and path; the response is intentionally
// terse so it cannot leak the verifier's expectations back to the client.
func RequireOAuth(v *Verifier, next http.Handler) http.Handler {
	if v == nil {
		// Misconfiguration: refuse every request rather than serve
		// unauthenticated traffic.
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			slog.Error("oauth middleware constructed without a verifier",
				"remote", r.RemoteAddr, "path", r.URL.Path)
			unauthorized(w)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("Authorization")
		token, ok := bearerToken(raw)
		if !ok {
			unauthorized(w)
			return
		}
		claims, err := v.Verify(token)
		if err != nil {
			slog.Warn("rejected unauthenticated MCP request",
				"remote", r.RemoteAddr, "path", r.URL.Path, "reason", err.Error())
			unauthorized(w)
			return
		}
		// Stash the claims on the context so handlers downstream can
		// tell who they are talking to. Keeping it in the context (not a
		// package global) is what makes the middleware composable.
		ctx := WithClaims(r.Context(), claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// bearerToken parses a Bearer authorization header. RFC 7235 makes the
// scheme name case-insensitive, and clients in the wild differ.
func bearerToken(header string) (string, bool) {
	scheme, presented, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	presented = strings.TrimSpace(presented)
	if presented == "" {
		return "", false
	}
	return presented, true
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="nodered-mcp"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
