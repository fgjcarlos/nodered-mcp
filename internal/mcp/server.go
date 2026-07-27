// Package mcp wires up the Model Context Protocol server.
//
// It is a thin layer: the heavy lifting is done by the nodered.Client,
// this package just adapts the client's methods into the three MCP
// primitives — tools, resources, prompts.
package mcp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
	"github.com/fgjcarlos/nodered-mcp/internal/oauth"
)

// Server bundles the MCP server and the Node-RED client it talks to.
//
// The registries below record what was actually registered, which is what the
// startup log reports. They are per-Server, not package-level: read-only and
// full servers can coexist in one process and must not see each other's tools.
type Server struct {
	mcpServer *server.MCPServer
	nrClient  *nodered.Client
	// readOnly withholds every tool that mutates the Node-RED instance.
	readOnly bool
	// debugTail streams debug output in the background. Nil when the tail
	// could not be constructed, in which case get_debug_messages says so
	// rather than the server refusing to start.
	debugTail *nodered.DebugTail

	tools     []mcp.Tool
	resources []mcp.Resource
	prompts   []mcp.Prompt
}

// New builds a fully-configured MCP server. It registers all tools,
// resources and prompts declared in the rest of this package. The version
// string is what the server reports to clients during the MCP handshake —
// callers pass the build-time version (see main.version) so every surface
// (binary, server identity, mcpb manifest) stays in sync.
//
// When readOnly is set, only side-effect-free tools are registered: the
// mutating ones are never advertised to the client, so a model cannot call
// what it cannot see. Resources and prompts are read-only surfaces and are
// always registered.
func New(nrClient *nodered.Client, version string, readOnly bool) *Server {
	if version == "" {
		version = "dev"
	}
	s := server.NewMCPServer(
		"nodered-mcp",
		version,
		server.WithToolCapabilities(false),
		server.WithResourceCapabilities(false, false),
		server.WithPromptCapabilities(false),
	)

	srv := &Server{
		mcpServer: s,
		nrClient:  nrClient,
		readOnly:  readOnly,
	}

	// The debug tail is best-effort. A Node-RED that is unreachable, or an
	// unusable base URL, must not stop the other 23 tools from working.
	if tail, err := nodered.NewDebugTail(nrClient, nodered.DefaultDebugBufferSize); err != nil {
		slog.Warn("debug tail unavailable", "error", err)
	} else {
		srv.debugTail = tail
	}

	srv.registerTools()
	srv.registerResources()
	srv.registerPrompts()

	slog.Info("MCP server initialized",
		"tools", len(srv.tools),
		"resources", len(srv.resources),
		"prompts", len(srv.prompts),
		"read_only", readOnly,
	)

	return srv
}

// addReadTool registers a side-effect-free tool. Always registered.
func (s *Server) addReadTool(tool mcp.Tool, handler server.ToolHandlerFunc) {
	s.mcpServer.AddTool(tool, handler)
	s.tools = append(s.tools, tool)
}

// addWriteTool registers a tool that mutates the Node-RED instance — its
// persisted config, its palette, or its running flows. Skipped entirely in
// read-only mode.
func (s *Server) addWriteTool(tool mcp.Tool, handler server.ToolHandlerFunc) {
	if s.readOnly {
		return
	}
	s.mcpServer.AddTool(tool, handler)
	s.tools = append(s.tools, tool)
}

// startDebugTail begins streaming debug output in the background. It is
// deliberately started with the server rather than on first use: the point of
// a tail is to already hold what happened before you thought to ask.
func (s *Server) startDebugTail(ctx context.Context) {
	if s.debugTail == nil {
		return
	}
	go s.debugTail.Run(ctx)
}

// Run starts the server over stdio and blocks until the client
// disconnects or an error occurs.
func (s *Server) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startDebugTail(ctx)

	slog.Info("starting MCP server (stdio transport)")
	return server.ServeStdio(s.mcpServer)
}

// RunHTTP starts the server over the streamable-HTTP transport and blocks
// until the process receives SIGINT/SIGTERM or the listener fails. The MCP
// endpoint is served at <addr>/mcp. On signal it shuts down gracefully.
//
// auth selects the authentication mode:
//   - token != "": require a static Bearer matching token (config.validate
//     decides whether that is mandatory for the bind address).
//   - verifier != nil: require a JWT Bearer, verified against the pinned
//     issuer and audience, signed by a key in verifier's keyset.
//
// Exactly one is required. config.validate rejects both or neither.
func (s *Server) RunHTTP(addr, token string, verifier *oauth.Verifier) error {
	// The http.Server is created first and handed to mcp-go, so the MCP
	// handler can be wrapped in auth while mcp-go keeps owning the listener
	// lifecycle — including closing sessions on shutdown.
	httpServer := &http.Server{}
	mcpHTTP := server.NewStreamableHTTPServer(s.mcpServer,
		server.WithStreamableHTTPServer(httpServer))

	var authMW func(http.Handler) http.Handler
	switch {
	case token != "":
		authMW = func(next http.Handler) http.Handler { return requireBearer(token, next) }
	case verifier != nil:
		authMW = func(next http.Handler) http.Handler { return oauth.RequireOAuth(verifier, next) }
	default:
		// config.validate should have caught this. Fail loudly rather
		// than come up without authentication.
		return errors.New("mcp: RunHTTP called without token or OAuth verifier")
	}

	mux := http.NewServeMux()
	mux.Handle("/mcp", authMW(mcpHTTP))
	httpServer.Handler = mux

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s.startDebugTail(ctx)

	errCh := make(chan error, 1)
	go func() { errCh <- mcpHTTP.Start(addr) }()

	slog.Info("starting MCP server (http transport)",
		"addr", addr, "endpoint", "/mcp",
		"auth_mode", authModeLabel(token, verifier))
	if token == "" && verifier == nil {
		slog.Warn("http transport has no authentication; it is only safe because the " +
			"listen address is loopback-only")
	}
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutdown signal received, stopping http server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return mcpHTTP.Shutdown(shutdownCtx)
	}
}

// authModeLabel returns "bearer" or "oauth" for the startup log line so
// operators can see at a glance which mode the server booted with.
func authModeLabel(token string, verifier *oauth.Verifier) string {
	if token != "" {
		return "bearer"
	}
	if verifier != nil {
		return "oauth"
	}
	return "none"
}
