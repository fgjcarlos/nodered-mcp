package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func okHandler() (http.Handler, *bool) {
	reached := false
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}), &reached
}

func TestRequireBearerAcceptsTheConfiguredToken(t *testing.T) {
	next, reached := okHandler()
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	rec := httptest.NewRecorder()

	requireBearer("s3cret", next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if !*reached {
		t.Error("the request never reached the MCP handler")
	}
}

// The scheme name is case-insensitive per RFC 7235, and clients differ.
func TestRequireBearerAcceptsAnyCaseScheme(t *testing.T) {
	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		t.Run(scheme, func(t *testing.T) {
			next, reached := okHandler()
			req := httptest.NewRequest("POST", "/mcp", nil)
			req.Header.Set("Authorization", scheme+" s3cret")
			rec := httptest.NewRecorder()

			requireBearer("s3cret", next).ServeHTTP(rec, req)

			if !*reached {
				t.Errorf("scheme %q was rejected, got %d", scheme, rec.Code)
			}
		})
	}
}

func TestRequireBearerRejectsBadRequests(t *testing.T) {
	tests := []struct {
		name, header string
	}{
		{"no header at all", ""},
		{"wrong token", "Bearer wrong"},
		{"empty token", "Bearer "},
		{"a token that is a prefix of the real one", "Bearer s3cre"},
		{"a token that extends the real one", "Bearer s3cretx"},
		{"the wrong scheme", "Basic s3cret"},
		{"the raw token with no scheme", "s3cret"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			next, reached := okHandler()
			req := httptest.NewRequest("POST", "/mcp", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()

			requireBearer("s3cret", next).ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d", rec.Code)
			}
			if *reached {
				t.Error("an unauthorised request reached the MCP handler")
			}
			// Without this header a client cannot tell it needs to authenticate.
			if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
				t.Errorf("missing or wrong WWW-Authenticate header: %q", got)
			}
		})
	}
}

// Fail closed: a middleware built with no token must not become a pass-through.
// Configuration decides whether to wrap at all; if it wraps, it must enforce.
func TestRequireBearerWithNoConfiguredTokenDeniesEverything(t *testing.T) {
	for _, header := range []string{"", "Bearer ", "Bearer anything"} {
		next, reached := okHandler()
		req := httptest.NewRequest("POST", "/mcp", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()

		requireBearer("", next).ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized || *reached {
			t.Errorf("header %q: an empty configured token must deny, got %d", header, rec.Code)
		}
	}
}

// The response must not echo the expected token, nor confirm how much of a
// guess was right.
func TestRequireBearerDoesNotLeakTheToken(t *testing.T) {
	next, _ := okHandler()
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()

	requireBearer("s3cret", next).ServeHTTP(rec, req)

	body := rec.Body.String() + rec.Header().Get("WWW-Authenticate")
	if strings.Contains(body, "s3cret") {
		t.Errorf("the configured token leaked into the response: %q", body)
	}
}
