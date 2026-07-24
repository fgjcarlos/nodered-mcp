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
//	nodered-mcp version           print the version and exit
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/fgjcarlos/nodered-mcp/internal/config"
	mcpserver "github.com/fgjcarlos/nodered-mcp/internal/mcp"
	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// version is stamped at build time: go build -ldflags "-X main.version=v1.2.3".
var version = "dev"

// flagEnv maps each CLI flag to the environment variable it overrides. A flag
// set on the command line is exported into its env twin before config.Load
// reads (and validates) the environment — so precedence is flag > env >
// default, with all validation kept in one place.
var flagEnv = map[string]string{
	"url":       "NODERED_URL",
	"token":     "NODERED_TOKEN",
	"transport": "MCP_TRANSPORT",
	"http-addr": "MCP_HTTP_ADDR",
	"log-level": "MCP_LOG_LEVEL",
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
		fmt.Println(version)
		return nil
	case "init":
		return runInit(args)
	case "serve":
		return serve(args)
	default:
		return fmt.Errorf("unknown command %q (use serve|init|version)", cmd)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.String("url", "", "Node-RED base URL (env NODERED_URL)")
	fs.String("token", "", "Node-RED API token (env NODERED_TOKEN)")
	fs.String("transport", "", "MCP transport: stdio|http (env MCP_TRANSPORT)")
	fs.String("http-addr", "", "listen address for http transport, host:port (env MCP_HTTP_ADDR)")
	fs.String("log-level", "", "log level: debug|info|warn|error (env MCP_LOG_LEVEL)")
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

	srv := mcpserver.New(nrClient, version)
	if cfg.MCPTransport == "http" {
		return srv.RunHTTP(cfg.MCPHTTPAddr)
	}
	return srv.Run()
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
