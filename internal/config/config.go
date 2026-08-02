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
	// NodeRedBackupKeep is the maximum number of backup files to retain.
	// Defaults to 50. Set NODERED_BACKUP_KEEP=0 to disable pruning.
	NodeRedBackupKeep int
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
	// MCPHTTPRatePerSec is the steady-state per-source-IP request rate
	// (requests/sec) the HTTP transport allows. Defaults to 1.0; tune via
	// MCP_HTTP_RATE_PER_SEC. The bucket refills at this rate so a legit
	// agent that occasionally bursts can still get work done; a flooder
	// gets throttled. Issue #90.
	MCPHTTPRatePerSec float64
	// MCPHTTPRateBurst is the bucket size (max concurrent requests in a
	// single burst) the rate limiter allows per source IP. Defaults to 10;
	// tune via MCP_HTTP_RATE_BURST. A burst of 10 covers a typical agent
	// loop (initialize + a few tool calls in flight) without making
	// brute-force cheap.
	MCPHTTPRateBurst int
	// MCPHTTPRateDisabled opts out of the rate limiter entirely. Set
	// MCP_HTTP_RATE_DISABLED=true for tests, local sandboxes, or any
	// other deployment where the operator is providing throttling at a
	// different layer. Default is to enforce.
	MCPHTTPRateDisabled bool
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
	// MCPAllowInsecureLoopback silences the startup warning emitted when
	// the HTTP transport comes up on a loopback bind with no token and
	// no OAuth verifier. The default (false) always warns, because the
	// loopback-only assumption is silently broken by a reverse-proxy
	// deployment (nginx / Caddy / Traefik forwarding to 127.0.0.1) —
	// see issue #89. Operators who understand the trap and have
	// upstream auth (mTLS at the proxy, IP allowlist, etc.) can set
	// MCP_ALLOW_INSECURE_LOOPBACK=1 to acknowledge the risk and silence
	// the warning. This flag does NOT weaken any guard; it only stops
	// the nag at startup.
	MCPAllowInsecureLoopback bool
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
		MCPLogLevel:      strings.ToLower(getEnv("MCP_LOG_LEVEL", "info")),		MCPTransport:     strings.ToLower(getEnv("MCP_TRANSPORT", "stdio")),
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

	// Issue #109: NODERED_BACKUP_KEEP controls how many backup files to retain.
	// Default 50; set to 0 to disable pruning. The zero value is resolved to
	// the default inside nodered.NewClient, so we use -1 as the "explicitly
	// disabled" sentinel here and translate 0-from-env to -1.
	cfg.NodeRedBackupKeep = 0 // 0 → default (resolved by NewClient)
	if v := os.Getenv("NODERED_BACKUP_KEEP"); v != "" {
		n, parseErr := strconv.Atoi(v)
		if parseErr != nil || n < 0 {
			return nil, fmt.Errorf("NODERED_BACKUP_KEEP must be a non-negative integer, got %q", v)
		}
		if n == 0 {
			cfg.NodeRedBackupKeep = -1 // explicit opt-out: pass -1 so NewClient skips pruning
		} else {
			cfg.NodeRedBackupKeep = n
		}
	}

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

	// Issue #89: opt-out for the loopback-without-token warning. The
	// option is intentionally separate from MCP_HTTP_TOKEN / OAuth so
	// operators who intentionally expose the transport (e.g. behind a
	// reverse proxy with upstream auth) can keep the rest of the
	// config clean and still silence the nag at startup.
	allowInsecureLoopback, err := strconv.ParseBool(getEnv("MCP_ALLOW_INSECURE_LOOPBACK", "false"))
	if err != nil {
		return nil, fmt.Errorf("parsing MCP_ALLOW_INSECURE_LOOPBACK: %w", err)
	}
	cfg.MCPAllowInsecureLoopback = allowInsecureLoopback

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

	// Issue #90: per-source-IP token-bucket rate limit. Default 1 req/s
	// with a burst of 10 — tight enough to make brute-force expensive,
	// loose enough that a legit agent loop (initialize + a handful of
	// tool calls) is unaffected. Operators tune via MCP_HTTP_RATE_PER_SEC
	// and MCP_HTTP_RATE_BURST, or opt out entirely with
	// MCP_HTTP_RATE_DISABLED=true.
	cfg.MCPHTTPRatePerSec = 1.0
	if v := os.Getenv("MCP_HTTP_RATE_PER_SEC"); v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("MCP_HTTP_RATE_PER_SEC must be a positive number, got %q", v)
		}
		cfg.MCPHTTPRatePerSec = n
	}
	cfg.MCPHTTPRateBurst = 10
	if v := os.Getenv("MCP_HTTP_RATE_BURST"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("MCP_HTTP_RATE_BURST must be a positive integer, got %q", v)
		}
		cfg.MCPHTTPRateBurst = n
	}
	rateDisabled, err := strconv.ParseBool(getEnv("MCP_HTTP_RATE_DISABLED", "false"))
	if err != nil {
		return nil, fmt.Errorf("parsing MCP_HTTP_RATE_DISABLED: %w", err)
	}
	cfg.MCPHTTPRateDisabled = rateDisabled

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
		"allow_insecure_loopback", cfg.MCPAllowInsecureLoopback,
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
