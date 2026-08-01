// Package mcp wires up the Model Context Protocol server.
//
// It is a thin layer: the heavy lifting is done by the nodered.Client,
// this package just adapts the client's methods into the three MCP
// primitives — tools, resources, prompts.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/fgjcarlos/nodered-mcp/internal/config"
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
	// debugStream remembers whether the operator opted in to opening the
	// /comms WebSocket. When false, debugTail stays nil and
	// startDebugTail is a no-op. The field is stored on the Server so a
	// future code path can report it without poking into debugTail.
	debugStream bool
	// debugTail streams debug output in the background. Nil when the tail
	// could not be constructed, in which case get_debug_messages says so
	// rather than the server refusing to start.
	debugTail *nodered.DebugTail
	// statusTail mirrors debugTail for node-status events on the same
	// /comms WebSocket (issue #51). Nil when the tail could not be
	// constructed, in which case get_node_status reports a clear
	// "stream unavailable" message. The tail lives independently of
	// debugTail because debug and status have different lifetimes
	// (consume-and-forget vs remember-the-latest), but they share the
	// MCP_DEBUG_STREAM opt-in for the same reason: the /comms dial can
	// crash the runtime on some Node-RED versions.
	statusTail *nodered.StatusTail

	// ctxHelper tracks the on-runtime helper backing the set_context tool
	// (issue #52). It is provisioned lazily on the first set_context call
	// and reused across calls — the same inject + function node pair stays
	// in the flow config until the operator deletes the flow tab.
	ctxHelper *setContextHelper

	// denylist reports whether a given node type is forbidden by the
	// operator-configured MCP_NODE_DENYLIST. Issue #81: defaults to a
	// closure that allows every type when nil (no denylist configured).
	// Stored as a closure so tests can swap in any predicate.
	denylist func(string) bool

	tools     []mcp.Tool
	resources []mcp.Resource
	prompts   []mcp.Prompt
}

// Options configures a Server. It exists so the constructor signature
// can grow with new knobs without churning every test call site. A zero
// Options gives the dev-friendly defaults the MCP server has always
// shipped with (no denylist — issue #81's defaults are applied at the
// config layer, not here, so tests can opt out explicitly).
type Options struct {
	// Version is what the server reports during the MCP handshake.
	// Callers pass the build-time version (see main.version) so every
	// surface (binary, server identity, mcpb manifest) stays in sync.
	Version string
	// ReadOnly withholds every tool that mutates the Node-RED instance.
	ReadOnly bool
	// DebugStream opts in to opening the /comms WebSocket tail at
	// server start. See MCP_DEBUG_STREAM / --debug-stream docs.
	DebugStream bool
	// NodeDenylist is the set of node types the write tools must
	// refuse to deploy. Empty slice (or nil) means "no denylist":
	// every node type is allowed. See config.Load for the
	// operator-facing toggle MCP_NODE_DENYLIST (issue #81).
	NodeDenylist []string
}

// buildDenylist turns a slice of node types into a closure the write
// tools can call per-node. The closure form lets tests inject any
// predicate they like (the slice path is reserved for production). A
// nil or empty slice yields a pass-through closure: empty denylist is
// the documented opt-out, not a regression.
func buildDenylist(types []string) func(string) bool {
	if len(types) == 0 {
		return func(string) bool { return false }
	}
	set := make(map[string]struct{}, len(types))
	for _, t := range types {
		set[t] = struct{}{}
	}
	return func(t string) bool {
		_, ok := set[t]
		return ok
	}
}

