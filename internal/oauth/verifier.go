package oauth

import (
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// Claims captures the subset of JWT claims we read after a successful
// verification. Anything the verifier did not look at is not exposed, so
// a future addition cannot accidentally widen trust by reading extra
// fields from a token.
type Claims struct {
	Subject  string   `json:"sub"`
	Issuer   string   `json:"iss"`
	Audience []string `json:"aud"`
	Scope    string   `json:"scope,omitempty"`
	// raw is the full claim set, kept around for handlers that want to
	// peek at provider-specific fields without us promising to support
	// them. They go through Claims.Raw() rather than being promoted.
	raw map[string]any
}

// Raw returns the full claim set. Callers should treat it as
// untrusted-user input: never use a raw claim for an authorisation
// decision without validating it the same way Verify validates the
// standard claims.
func (c *Claims) Raw() map[string]any { return c.raw }

// Verifier validates JWTs against a pinned issuer and audience, looking
// up the signing key in a Keyset by kid.
//
// The verifier is read-only and goroutine-safe. Construct one at startup
// and share it across HTTP handlers.
type Verifier struct {
	issuer   string
	audience string
	keys     *Keyset

	parser *jwt.Parser
}

// NewVerifier builds a Verifier. issuer and audience must be non-empty
// (the operator's intent is to pin them — an empty pin would silently
// accept any issuer/audience).
func NewVerifier(issuer, audience string, keys *Keyset) (*Verifier, error) {
	if issuer == "" {
		return nil, errors.New("oauth: verifier requires an issuer")
	}
	if audience == "" {
		return nil, errors.New("oauth: verifier requires an audience")
	}
	if keys == nil {
		return nil, errors.New("oauth: verifier requires a keyset")
	}
	// jwt.WithValidMethods pins the signing algorithm whitelist at the
	// parser level. The classic "alg: none" attack dies here.
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"RS256", "RS384", "RS512",
			"ES256", "ES384", "ES512"}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
	)
	return &Verifier{
		issuer:   issuer,
		audience: audience,
		keys:     keys,
		parser:   parser,
	}, nil
}

// Verify parses and validates tokenString. On success the returned
// Claims expose the standard fields; Raw gives access to provider extras.
//
// Failures are intentionally coarse at this layer: callers care about
// "valid" vs "not", not why. The wrapped error preserves the detail for
// the slog line.
func (v *Verifier) Verify(tokenString string) (*Claims, error) {
	keyFn := func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("missing kid in JWT header")
		}
		key, err := v.keys.KeyByID(kid)
		if err != nil {
			return nil, err
		}
		return key.PublicKey()
	}

	parsed, err := v.parser.Parse(tokenString, keyFn)
	if err != nil {
		return nil, fmt.Errorf("oauth: %w", err)
	}

	mapClaims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("oauth: unexpected claim type")
	}

	c := &Claims{raw: mapClaims}
	if s, ok := mapClaims["sub"].(string); ok {
		c.Subject = s
	}
	if s, ok := mapClaims["iss"].(string); ok {
		c.Issuer = s
	}
	if s, ok := mapClaims["scope"].(string); ok {
		c.Scope = s
	}
	// `aud` may be a single string or an array, per RFC 7519 §4.1.3.
	switch a := mapClaims["aud"].(type) {
	case string:
		c.Audience = []string{a}
	case []any:
		for _, item := range a {
			if s, ok := item.(string); ok {
				c.Audience = append(c.Audience, s)
			}
		}
	}
	return c, nil
}