package oauth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// happyDiscovery serves the minimum metadata a real IdP would expose.
const happyDiscovery = `{
  "issuer": "https://issuer.example/",
  "jwks_uri": "https://issuer.example/.well-known/jwks.json",
  "authorization_endpoint": "https://issuer.example/auth",
  "token_endpoint": "https://issuer.example/token"
}`

func TestFetchDiscoveryHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, happyDiscovery)
	}))
	defer srv.Close()

	md, err := FetchDiscovery(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("FetchDiscovery: %v", err)
	}
	if md.Issuer != "https://issuer.example/" {
		t.Errorf("issuer = %q, want %q", md.Issuer, "https://issuer.example/")
	}
	if md.JWKSURI != "https://issuer.example/.well-known/jwks.json" {
		t.Errorf("jwks_uri = %q", md.JWKSURI)
	}
}

// Trailing slash on the issuer must not produce //.well-known/...
// The IdP may or may not redirect, but the URL we build has to be sane.
func TestFetchDiscoveryTrimsTrailingSlash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, happyDiscovery)
	}))
	defer srv.Close()

	if _, err := FetchDiscovery(context.Background(), srv.URL+"/"); err != nil {
		t.Fatalf("FetchDiscovery: %v", err)
	}
}

func TestFetchDiscoveryRejectsEmptyIssuer(t *testing.T) {
	if _, err := FetchDiscovery(context.Background(), ""); err == nil {
		t.Fatal("empty issuer must fail")
	}
}

func TestFetchDiscoveryRejectsIncompleteDocument(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"issuer":""}`) // missing jwks_uri
	}))
	defer srv.Close()

	_, err := FetchDiscovery(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "missing issuer or jwks_uri") {
		t.Fatalf("want missing-fields error, got %v", err)
	}
}

func TestFetchDiscoveryRejects404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no metadata here", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := FetchDiscovery(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("404 must surface as an error")
	}
}

func TestFetchDiscoveryRejectsBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{ this is not json`)
	}))
	defer srv.Close()

	_, err := FetchDiscovery(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("malformed JSON must surface as an error")
	}
}

// decoder behaviour we depend on: unknown fields are not an error.
// This guards against the "strict" decoder rejecting a perfectly valid
// IdP response that happens to include custom fields.
func TestFetchDiscoveryIgnoresUnknownFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{
		  "issuer": "https://issuer.example/",
		  "jwks_uri": "https://issuer.example/jwks",
		  "scopes_supported": ["openid"],
		  "custom_field": 42
		}`)
	}))
	defer srv.Close()

	if _, err := FetchDiscovery(context.Background(), srv.URL); err != nil {
		t.Fatalf("extra fields must be ignored, got %v", err)
	}
}

// guard against the body-shape regression where a non-loopback failure
// (e.g. truncated preview) silently passes through.
func TestFetchDiscoveryIncludesStatusInError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "boom")
	}))
	defer srv.Close()

	_, err := FetchDiscovery(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("5xx must surface")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error must mention status, got %q", err.Error())
	}
}
