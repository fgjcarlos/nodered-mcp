package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
)

// handleListNodes lists the installed palette modules.
func (s *Server) handleListNodes(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slog.Debug("tool: list_nodes")

	nodes, err := s.nrClient.ListNodes(ctx)
	if err != nil {
		slog.Error("list_nodes failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	out, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding nodes: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", string(out))), nil
}

// handleGetNodeInfo returns metadata for a single node module.

// handleGetNodeInfo returns metadata for a single node module.
func (s *Server) handleGetNodeInfo(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	module, err := req.RequireString("module")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: get_node_info", "module", module)

	info, err := s.nrClient.GetNodeInfo(ctx, module)
	if err != nil {
		slog.Error("get_node_info failed", "error", err, "module", module)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	out, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding node info: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", string(out))), nil
}

// handleInstallNode installs a node module from npm.

// handleInstallNode installs a node module from npm.
func (s *Server) handleInstallNode(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	module, err := req.RequireString("module")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	version := req.GetString("version", "")
	slog.Debug("tool: install_node", "module", module, "version", version)

	info, err := s.nrClient.InstallNode(ctx, module, version)
	if err != nil {
		slog.Error("install_node failed", "error", err, "module", module)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	out, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding node info: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Installed %q.\n\n```json\n%s\n```", module, string(out))), nil
}

// handleUninstallNode removes an installed node module.

// handleUninstallNode removes an installed node module.
func (s *Server) handleUninstallNode(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	module, err := req.RequireString("module")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: uninstall_node", "module", module)

	if err := s.nrClient.UninstallNode(ctx, module); err != nil {
		slog.Error("uninstall_node failed", "error", err, "module", module)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Uninstalled %q.", module)), nil
}

// handleEnableNode enables a module or one of its node sets.

// handleEnableNode enables a module or one of its node sets.
func (s *Server) handleEnableNode(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return s.setNodeEnabled(ctx, req, true)
}

// handleDisableNode disables a module or one of its node sets.

// handleDisableNode disables a module or one of its node sets.
func (s *Server) handleDisableNode(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return s.setNodeEnabled(ctx, req, false)
}

// setNodeEnabled backs both enable_node and disable_node.

// setNodeEnabled backs both enable_node and disable_node.
func (s *Server) setNodeEnabled(ctx context.Context, req mcp.CallToolRequest, enabled bool) (*mcp.CallToolResult, error) {
	module, err := req.RequireString("module")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	set := req.GetString("set", "")
	slog.Debug("tool: set_node_enabled", "module", module, "set", set, "enabled", enabled)

	info, err := s.nrClient.SetNodeEnabled(ctx, module, set, enabled)
	if err != nil {
		slog.Error("set_node_enabled failed", "error", err, "module", module, "enabled", enabled)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	verb := "disabled"
	if enabled {
		verb = "enabled"
	}
	out, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding node info: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Module %q %s.\n\n```json\n%s\n```", module, verb, string(out))), nil
}

// handleListBackups lists saved flow snapshots.

// handleSearchNodes queries the public npm registry for node-red-* modules.
func (s *Server) handleSearchNodes(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// mcp-go returns a float64 for number arguments. Default to 10 when the
	// caller leaves it empty; clamp to [1,50] to keep registry responses sane.
	limit := int(req.GetFloat("limit", 10))
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	slog.Debug("tool: search_nodes", "query", query, "limit", limit)

	hits, err := s.nrClient.SearchNodes(ctx, query, limit)
	if err != nil {
		slog.Error("search_nodes failed", "error", err, "query", query)
		return mcp.NewToolResultError(fmt.Sprintf("searching npm registry: %v", err)), nil
	}
	out, err := json.MarshalIndent(hits, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding hits: %v", err)), nil
	}
	if len(hits) == 0 {
		return mcp.NewToolResultText("No node-red modules matched that query."), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("%d hit(s) for %q.\n\n```json\n%s\n```", len(hits), query, string(out))), nil
}
