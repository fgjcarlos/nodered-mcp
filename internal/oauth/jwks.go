package oauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// Key is one JWK from a JWKS document, reduced to what the verifier needs.
// Using a concrete type (rather than a generic crypto.PublicKey) keeps the
// keyset table small and the cache serialisable.
type Key struct {
	ID string // kid

	Kty string // kty: "RSA", "EC", ...
	Alg string // alg: "RS256", "ES256", ...
	Use string // use: "sig"

	// RSA
	N *big.Int
	E *big.Int

	// EC (P-256 / P-384 / P-521)
	Crv string
	X   *big.Int
	Y   *big.Int
}

// PublicKey returns the underlying crypto key in the form jwt-go expects.
func (k *Key) PublicKey() (any, error) {
	switch k.Kty {
	case "RSA":
		if k.N == nil || k.E == nil {
			return nil, fmt.Errorf("oauth: RSA key %q missing n/e", k.ID)
		}
		return &rsa.PublicKey{N: k.N, E: int(k.E.Int64())}, nil
	case "EC":
		var curve elliptic.Curve
		switch k.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("oauth: unsupported EC curve %q", k.Crv)
		}
		return &ecdsa.PublicKey{Curve: curve, X: k.X, Y: k.Y}, nil
	default:
		return nil, fmt.Errorf("oauth: unsupported key type %q", k.Kty)
	}
}

// jwks is the wire shape of a JWKS document (RFC 7517 §4). Unknown fields
// are ignored — IdPs add metadata keys at will.
type jwks struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid,omitempty"`
	Alg string `json:"alg,omitempty"`
	Use string `json:"use,omitempty"`

	// RSA
	N string `json:"n,omitempty"`
	E string `json:"e,omitempty"`

	// EC
	Crv string `json:"crv,omitempty"`
	X   string `json:"x,omitempty"`
	Y   string `json:"y,omitempty"`
}

// decodeBase64URL parses a base64url-encoded big integer as used in JWKs.
// Padding is not used, so the stdlib encoder needs raw stdlib decoding.
func decodeBase64URL(s string) (*big.Int, error) {
	if s == "" {
		return nil, errors.New("empty base64url value")
	}
	bytes, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("base64url: %w", err)
	}
	return new(big.Int).SetBytes(bytes), nil
}

// parseKeys converts a JWKS payload into the verifier-friendly Key form.
// Entries with use != "sig" or kty not in {RSA, EC} are dropped — we have
// no use for encryption keys here.
func parseKeys(raw jwks) ([]*Key, error) {
	var out []*Key
	for i, j := range raw.Keys {
		if j.Use != "" && j.Use != "sig" {
			continue
		}
		k := &Key{ID: j.Kid, Kty: j.Kty, Alg: j.Alg, Use: j.Use}
		switch j.Kty {
		case "RSA":
			n, err := decodeBase64URL(j.N)
			if err != nil {
				return nil, fmt.Errorf("key %d: n: %w", i, err)
			}
			e, err := decodeBase64URL(j.E)
			if err != nil {
				return nil, fmt.Errorf("key %d: e: %w", i, err)
			}
			k.N, k.E = n, e
		case "EC":
			x, err := decodeBase64URL(j.X)
			if err != nil {
				return nil, fmt.Errorf("key %d: x: %w", i, err)
			}
			y, err := decodeBase64URL(j.Y)
			if err != nil {
				return nil, fmt.Errorf("key %d: y: %w", i, err)
			}
			k.Crv, k.X, k.Y = j.Crv, x, y
		default:
			continue // unknown key type — skip rather than fail the whole set
		}
		out = append(out, k)
	}
	return out, nil
}

// FetchJWKS downloads the JWKS document at jwksURI and returns the
// signing keys. It does not cache; that is the Keyset's job.
func FetchJWKS(jwksURI string) ([]*Key, error) {
	if jwksURI == "" {
		return nil, errors.New("oauth: empty jwks_uri")
	}
	c := &http.Client{Timeout: metadataHTTPTimeout}
	resp, err := c.Get(jwksURI)
	if err != nil {
		return nil, fmt.Errorf("oauth: fetching JWKS %s: %w", jwksURI, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth: JWKS %s returned %s", jwksURI, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		return nil, fmt.Errorf("oauth: reading JWKS body: %w", err)
	}
	var raw jwks
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("oauth: decoding JWKS: %w", err)
	}
	return parseKeys(raw)
}

// defaultMissWindow is the minimum gap between two refreshes triggered by
// an unknown kid. The kid comes from an unverified token header, so the
// requester chooses it: without a window, every invalid token costs a
// JWKS download. One minute still lets a rotation be picked up promptly,
// because the first miss after the window opens refreshes again.
const defaultMissWindow = time.Minute

