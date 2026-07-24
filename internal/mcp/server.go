// Package mcp wires up the Model Context Protocol server.
//
// It is a thin layer: the heavy lifting is done by the nodered.Client,
// this package just adapts the client's methods into the three MCP
// primitives — tools, resources, prompts.
package mcp

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// Server bundles the MCP server and the Node-RED client it talks to.
type Server struct {
	mcpServer *server.MCPServer
	nrClient  *nodered.Client
}

// New builds a fully-configured MCP server. It registers all tools,
// resources and prompts declared in the rest of this package. The version
// string is what the server reports to clients during the MCP handshake —
// callers pass the build-time version (see main.version) so every surface
// (binary, server identity, mcpb manifest) stays in sync.
func New(nrClient *nodered.Client, version string) *Server {
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
	}

	srv.registerTools()
	srv.registerResources()
	srv.registerPrompts()

	slog.Info("MCP server initialized",
		"tools", len(tools),
		"resources", len(resources),
		"prompts", len(prompts),
	)

	return srv
}

// Run starts the server over stdio and blocks until the client
// disconnects or an error occurs.
func (s *Server) Run() error {
	slog.Info("starting MCP server (stdio transport)")
	return server.ServeStdio(s.mcpServer)
}

// RunHTTP starts the server over the streamable-HTTP transport and blocks
// until the process receives SIGINT/SIGTERM or the listener fails. The MCP
// endpoint is served at <addr>/mcp. On signal it shuts down gracefully.
func (s *Server) RunHTTP(addr string) error {
	httpSrv := server.NewStreamableHTTPServer(s.mcpServer)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Start(addr) }()

	slog.Info("starting MCP server (http transport)", "addr", addr, "endpoint", "/mcp")

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutdown signal received, stopping http server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
}
