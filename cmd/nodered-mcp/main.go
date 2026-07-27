// Command nodered-mcp is an MCP (Model Context Protocol) server that
// exposes a Node-RED instance to LLM clients such as Claude Desktop.
//
// Configuration comes from environment variables (see .env.example) and can
// be overridden with flags. The server speaks JSON-RPC over stdio by default,
// or streamable HTTP when --transport http is set.
//
// Usage:
//
//	nodered-mcp [serve] [flags]   run the server (default command)
//	nodered-mcp init [--all]      interactively generate a client config snippet
//	nodered-mcp update [--check]  detect the install channel and upgrade in place
//	nodered-mcp version           print the version and exit
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/fgjcarlos/nodered-mcp/internal/config"
	mcpserver "github.com/fgjcarlos/nodered-mcp/internal/mcp"
	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
	"github.com/fgjcarlos/nodered-mcp/internal/oauth"
)

// version is stamped at build time: go build -ldflags "-X main.version=v1.2.3".
// Release builds go through goreleaser, which sets it. Plain `go install` does
// not apply ldflags, so resolveVersion recovers it from the build info instead.
var version = "dev"

// resolveVersion reports the most accurate version available.
func resolveVersion() string {
	embedded := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		embedded = info.Main.Version
	}
	return pickVersion(version, embedded)
}

// pickVersion chooses between the ldflags stamp and the version the Go
// toolchain embedded. Kept separate from resolveVersion so the precedence
// rules are testable without producing real builds.
func pickVersion(stamped, embedded string) string {
	if stamped != "" && stamped != "dev" {
		return stamped
	}
	// "(devel)" is what the toolchain reports for a plain `go build` in a
	// working tree — it carries no more information than "dev" does.
	if embedded != "" && embedded != "(devel)" {
		return embedded
	}
	return "dev"
}

// flagEnv maps each CLI flag to the environment variable it overrides. A flag
// set on the command line is exported into its env twin before config.Load
// reads (and validates) the environment — so precedence is flag > env >
// default, with all validation kept in one place.
var flagEnv = map[string]string{
	"url":          "NODERED_URL",
	"token":        "NODERED_TOKEN",
	"transport":    "MCP_TRANSPORT",
	"http-addr":    "MCP_HTTP_ADDR",
	"http-token":   "MCP_HTTP_TOKEN",
	"oauth-issuer": "MCP_OAUTH_ISSUER",
	"oauth-aud":    "MCP_OAUTH_AUDIENCE",
	"log-level":    "MCP_LOG_LEVEL",
	"read-only":    "MCP_READ_ONLY",
	"debug-stream": "MCP_DEBUG_STREAM",
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "nodered-mcp: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	// The first non-flag argument, if any, is the subcommand.
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}

	switch cmd {
	case "version":
		fmt.Println(resolveVersion())
		return nil
	case "init":
		return runInit(args)
	case "update":
		return runUpdate(args)
	case "serve":
		return serve(args)
	default:
		return fmt.Errorf("unknown command %q (use serve|init|update|version)", cmd)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.String("url", "", "Node-RED base URL (env NODERED_URL)")
	fs.String("token", "", "Node-RED API token (env NODERED_TOKEN)")
	fs.String("transport", "", "MCP transport: stdio|http (env MCP_TRANSPORT)")
	fs.String("http-addr", "", "listen address for http transport, host:port (env MCP_HTTP_ADDR)")
	fs.String("http-token", "", "bearer token required on the http transport; mandatory unless bound to loopback (env MCP_HTTP_TOKEN)")
	fs.String("oauth-issuer", "", "OAuth 2.1 / OIDC issuer URL; when set, every http request must carry a JWT-bearer from this issuer (env MCP_OAUTH_ISSUER)")
	fs.String("oauth-aud", "", "audience claim the JWT must include; required when oauth-issuer is set (env MCP_OAUTH_AUDIENCE)")
	fs.String("log-level", "", "log level: debug|info|warn|error (env MCP_LOG_LEVEL)")
	fs.Bool("read-only", false, "expose only tools that cannot modify Node-RED (env MCP_READ_ONLY)")
	fs.Bool("debug-stream", false, "open the /comms WebSocket tail at startup to enable debug streaming (env MCP_DEBUG_STREAM). Off by default; some Node-RED versions crash on the handshake.")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	// Flags win over env: export any explicitly-set flag into its env twin
	// before Load reads the environment.
	fs.Visit(func(f *flag.Flag) {
		if env, ok := flagEnv[f.Name]; ok {
			_ = os.Setenv(env, f.Value.String())
		}
	})

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	setupLogger(cfg.MCPLogLevel)

	nrClient, err := nodered.NewClient(nodered.Options{
		BaseURL:   cfg.NodeRedURL,
		Token:     cfg.NodeRedToken,
		Username:  cfg.NodeRedUsername,
		Password:  cfg.NodeRedPassword,
		Insecure:  cfg.NodeRedInsecure,
		BackupDir: cfg.NodeRedBackupDir,
	})
	if err != nil {
		return fmt.Errorf("creating nodered client: %w", err)
	}

	srv := mcpserver.New(nrClient, resolveVersion(), cfg.MCPReadOnly, cfg.MCPDebugStream)
	if cfg.MCPTransport == "http" {
		verifier, err := buildOAuthVerifier(cfg)
		if err != nil {
			return fmt.Errorf("building OAuth verifier: %w", err)
		}
		return srv.RunHTTP(cfg.MCPHTTPAddr, cfg.MCPHTTPToken, verifier)
	}
	return srv.Run()
}

// buildOAuthVerifier assembles a JWT verifier from the OAuth config, when
// OAuth is configured. It returns (nil, nil) for the bearer-token path,
// which is the signal to RunHTTP to use the static-secret middleware.
//
// Discovery happens at startup, not lazily: a misconfigured issuer URL
// must stop the server from coming up, not be discovered by the first
// authenticated request.
func buildOAuthVerifier(cfg *config.Config) (*oauth.Verifier, error) {
	if cfg.OAuthIssuer == "" {
		return nil, nil // bearer mode
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	md, err := oauth.FetchDiscovery(ctx, cfg.OAuthIssuer)
	if err != nil {
		return nil, err
	}
	// Pin both the issuer and the audience the IdP advertises: if they
	// drift from what the operator configured, that is a configuration
	// mismatch the operator needs to see now, not at the first request.
	if md.Issuer != cfg.OAuthIssuer {
		return nil, fmt.Errorf("oauth: issuer mismatch: configured %q but IdP advertises %q",
			cfg.OAuthIssuer, md.Issuer)
	}
	keys := oauth.NewKeyset(md.JWKSURI, 15*time.Minute)
	return oauth.NewVerifier(cfg.OAuthIssuer, cfg.OAuthAudience, keys)
}

// setupLogger configures the default slog logger. We log to stderr on
// purpose: stdout is reserved for JSON-RPC frames over the stdio transport.
func setupLogger(level string) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})
	slog.SetDefault(slog.New(h))
}