// Keyset is a TTL-bounded cache around a JWKS fetch. The verifier hits it
// once per request, so the lock is held briefly and contention is not a
// concern at the volumes MCP generates.
//
// # ponytail: single global lock, per-issuer locks if many issuers are
// configured. Acceptable while one issuer is the only supported topology.
type Keyset struct {
	jwksURI string

	mu        sync.RWMutex
	keys      []*Key
	fetchedAt time.Time
	gen       uint64
	ttl       time.Duration

	// refreshMu serialises the refresh attempts: at most one download is
	// in flight, and the callers that wait for it share its result.
	refreshMu sync.Mutex
	// lastMiss is when the last refresh triggered by an unknown kid was
	// attempted. It bounds how often a miss may reach the IdP.
	lastMiss time.Time
	// missWindow is the minimum gap between two miss-triggered refreshes.
	missWindow time.Duration

	// now lets tests freeze time without dragging in a clock package.
	now func() time.Time
}

// NewKeyset constructs a Keyset that re-fetches at most every ttl. A ttl
// of zero disables caching: every KeyByID call refetches. Use that for
// tests; in production 15 minutes matches typical IdP key-rotation
// windows.
//
// A refresh triggered by an unknown kid is bounded by the miss window
// (one minute): the kid comes from an unverified token header, so the
// requester chooses how often the IdP is contacted.
func NewKeyset(jwksURI string, ttl time.Duration) *Keyset {
	return &Keyset{jwksURI: jwksURI, ttl: ttl, missWindow: defaultMissWindow, now: time.Now}
}

// snapshot returns the cached keys, the generation they belong to and the
// time they were fetched at. The generation changes on every successful
// refresh, so a caller can tell whether somebody else refreshed while it
// was not looking.
func (k *Keyset) snapshot() ([]*Key, uint64, time.Time) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.keys, k.gen, k.fetchedAt
}

// fetch downloads the JWKS and replaces the cache. The caller holds
// refreshMu, so only one download runs at a time. Errors are returned
// without disturbing the existing cache, so a transient failure does not
// lock everyone out for the TTL window.
func (k *Keyset) fetch() error {
	keys, err := FetchJWKS(k.jwksURI)
	if err != nil {
		return err
	}
	k.mu.Lock()
	k.keys = keys
	k.fetchedAt = k.now()
	k.gen++
	k.mu.Unlock()
	return nil
}

// refresh replaces the cache with a fresh download. The observed
// generation is the one the caller read before it queued: if somebody
// else refreshed in the meantime, that refresh is shared instead of
// starting a second download.
func (k *Keyset) refresh(observed uint64) error {
	k.refreshMu.Lock()
	defer k.refreshMu.Unlock()

	k.mu.RLock()
	gen := k.gen
	k.mu.RUnlock()
	if gen != observed {
		return nil
	}
	return k.fetch()
}

// refreshOnMiss replaces the cache after an unknown kid, but at most once
// per miss window. Inside the window the cached keys are good enough, and
// the IdP does not get to be a download amplifier.
func (k *Keyset) refreshOnMiss(observed uint64) error {
	k.refreshMu.Lock()
	defer k.refreshMu.Unlock()

	k.mu.RLock()
	gen := k.gen
	k.mu.RUnlock()
	if gen != observed {
		// Somebody else already refreshed for this miss.
		return nil
	}
	if k.now().Sub(k.lastMiss) < k.missWindow {
		return nil
	}
	k.lastMiss = k.now()
	return k.fetch()
}

// KeyByID returns the key with the matching kid, refetching if the cache
// has expired (or if the requested kid is missing from a still-fresh
// cache — the common case when an IdP has just rotated). The refresh
// triggered by a missing kid is bounded by the miss window, and the
// callers that miss at the same time share one download.
func (k *Keyset) KeyByID(kid string) (*Key, error) {
	if kid == "" {
		return nil, errors.New("oauth: empty kid")
	}

	keys, gen, fetchedAt := k.snapshot()
	if len(keys) == 0 || k.now().Sub(fetchedAt) > k.ttl {
		if err := k.refresh(gen); err != nil {
			return nil, err
		}
		keys, gen, _ = k.snapshot()
	}

	if key := findKey(keys, kid); key != nil {
		return key, nil
	}

	// The kid isn't in the cache. Treat that as a rotation signal: one
	// more fetch, and give up if the key is still missing. The kid comes
	// from an unverified token header and the requester chooses it, so
	// the refresh is bounded (one per window) and shared by the callers
	// that miss at the same time.
	if err := k.refreshOnMiss(gen); err != nil {
		return nil, err
	}
	keys, _, _ = k.snapshot()
	if key := findKey(keys, kid); key != nil {
		return key, nil
	}
	return nil, fmt.Errorf("oauth: kid %q not found in JWKS", kid)
}

// findKey returns the key with the matching kid, or nil.
func findKey(keys []*Key, kid string) *Key {
	for _, key := range keys {
		if key.ID == kid {
			return key
		}
	}
	return nil
}
