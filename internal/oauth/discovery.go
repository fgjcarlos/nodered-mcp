// Package oauth validates bearer tokens issued by an external OAuth 2.1 /
// OpenID Connect identity provider. nodered-mcp acts as a Resource Server:
// it does not issue tokens, it only verifies them against the issuer's
// public keys.
//
// The package is deliberately small. JWKS discovery and caching live here
// because they are not large enough to justify their own module, and they
// share a single cache with the verifier.
package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// issuerMetadata is the subset of OpenID Connect Discovery 1.0 and OAuth
// 2.1 Authorization Server Metadata (RFC 8414) that we need. Extra fields
// are ignored on decode so the same struct works for either spec — IdPs in
// the wild tend to expose one or both.
type issuerMetadata struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

// metadataHTTPTimeout caps the discovery fetch. A hung IdP must not stall
// every MCP request that comes in while the cache is cold.
const metadataHTTPTimeout = 10 * time.Second

// FetchDiscovery loads the metadata document at
// <issuer>/.well-known/openid-configuration. The issuer argument is only
// used to build the URL — verifying it matches what the IdP publishes is
// the caller's job, since "what to pin" depends on what the operator
// configured.
//
// The returned metadata carries both `issuer` and `jwks_uri`, so the
// verifier can pin them independently rather than trusting one field that
// happens to point at the other.
func FetchDiscovery(ctx context.Context, issuer string) (*issuerMetadata, error) {
	if issuer == "" {
		return nil, fmt.Errorf("oauth: empty issuer")
	}
	url := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("oauth: building discovery request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	c := &http.Client{Timeout: metadataHTTPTimeout}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Drain a small prefix so the error message is useful without
		// buffering arbitrarily large bodies.
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("oauth: discovery at %s returned %s: %q",
			url, resp.Status, string(preview))
	}
	var md issuerMetadata
	// Unknown fields are intentionally accepted: IdPs add metadata keys
	// at will, and a stricter decoder turns a healthy response into a
	// hard error. Required fields are validated below.
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&md); err != nil {
		return nil, fmt.Errorf("oauth: decoding discovery response: %w", err)
	}
	if md.Issuer == "" || md.JWKSURI == "" {
		return nil, fmt.Errorf("oauth: discovery response missing issuer or jwks_uri")
	}
	return &md, nil
}
