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
	)

	return cfg, nil
}

func (c *Config) validate() error {
	if c.NodeRedURL == "" {
		return errors.New("NODERED_URL is required")
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
		if c.MCPHTTPToken == "" && c.OAuthIssuer == "" && !isLoopbackAddr(c.MCPHTTPAddr) {
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
