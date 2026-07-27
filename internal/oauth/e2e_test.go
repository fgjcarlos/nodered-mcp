package oauth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/fgjcarlos/nodered-mcp/internal/oauth"
)

// mockIdP hosts the minimum an OpenID Connect provider has to expose:
// a discovery document and a JWKS endpoint with one signing key.
// Returns the base URL operators would put in MCP_OAUTH_ISSUER.
type mockIdP struct {
	srv  *httptest.Server
	keys *rsa.PrivateKey
	kid  string
	iss  string
	aud  string
}

func newMockIdP(t *testing.T) *mockIdP {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	idp := &mockIdP{
		keys: priv,
		kid:  "test-kid",
		iss:  "", // filled in once the server has a URL
		aud:  "nodered-mcp",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, fmt.Sprintf(`{
		  "issuer": %q,
		  "jwks_uri": %q
		}`, idp.iss, idp.iss+"/jwks"))
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(idp.keys.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01})
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"keys":[{"kty":"RSA","kid":%q,"alg":"RS256","use":"sig","n":%q,"e":%q}]}`,
			idp.kid, n, e)
	})
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	idp.iss = idp.srv.URL
	return idp
}

func (idp *mockIdP) signToken(t *testing.T, sub string, exp time.Time) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss": idp.iss,
		"aud": idp.aud,
		"sub": sub,
		"iat": time.Now().Unix(),
		"exp": exp.Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = idp.kid
	signed, err := tok.SignedString(idp.keys)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}

// end-to-end: discovery → JWKS → Verifier → RequireOAuth → handler.
// Standing this up as one test rather than as four separate layers is
// what proves the pieces compose; the lower-level tests in the
// internal/ package cover each piece on its own.
func TestEndToEndValidTokenReachesHandler(t *testing.T) {
	idp := newMockIdP(t)

	md, err := oauth.FetchDiscovery(t.Context(), idp.iss)
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	if md.Issuer != idp.iss || md.JWKSURI != idp.iss+"/jwks" {
		t.Fatalf("discovery fields wrong: %+v", md)
	}
	keys := oauth.NewKeyset(md.JWKSURI, time.Minute)
	v, err := oauth.NewVerifier(idp.iss, idp.aud, keys)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	tok := idp.signToken(t, "user-1", time.Now().Add(time.Minute))

	var reached bool
	var seenSub string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		c := oauth.FromContext(r.Context())
		if c != nil {
			seenSub = c.Subject
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	oauth.RequireOAuth(v, handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if !reached {
		t.Error("handler never ran")
	}
	if seenSub != "user-1" {
		t.Errorf("sub = %q, want user-1", seenSub)
	}
}

// Cross-IdP forgery: token signed by a different keyset must not
// pass, even if the iss claim happens to match.
func TestEndToEndRejectsTokenSignedByDifferentIdP(t *testing.T) {
	idp := newMockIdP(t)
	other := newMockIdP(t)

	md, err := oauth.FetchDiscovery(t.Context(), idp.iss)
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	keys := oauth.NewKeyset(md.JWKSURI, time.Minute)
	v, err := oauth.NewVerifier(idp.iss, idp.aud, keys)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	// Token signed by `other`'s key, with iss matching idp. This is
	// exactly the forgery the verifier must catch.
	tok := other.signToken(t, "attacker", time.Now().Add(time.Minute))

	var reached bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	oauth.RequireOAuth(v, handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	if reached {
		t.Error("forged token reached the handler")
	}
}

// Expired token with the right key and the right issuer must still be
// rejected: the parser enforces exp, the verifier does not relax it.
func TestEndToEndRejectsExpiredToken(t *testing.T) {
	idp := newMockIdP(t)

	md, _ := oauth.FetchDiscovery(t.Context(), idp.iss)
	keys := oauth.NewKeyset(md.JWKSURI, time.Minute)
	v, _ := oauth.NewVerifier(idp.iss, idp.aud, keys)

	tok := idp.signToken(t, "u", time.Now().Add(-time.Minute))

	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	oauth.RequireOAuth(v, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("expired token reached the handler")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
		t.Errorf("missing WWW-Authenticate: %q", got)
	}
}