// New builds a fully-configured MCP server. It registers all tools,
// resources and prompts declared in the rest of this package.
//
// When readOnly is set, only side-effect-free tools are registered: the
// mutating ones are never advertised to the client, so a model cannot call
// what it cannot see. Resources and prompts are read-only surfaces and are
// always registered.
//
// When debugStream is false, the WebSocket tail to /comms is not started
// at all — neither constructed nor registered with the server. This is the
// default and the safe one: starting a /comms dial on some Node-RED
// versions (notably :latest) crashes the runtime via a bug in
// @node-red/editor-api/auth/tokens.js. Operators who need debug streaming
// opt in explicitly via MCP_DEBUG_STREAM=on.
//
// opts.NodeDenylist is wired into the server's denylist closure. The
// write tools call it before forwarding any node payload to Node-RED
// (issue #81 — defense in depth against RCE via the "exec" / "system"
// node types). Empty slice (or nil) means "no denylist": every node type
// is allowed. See config.Load for the operator-facing toggle
// MCP_NODE_DENYLIST.
func New(nrClient *nodered.Client, opts Options) *Server {
	version := opts.Version
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
		mcpServer:   s,
		nrClient:    nrClient,
		readOnly:    opts.ReadOnly,
		debugStream: opts.DebugStream,
		denylist:    buildDenylist(opts.NodeDenylist),
	}

	// The debug tail is best-effort and opt-in. A Node-RED that is
	// unreachable, or one that crashes on the /comms handshake (Node-RED
	// :latest as of writing), must not stop the other 23 tools from working
	// and must not bring the runtime down by triggering the upstream bug.
	if !opts.DebugStream {
		slog.Info("debug stream disabled; set MCP_DEBUG_STREAM=on to enable")
	} else {
		if tail, err := nodered.NewDebugTail(nrClient, nodered.DefaultDebugBufferSize); err != nil {
			slog.Warn("debug tail unavailable", "error", err)
		} else {
			srv.debugTail = tail
		}
		// The status tail shares the same /comms transport and the
		// same opt-in. A failure to construct it (typically: the
		// base URL has an unparseable scheme) is logged but does
		// not stop the rest of the server.
		if tail, err := nodered.NewStatusTail(nrClient); err != nil {
			slog.Warn("status tail unavailable", "error", err)
		} else {
			srv.statusTail = tail
		}
	}

	srv.registerTools()
	srv.registerResources()
	srv.registerPrompts()

	slog.Info("MCP server initialized",
		"tools", len(srv.tools),
		"resources", len(srv.resources),
		"prompts", len(srv.prompts),
		"read_only", opts.ReadOnly,
	)

	return srv
}

// addReadTool registers a side-effect-free tool. Always registered.
//
// The handler is wrapped in withTimeoutRetry so every tool call is bounded
// by toolTimeout and gets up to maxRetries retries on transport-level hangs
// (context.DeadlineExceeded, *net.OpError). The audit of v0.5.12 (28 Jul
// 2026) reported 9 hangs of ~4 minutes each across a session; the inner
// nodered.Client already has its own 30s defaultTimeout, but the outer MCP
// transport (stdio / Streamable HTTP) keeps waiting on the goroutine. The
// wrapper here cuts that wait at toolTimeout and surfaces a typed error.
func (s *Server) addReadTool(tool mcp.Tool, handler server.ToolHandlerFunc) {
	s.mcpServer.AddTool(tool, s.withTimeoutRetry(tool.Name, handler))
	s.tools = append(s.tools, tool)
}

// addWriteTool registers a tool that mutates the Node-RED instance — its
// persisted config, its palette, or its running flows. Skipped entirely in
// read-only mode. Same withTimeoutRetry wrapping as addReadTool.
func (s *Server) addWriteTool(tool mcp.Tool, handler server.ToolHandlerFunc) {
	if s.readOnly {
		return
	}
	s.mcpServer.AddTool(tool, s.withTimeoutRetry(tool.Name, handler))
	s.tools = append(s.tools, tool)
}

// toolTimeout is the per-tool call deadline. Set per the v0.5.12 audit:
// the audit observed 4-minute hangs; 15s is well below that and above
// the largest legitimate write (set_flows on a 50-node instance).
const toolTimeout = 15 * time.Second

// maxRetries is the number of additional attempts after the first call.
// Total worst case is (1 + maxRetries) * toolTimeout = 45s end-to-end.
// Per-issue #42 decision: stay under the 4-minute hang and still cover
// the intermittent transport failures the audit attributed to the
// Node-RED runtime.
const maxRetries = 2

