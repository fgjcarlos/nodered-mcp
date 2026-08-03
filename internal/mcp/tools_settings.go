package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// handleGetSettings returns the Node-RED server settings as JSON.
func (s *Server) handleGetSettings(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slog.Debug("tool: get_settings")

	raw, err := s.nrClient.GetSettings(ctx)
	if err != nil {
		slog.Error("get_settings failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", prettyJSON(raw))), nil
}

// handleGetDiagnostics returns the runtime diagnostics report.

// handleGetDiagnostics returns the runtime diagnostics report.
func (s *Server) handleGetDiagnostics(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slog.Debug("tool: get_diagnostics")

	raw, err := s.nrClient.GetDiagnostics(ctx)
	if err != nil {
		slog.Error("get_diagnostics failed", "error", err)
		// A 404 here almost always means an older Node-RED rather than a real
		// fault, and that is worth saying rather than leaving the model to guess.
		var apiErr *nodered.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return mcp.NewToolResultError(
				"this Node-RED does not expose /diagnostics (added in 3.1). " +
					"Use get_settings for what configuration is available instead.",
			), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", prettyJSON(raw))), nil
}

// handleListPlugins returns the editor plugins loaded by the runtime.

// handleListPlugins returns the editor plugins loaded by the runtime.
func (s *Server) handleListPlugins(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slog.Debug("tool: list_plugins")

	raw, err := s.nrClient.ListPlugins(ctx)
	if err != nil {
		slog.Error("list_plugins failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	if len(raw) == 0 || string(raw) == "[]" {
		return mcp.NewToolResultText("No editor plugins are loaded."), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", prettyJSON(raw))), nil
}

// handleGetDebugMessages returns recent debug-node output from the tail.
