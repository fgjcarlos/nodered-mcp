package oauth

import (
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// signForTest issues a JWT the verifier will accept. Tests use it as the
// "good token" baseline and mutate fields to drive failure paths.
func signForTest(t *testing.T, key *rsa.PrivateKey, kid, iss, aud, sub string, scopes []string, exp time.Time) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss": iss,
		"aud": aud,
		"sub": sub,
		"iat": time.Now().Unix(),
		"exp": exp.Unix(),
	}
	if len(scopes) > 0 {
		claims["scope"] = strings.Join(scopes, " ")
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	signed, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}

// keysetFromKey spins up a JWKS server hosting only the supplied key
// under the given kid. The returned *Verifier and cleanup are paired.
func keysetFromKey(t *testing.T, kid string, key *rsa.PrivateKey, issuer, audience string) (*Verifier, func()) {
	t.Helper()
	n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01})
	srv := newJWKSServer(t, fmt.Sprintf(
		`{"keys":[{"kty":"RSA","kid":%q,"alg":"RS256","use":"sig","n":%q,"e":%q}]}`,
		kid, n, e,
	))
	keys := NewKeyset(srv.URL, time.Minute)
	v, err := NewVerifier(issuer, audience, keys)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v, srv.Close
}

func TestVerifierAcceptsValidToken(t *testing.T) {
	key := makeRSAKey(t)
	v, cleanup := keysetFromKey(t, "k1", key, "https://issuer/", "nodered-mcp")
	defer cleanup()

	good := signForTest(t, key, "k1", "https://issuer/", "nodered-mcp", "user-1",
		[]string{"mcp:read"}, time.Now().Add(5*time.Minute))

	claims, err := v.Verify(good)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "user-1" {
		t.Errorf("sub = %q", claims.Subject)
	}
	if claims.Scope != "mcp:read" {
		t.Errorf("scope = %q", claims.Scope)
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != "nodered-mcp" {
		t.Errorf("aud = %v", claims.Audience)
	}
}

// alg:none must not be accepted even if the rest of the token looks
// right. jwt-go's WithValidMethods covers this; the test guards against
// a future change dropping the option.
func TestVerifierRejectsAlgNone(t *testing.T) {
	key := makeRSAKey(t)
	v, cleanup := keysetFromKey(t, "k1", key, "https://issuer/", "nodered-mcp")
	defer cleanup()

	// Hand-craft a token whose alg is none.
	claims := jwt.MapClaims{
		"iss": "https://issuer/",
		"aud": "nodered-mcp",
		"sub": "user-1",
		"exp": time.Now().Add(time.Minute).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	tok.Header["kid"] = "k1"
	signed, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if _, err := v.Verify(signed); err == nil {
		t.Fatal("alg:none must be rejected")
	}
}

// Wrong issuer: token says one thing, verifier is pinned to another.
// Same for wrong audience. Two assertions, one underlying mechanism
// (WithIssuer / WithAudience in the parser).
func TestVerifierRejectsMismatchedIssuerAndAudience(t *testing.T) {
	key := makeRSAKey(t)
	v, cleanup := keysetFromKey(t, "k1", key, "https://issuer/", "nodered-mcp")
	defer cleanup()

	wrongIss := signForTest(t, key, "k1", "https://attacker/", "nodered-mcp", "u",
		nil, time.Now().Add(time.Minute))
	if _, err := v.Verify(wrongIss); err == nil {
		t.Error("wrong issuer must be rejected")
	}

	wrongAud := signForTest(t, key, "k1", "https://issuer/", "some-other-app", "u",
		nil, time.Now().Add(time.Minute))
	if _, err := v.Verify(wrongAud); err == nil {
		t.Error("wrong audience must be rejected")
	}
}

// Expired tokens must fail; the parser checks exp by default but a
// regression test costs nothing and pins the behaviour.
func TestVerifierRejectsExpiredToken(t *testing.T) {
	key := makeRSAKey(t)
	v, cleanup := keysetFromKey(t, "k1", key, "https://issuer/", "nodered-mcp")
	defer cleanup()

	expired := signForTest(t, key, "k1", "https://issuer/", "nodered-mcp", "u",
		nil, time.Now().Add(-time.Minute))
	if _, err := v.Verify(expired); err == nil {
		t.Fatal("expired token must be rejected")
	}
}

// Token signed by a key that is not in the JWKS — covers the keyFn
// path where kid is present but the keyset does not contain it.
func TestVerifierRejectsTokenSignedByUnknownKey(t *testing.T) {
	signer := makeRSAKey(t)
	// Verifier's keyset only knows about a different key.
	other := makeRSAKey(t)
	v, cleanup := keysetFromKey(t, "k1", other, "https://issuer/", "nodered-mcp")
	defer cleanup()

	tok := signForTest(t, signer, "k1", "https://issuer/", "nodered-mcp", "u",
		nil, time.Now().Add(time.Minute))
	if _, err := v.Verify(tok); err == nil {
		t.Fatal("token signed by an unknown key must be rejected")
	}
}

// Garbage in the token slot. The parser must surface this as an error
// rather than panic or, worse, return a Claims built from junk.
func TestVerifierRejectsGarbage(t *testing.T) {
	key := makeRSAKey(t)
	v, cleanup := keysetFromKey(t, "k1", key, "https://issuer/", "nodered-mcp")
	defer cleanup()

	if _, err := v.Verify("not-a-jwt"); err == nil {
		t.Fatal("garbage must be rejected")
	}
}

// NewVerifier refuses to build without the pins. A nil keyset is just
// as bad: every request would fail with a nil-pointer deref, which is
// the wrong shape of error for a configuration mistake.
func TestNewVerifierRequiresPins(t *testing.T) {
	key := makeRSAKey(t)
	keys := NewKeyset("http://example.invalid/jwks", time.Minute)

	if _, err := NewVerifier("", "aud", keys); err == nil {
		t.Error("empty issuer must fail")
	}
	if _, err := NewVerifier("iss", "", keys); err == nil {
		t.Error("empty audience must fail")
	}
	if _, err := NewVerifier("iss", "aud", nil); err == nil {
		t.Error("nil keyset must fail")
	}
	_ = key
}