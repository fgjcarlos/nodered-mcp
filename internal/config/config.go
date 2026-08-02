// Package config loads runtime configuration from environment variables.
//
// All knobs come from env vars so the binary is trivial to wire up from any
// MCP client config (Claude Desktop, Cursor, Cline, etc.) and from container
// runtimes. Defaults are deliberately dev-friendly.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config is the fully-resolved runtime configuration for nodered-mcp.
type Config struct {
	NodeRedURL       string
	NodeRedToken     string
	NodeRedUsername  string
	NodeRedPassword  string
	NodeRedInsecure  bool
	NodeRedBackupDir string
	MCPLogLevel      string
	MCPTransport     string
	// MCPHTTPAddr is the listen address for the "http" transport
	// (host:port). Ignored when MCPTransport is "stdio".
	MCPHTTPAddr string
	// MCPHTTPToken is the shared bearer token required on every request to
	// the HTTP transport. Mandatory when the listen address is reachable from
	// outside this machine; optional for a loopback bind.
	MCPHTTPToken string
	// MCPHTTPMaxBody is the cap on a single HTTP request body, in bytes.
	// Defaults to 32 MiB if unset. Configurable via MCP_HTTP_MAX_BODY.
	// Without an upper bound a client could POST a multi-gigabyte payload
	// over one connection and exhaust memory — the bound is enforced at
	// the http.Server via MaxBytesHandler before any handler runs.
	MCPHTTPMaxBody int
	// OAuthIssuer is the URL of an external OAuth 2.1 / OpenID Connect
	// identity provider. When set, every HTTP request must carry a
	// JWT-bearer issued by it, signed by a key advertised at
	// <issuer>/.well-known/jwks.json, with `iss` equal to OAuthIssuer
	// and `aud` equal to OAuthAudience. Mutual exclusion with
	// MCPHTTPToken: set either, not both.
	OAuthIssuer string
	// OAuthAudience is the value the JWT `aud` claim must match.
	// Required when OAuthIssuer is set; meaningless otherwise.
	OAuthAudience string
	// MCPReadOnly withholds every tool that mutates the Node-RED instance.
	// Point the server at a production instance with this set and the model
	// can inspect and diagnose, but not deploy, install, or inject.
	MCPReadOnly bool
	// MCPDebugStream opts in to opening the /comms WebSocket tail at
	// server start. Defaults to false because the tail crashes some
	// Node-RED versions (see #17). Set true to enable debug streaming.
	MCPDebugStream bool
	// NodeDenylist is the set of node types the MCP write tools must refuse
	// to deploy. The default ("exec,system") defends against RCE on the
	// Node-RED host (issue #81): callers of create_flow / update_flow /
	// add_node / set_flows can otherwise ship a node that executes shell
	// commands as the Node-RED process. Operators who genuinely need those
	// node types can opt out with MCP_NODE_DENYLIST="" — see SECURITY.md.
	NodeDenylist []string
}

// defaultNodeDenylist is the set of node types the MCP write tools refuse
// by default. Issue #81: the Node-RED "exec" and "system" nodes run shell
// commands on the host OS; an MCP caller can otherwise achieve RCE simply
// by writing a flow that contains one of them.
var defaultNodeDenylist = []string{"exec", "system"}

// HasDeniedNodeType reports whether nodeType is in the configured
// denylist. The check is case-sensitive (Node-RED node types are).
func (c *Config) HasDeniedNodeType(nodeType string) bool {
	for _, t := range c.NodeDenylist {
		if t == nodeType {
			return true
		}
	}
	return false
}