// withTimeoutRetry wraps a tool handler so every call gets a fresh ctx
// with toolTimeout and is retried up to maxRetries times on transport
// errors. Two ways an error reaches us:
//
//  1. The handler returns (nil, err) directly — typical of helpers
//     that did not go through mcp.NewToolResultError.
//  2. The handler returns (result, nil) with result.IsError == true,
//     which is the convention every handle* uses (the audit confirmed
//     this is the universal shape across all 29 tools).
//
// We respect both: in case 2 we inspect the result's first text
// content for transport-error markers so the wrapper does not silently
// treat a "Node-RED hung up" response as a clean answer.
//
// HTTP 4xx/5xx responses from Node-RED (after the handler has
// rewritten them via NewToolResultError) are NOT retried: those are
// the server answering, not hanging. The audit verified the hangs are
// atomic (no partial wire, no backup written for the hung call), so
// re-running the call is safe.
func (s *Server) withTimeoutRetry(toolName string, handler server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var lastErr error
		attempts := maxRetries + 1
		for attempt := 1; attempt <= attempts; attempt++ {
			callCtx, cancel := context.WithTimeout(ctx, toolTimeout)
			res, err := handler(callCtx, req)
			cancel()
			if err == nil && !resultIsTransportError(res) {
				return res, nil
			}
			// Pick the most informative error to keep for the final wrap.
			if err != nil {
				lastErr = err
			} else if res != nil {
				lastErr = errors.New(resultFirstText(res))
			}
			if !isRetryableTransport(err, res) {
				return res, err
			}
			if attempt < attempts {
				slog.Warn("tool call retried after transport error",
					"tool", toolName, "attempt", attempt, "error", lastErr)
			}
		}
		return nil, fmt.Errorf("tool %q failed after %d attempts: %w", toolName, attempts, lastErr)
	}
}

// isRetryableTransport reports whether an error (or a result flagged as
// error) is a transport-level hang that may succeed on retry. HTTP
// 4xx/5xx responses (apiError, "Cannot GET", "not found") are answered
// by the server, so retrying just spams it.
func isRetryableTransport(err error, res *mcp.CallToolResult) bool {
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return true
		}
		var netErr *net.OpError
		if errors.As(err, &netErr) {
			return true
		}
		// The nodered.Client.do wrapper formats transport failures as
		// "calling <method> <url>: <cause>". Reject once we see an
		// APIError (server answered) and let other errors fall through.
		var apiErr *nodered.APIError
		if errors.As(err, &apiErr) {
			return false
		}
		// Untyped errors from net/http/io packages — retry; the next
		// attempt may catch a transient blip.
		return true
	}
	// err == nil but the handler signalled an error result.
	return resultIsTransportError(res)
}

