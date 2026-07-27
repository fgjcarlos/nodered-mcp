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
		if c.MCPHTTPToken == "" && !isLoopbackAddr(c.MCPHTTPAddr) {
			return fmt.Errorf(
				"MCP_HTTP_TOKEN is required: %q is reachable from outside this machine, "+
					"and the http transport would otherwise expose full write access to "+
					"Node-RED. Set a token, or bind to 127.0.0.1 for local-only use",
				c.MCPHTTPAddr,
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