// Load reads configuration from the environment. If a .env file exists in
// the working directory it is loaded first (dev convenience); environment
// variables already set always take precedence.
func Load() (*Config, error) {
	// .env is optional — ignore "not found" errors silently.
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("loading .env: %w", err)
	}

	cfg := &Config{
		NodeRedURL:       getEnv("NODERED_URL", "http://localhost:1880"),
		NodeRedToken:     os.Getenv("NODERED_TOKEN"),
		NodeRedUsername:  os.Getenv("NODERED_USERNAME"),
		NodeRedPassword:  os.Getenv("NODERED_PASSWORD"),
		NodeRedBackupDir: getEnv("NODERED_BACKUP_DIR", "backups"),
		MCPLogLevel:      strings.ToLower(getEnv("MCP_LOG_LEVEL", "info")),
		MCPTransport:     strings.ToLower(getEnv("MCP_TRANSPORT", "stdio")),
		MCPHTTPAddr:      getEnv("MCP_HTTP_ADDR", ":8090"),
		MCPHTTPToken:     os.Getenv("MCP_HTTP_TOKEN"),
		OAuthIssuer:      os.Getenv("MCP_OAUTH_ISSUER"),
		OAuthAudience:    os.Getenv("MCP_OAUTH_AUDIENCE"),
	}

	insecure, err := strconv.ParseBool(getEnv("NODERED_INSECURE", "false"))
	if err != nil {
		return nil, fmt.Errorf("parsing NODERED_INSECURE: %w", err)
	}
	cfg.NodeRedInsecure = insecure

	readOnly, err := strconv.ParseBool(getEnv("MCP_READ_ONLY", "false"))
	if err != nil {
		return nil, fmt.Errorf("parsing MCP_READ_ONLY: %w", err)
	}
	cfg.MCPReadOnly = readOnly

	debugStream, err := strconv.ParseBool(getEnv("MCP_DEBUG_STREAM", "false"))
	if err != nil {
		return nil, fmt.Errorf("parsing MCP_DEBUG_STREAM: %w", err)
	}
	cfg.MCPDebugStream = debugStream

	// Issue #86: cap the HTTP request body so a hostile client cannot
	// stream an unbounded payload over one connection. 32 MiB comfortably
	// fits the largest legitimate MCP request (set_flows on a sizable
	// instance) while making a slow-loris-style memory attack expensive.
	cfg.MCPHTTPMaxBody = 32 << 20 // 32 MiB default
	if v := os.Getenv("MCP_HTTP_MAX_BODY"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("MCP_HTTP_MAX_BODY must be a positive integer (bytes), got %q", v)
		}
		cfg.MCPHTTPMaxBody = int(n)
	}

	// Issue #81: MCP_NODE_DENYLIST is a defense-in-depth against RCE via
	// the Node-RED "exec" / "system" nodes (and any other shell-executing
	// node type the operator wants to block). Unset -> apply the default
	// list ("exec,system"); explicit empty -> opt out (no denylist at all).
	if raw, ok := os.LookupEnv("MCP_NODE_DENYLIST"); ok {
		cfg.NodeDenylist = parseNodeDenylist(raw)
	} else {
		cfg.NodeDenylist = append([]string(nil), defaultNodeDenylist...)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	slog.Debug("config loaded",
		"url", cfg.NodeRedURL,
		"auth_token", cfg.NodeRedToken != "",
		"auth_basic", cfg.NodeRedUsername != "",
		"transport", cfg.MCPTransport,
		"http_auth", cfg.MCPHTTPToken != "",
		"oauth_issuer", cfg.OAuthIssuer != "",
		"read_only", cfg.MCPReadOnly,
		"debug_stream", cfg.MCPDebugStream,
	)

	return cfg, nil
}

func (c *Config) validate() error {
	if c.NodeRedURL == "" {
		return errors.New("NODERED_URL is required")
	}
	// SSRF guard: only allow http and https schemes. Other schemes
	// (file://, ftp://, gopher://) could turn the MCP into an SSRF
	// proxy against the Node-RED host or its network.
	u, err := url.Parse(c.NodeRedURL)
	if err != nil {
		return fmt.Errorf("NODERED_URL is not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("NODERED_URL must use http or https scheme, got %q (full value: %q)", u.Scheme, c.NodeRedURL)
	}
	if u.Host == "" {
		return fmt.Errorf("NODERED_URL must include a host, got %q", c.NodeRedURL)
	}
	switch c.MCPTransport {
	case "stdio":
		// ok
	case "http":
		if c.MCPHTTPAddr == "" {
			return errors.New("MCP_HTTP_ADDR is required when MCP_TRANSPORT is \"http\"")
		}
		// The HTTP transport exposes every tool, including deploying flows and
		// installing modules. Binding a network interface without a token
		// publishes that to anyone who can reach the port, so refuse to start
		// rather than come up quietly insecure.
		//
		// OAuth (issuer + audience) is the alternative form of authentication
		// for the HTTP transport: when configured, it satisfies the same
		// "must not come up unauthenticated" rule without requiring a shared
		// static secret.
		if c.MCPHTTPToken == "" && c.OAuthIssuer == "" && !IsLoopbackAddr(c.MCPHTTPAddr) {
			return fmt.Errorf(
				"MCP_HTTP_TOKEN or MCP_OAUTH_ISSUER is required: %q is reachable from outside "+
					"this machine, and the http transport would otherwise expose full write "+
					"access to Node-RED. Set a token, configure OAuth, or bind to 127.0.0.1 "+
					"for local-only use",
				c.MCPHTTPAddr,
			)
		}
		// OAuth pins both issuer and audience: configuring one without the
		// other is almost certainly an operator mistake.
		if (c.OAuthIssuer == "") != (c.OAuthAudience == "") {
			return errors.New(
				"MCP_OAUTH_ISSUER and MCP_OAUTH_AUDIENCE must be set together; " +
					"configuring only one is an insecure half-step",
			)
		}
		// Bearer and OAuth are alternative auth modes. Configuring both is
		// ambiguous: pick one. The runtime would have to choose which one
		// wins on each request, which is a footgun.
		if c.MCPHTTPToken != "" && c.OAuthIssuer != "" {
			return errors.New(
				"MCP_HTTP_TOKEN and MCP_OAUTH_ISSUER are mutually exclusive: " +
					"set the bearer token for static-secret auth, or configure " +
					"OAuth issuer + audience for IdP-issued JWTs",
			)
		}
	default:
		return fmt.Errorf("unsupported MCP_TRANSPORT %q (must be stdio|http)", c.MCPTransport)
	}
	switch c.MCPLogLevel {
	case "debug", "info", "warn", "error":
		// ok
	default:
		return fmt.Errorf("invalid MCP_LOG_LEVEL %q (must be debug|info|warn|error)", c.MCPLogLevel)
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// parseNodeDenylist turns the comma-separated MCP_NODE_DENYLIST value
// into a list of trimmed, non-empty node types. An all-whitespace input
// yields a nil slice, which is the signal "no denylist" — distinct from
// the unset default, which applies defaultNodeDenylist. This split is
// what makes MCP_NODE_DENYLIST="" an opt-out (issue #81).
func parseNodeDenylist(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