// resultIsTransportError reports whether a CallToolResult whose
// IsError == true was caused by a transport-level hang (the case the
// audit observed) rather than a clean 4xx/5xx answer from Node-RED.
func resultIsTransportError(res *mcp.CallToolResult) bool {
	if res == nil || !res.IsError {
		return false
	}
	text := resultFirstText(res)
	if text == "" {
		return false
	}
	// Strings the nodered.Client.do path emits on transport failure.
	// Keep this list tight: false positives here turn every legitimate
	// 4xx into a retry storm.
	for _, marker := range []string{
		"context deadline exceeded",
		"connection reset",
		"connection refused",
		"EOF",
		"i/o timeout",
		"no such host",
		"network is unreachable",
		"broken pipe",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// resultFirstText returns the first TextContent's text from a result,
// or "" if the result has none. Used to inspect what a handler
// reported when its result carries an error.
func resultFirstText(res *mcp.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		return ""
	}
	return tc.Text
}

// startDebugTail begins streaming debug output in the background. It is
// deliberately started with the server rather than on first use: the point of
// a tail is to already hold what happened before you thought to ask.
//
// The tail is opt-in via MCP_DEBUG_STREAM. When that flag is off,
// debugTail is nil and Run is never started — opening the WebSocket on
// some Node-RED versions (notably :latest as of writing) crashes the
// runtime via @node-red/editor-api/auth/tokens.js.
//
// Since issue #51 the same call also starts the status tail: it
// reuses the same /comms socket and the same opt-in (the upstream
// bug applies to any /comms consumer, not just the debug one).
func (s *Server) startDebugTail(ctx context.Context) {
	if !s.debugStream {
		return
	}
	if s.debugTail != nil {
		go s.debugTail.Run(ctx)
	}
	if s.statusTail != nil {
		go s.statusTail.Run(ctx)
	}
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
// Exactly one is required UNLESS addr is loopback-only, in which case
// config.validate already cleared the "no auth on a public bind" rule and
// the server can come up without credentials. config.validate is the single
// gate that decides whether the listen address is reachable from outside —
// RunHTTP trusts it and never re-decides.
func (s *Server) RunHTTP(addr, token string, verifier *oauth.Verifier) error {
	// signal.NotifyContext makes shutdown "send SIGINT/SIGTERM" the
	// production contract. Tests inject a cancelable context instead
	// so the smoke can drive Shutdown() programmatically.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return s.runHTTP(ctx, addr, token, verifier)
}

// runHTTP is the testable inner loop. It owns the server lifecycle but
// not the shutdown signal — that decision lives in RunHTTP, where the
// signal handling is also documented. The split keeps the smoke test
// honest (real http.Server, real mcp-go, real timeout configuration)
// without giving up the operator-facing signal contract.
func (s *Server) runHTTP(ctx context.Context, addr, token string, verifier *oauth.Verifier) error {
	// The http.Server is created first and handed to mcp-go, so the MCP
	// handler can be wrapped in auth while mcp-go keeps owning the listener
	// lifecycle — including closing sessions on shutdown.
	//
	// Timeouts: ReadHeaderTimeout closes the Slowloris window. ReadTimeout
	// and IdleTimeout cap stuck connections. WriteTimeout is deliberately
	// left at zero because mcp-go's Streamable HTTP transport serves
	// long-lived SSE responses (text/event-stream via http.Flusher); a
	// global WriteTimeout would kill the SSE stream before it has a chance
	// to deliver. mcp-go v0.57.0 (server/streamable_http_handle.go:120)
	// checks for Flusher support and 405s if the writer is non-streaming,
	// so the underlying writer on /mcp is SSE-capable. Tightening
	// WriteTimeout would require a second http.Server with its own mux
	// around the SSE path; that's a larger refactor and out of scope here.
	//
	// ponytail: WriteTimeout=0 is the intentional ceiling for SSE; a
	// per-handler deadline is the upgrade path if a future tool ever
	// produces runaway writes.
	httpServer := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	mcpHTTP := server.NewStreamableHTTPServer(s.mcpServer,
		server.WithStreamableHTTPServer(httpServer))

	var authMW func(http.Handler) http.Handler
	switch {
	case token != "":
		authMW = func(next http.Handler) http.Handler { return requireBearer(token, next) }
	case verifier != nil:
		authMW = func(next http.Handler) http.Handler { return oauth.RequireOAuth(verifier, next) }
	case config.IsLoopbackAddr(addr):
		// Loopback bind: no auth, but the mux must still be reached --
		// use a pass-through middleware rather than nil so the
		// handler below can wrap unconditionally.
		authMW = func(next http.Handler) http.Handler { return next }
	default:
		// config.validate should have caught this. Fail loudly rather
		// than come up without authentication.
		return errors.New("mcp: RunHTTP called without token or OAuth verifier")
	}

	mux := http.NewServeMux()
	// Wrap the handler with logging and panic recovery. mcp-go's streamable
	// HTTP transport does not log requests on its own, so a handler that
	// silently aborts (panic, internal error before write) leaves no trace
	// — the client sees ECONNRESET and the operator sees nothing. These two
	// wrappers make the failure mode visible in the server logs.
	mux.Handle("/mcp", logRequests(recoverPanics(authMW(mcpHTTP))))

	httpServer.Handler = mux

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
