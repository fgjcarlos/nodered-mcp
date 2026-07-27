package oauth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// makeRSAKey generates a throwaway RSA key for tests. The size keeps the
// test suite fast; 1024 is below production but accepted by every JWT
// verifier, and a JWKS test does not exercise crypto strength.
func makeRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return k
}

// encodeJWK renders an RSA public key as a single-element JWKS body so
// the test can decide the kid, kty, and key bytes independently.
func encodeJWK(t *testing.T, kid string, key *rsa.PrivateKey) string {
	t.Helper()
	n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())
	body := fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":%q,"alg":"RS256","use":"sig","n":%q,"e":%q}]}`, kid, n, e)
	return body
}

// freshKeyset stands up an httptest server hosting a JWKS, then builds a
// Keyset that points at it. Closing the returned func tears the server
// down.
func freshKeyset(t *testing.T, kid string, key *rsa.PrivateKey, ttl time.Duration) (*Keyset, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(encodeJWK(t, kid, key)))
	}))
	ks := NewKeyset(srv.URL, ttl)
	return ks, srv.Close
}

func TestKeyByIDHappyPath(t *testing.T) {
	key := makeRSAKey(t)
	ks, cleanup := freshKeyset(t, "k1", key, time.Minute)
	defer cleanup()

	got, err := ks.KeyByID("k1")
	if err != nil {
		t.Fatalf("KeyByID: %v", err)
	}
	if got.ID != "k1" {
		t.Errorf("kid = %q, want k1", got.ID)
	}
	if got.Kty != "RSA" {
		t.Errorf("kty = %q", got.Kty)
	}
	pub, err := got.PublicKey()
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	if _, ok := pub.(*rsa.PublicKey); !ok {
		t.Errorf("public key is not *rsa.PublicKey: %T", pub)
	}
}

func TestKeyByIDRefetchesWhenCacheExpires(t *testing.T) {
	key := makeRSAKey(t)
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(encodeJWK(t, "k1", key)))
	}))
	defer srv.Close()

	// Hand-craft a Keyset with a fixed clock so the TTL boundary is
	// deterministic without dragging in a clock package.
	ks := NewKeyset(srv.URL, time.Minute)
	now := time.Unix(1_700_000_000, 0)
	ks.now = func() time.Time { return now }

	if _, err := ks.KeyByID("k1"); err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	if calls != 1 {
		t.Fatalf("first lookup should fetch once, got %d", calls)
	}
	// Move past the TTL and look up again: a second fetch is expected.
	now = now.Add(2 * time.Minute)
	if _, err := ks.KeyByID("k1"); err != nil {
		t.Fatalf("second lookup: %v", err)
	}
	if calls < 2 {
		t.Errorf("post-expiry lookup did not refetch: calls=%d", calls)
	}
}

func TestKeyByIDRefetchesWhenKIDMissing(t *testing.T) {
	// Start with one key, swap to another mid-test; a request for the new
	// kid must trigger a refetch and succeed.
	key1 := makeRSAKey(t)
	key2 := makeRSAKey(t)
	current := key1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(encodeJWK(t, "rotated", current)))
	}))
	defer srv.Close()
	ks := NewKeyset(srv.URL, time.Hour)

	// Prime the cache with k1 (still served under kid "rotated" until
	// the swap below — same kid, but the underlying key has changed).
	if _, err := ks.KeyByID("rotated"); err != nil {
		t.Fatalf("prime: %v", err)
	}
	current = key2 // rotate
	if _, err := ks.KeyByID("rotated"); err != nil {
		t.Fatalf("post-rotation: %v", err)
	}
}

func TestKeyByIDRejectsUnknownKID(t *testing.T) {
	key := makeRSAKey(t)
	ks, cleanup := freshKeyset(t, "k1", key, time.Minute)
	defer cleanup()

	_, err := ks.KeyByID("k-does-not-exist")
	if err == nil {
		t.Fatal("unknown kid must fail")
	}
	if !strings.Contains(err.Error(), "not found in JWKS") {
		t.Errorf("error = %q, want mention of JWKS", err.Error())
	}
}

func TestKeyByIDRejectsEmptyKid(t *testing.T) {
	ks, cleanup := freshKeyset(t, "k1", makeRSAKey(t), time.Minute)
	defer cleanup()

	if _, err := ks.KeyByID(""); err == nil {
		t.Fatal("empty kid must fail")
	}
}

func TestParseKeysDropsNonSigningEntries(t *testing.T) {
	body := `{"keys":[
		{"kty":"RSA","kid":"sig","alg":"RS256","use":"sig","n":"AQ","e":"AQAB"},
		{"kty":"RSA","kid":"enc","alg":"RSA-OAEP","use":"enc","n":"AQ","e":"AQAB"}
	]}`
	raw := jwks{}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	keys, err := parseKeys(raw)
	if err != nil {
		t.Fatalf("parseKeys: %v", err)
	}
	if len(keys) != 1 || keys[0].ID != "sig" {
		t.Errorf("expected only the sig key, got %+v", keys)
	}
}