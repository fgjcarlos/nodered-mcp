package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// handleListSubflows returns every subflow definition installed in
// the runtime, as a JSON array.
func (s *Server) handleListSubflows(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slog.Debug("tool: list_subflows")

	list, err := s.nrClient.ListSubflows(ctx)
	if err != nil {
		slog.Error("list_subflows failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	// An empty list is a common answer on a fresh instance; render
	// it as [] so callers do not have to special-case the empty
	// string. Wrap in a top-level array for the same reason the
	// other list endpoints do.
	if list == nil {
		list = nodered.SubflowList{}
	}
	out, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding subflows: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", string(out))), nil
}

// handleGetSubflow returns a single subflow definition by id.

// handleGetSubflow returns a single subflow definition by id.
func (s *Server) handleGetSubflow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: get_subflow", "id", id)

	raw, err := s.nrClient.GetSubflow(ctx, id)
	if err != nil {
		slog.Error("get_subflow failed", "error", err, "id", id)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", prettyJSON(raw))), nil
}

// handleCreateSubflow installs a new subflow definition. The subflow
// argument accepts either a JSON object or a JSON-encoded string.

// handleCreateSubflow installs a new subflow definition. The subflow
// argument accepts either a JSON object or a JSON-encoded string.
func (s *Server) handleCreateSubflow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw, err := subflowParam(req, "subflow")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: create_subflow")

	created, err := s.nrClient.CreateSubflow(ctx, raw)
	if err != nil {
		slog.Error("create_subflow failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Subflow created (a backup was taken first).\n\n```json\n%s\n```", prettyJSON(created))), nil
}

// handleUpdateSubflow replaces a subflow definition by id. The
// subflow argument accepts either a JSON object or a JSON-encoded
// string; its id field must match the path id.

// handleUpdateSubflow replaces a subflow definition by id. The
// subflow argument accepts either a JSON object or a JSON-encoded
// string; its id field must match the path id.
func (s *Server) handleUpdateSubflow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	raw, err := subflowParam(req, "subflow")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: update_subflow", "id", id)

	if err := s.nrClient.UpdateSubflow(ctx, id, raw); err != nil {
		slog.Error("update_subflow failed", "error", err, "id", id)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Subflow %q updated (a backup was taken first).", id)), nil
}

// handleDeleteSubflow removes a subflow definition.

// handleDeleteSubflow removes a subflow definition.
func (s *Server) handleDeleteSubflow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: delete_subflow", "id", id)

	if err := s.nrClient.DeleteSubflow(ctx, id); err != nil {
		slog.Error("delete_subflow failed", "error", err, "id", id)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Subflow %q deleted (a backup was taken first).", id)), nil
}

// handleInstantiateSubflow adds a new instance of a subflow into a
// flow tab. The optional params argument is the same shape an editor
// instance node carries: id (caller-chosen, recommended), name, x,
// y, wires, env, and any custom keys.

// handleInstantiateSubflow adds a new instance of a subflow into a
// flow tab. The optional params argument is the same shape an editor
// instance node carries: id (caller-chosen, recommended), name, x,
// y, wires, env, and any custom keys.
func (s *Server) handleInstantiateSubflow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	flowID, err := req.RequireString("flow_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	subflowID, err := req.RequireString("subflow_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	params, err := instanceParamsParam(req, "params")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: instantiate_subflow", "flow_id", flowID, "subflow_id", subflowID)

	node, err := s.nrClient.InstantiateSubflow(ctx, flowID, subflowID, params)
	if err != nil {
		slog.Error("instantiate_subflow failed", "error", err, "flow_id", flowID, "subflow_id", subflowID)
		return mcp.NewToolResultError(fmt.Sprintf("calling Node-RED: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf(
		"Subflow instance added to flow %q (a backup was taken first). Wire it up with connect_nodes.\n\n```json\n%s\n```",
		flowID, prettyJSON(node),
	)), nil
}

// subflowParam reads a "subflow" argument as either a JSON object
// or a JSON-encoded string. Mirrors flowParam/nodeParam for the
// subflow-specific shape.

// subflowParam reads a "subflow" argument as either a JSON object
// or a JSON-encoded string. Mirrors flowParam/nodeParam for the
// subflow-specific shape.
func subflowParam(req mcp.CallToolRequest, key string) (json.RawMessage, error) {
	args := req.GetArguments()
	v, ok := args[key]
	if !ok {
		return nil, fmt.Errorf("required argument %q not found", key)
	}
	switch x := v.(type) {
	case string:
		raw := json.RawMessage(x)
		if !json.Valid(raw) {
			return nil, fmt.Errorf("%q must be a JSON-encoded subflow object or a subflow object passed directly", key)
		}
		return raw, nil
	case map[string]any:
		raw, err := json.Marshal(x)
		if err != nil {
			return nil, fmt.Errorf("encoding %q: %w", key, err)
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("%q must be a JSON-encoded subflow object or a subflow object passed directly", key)
	}
}

// instanceParamsParam reads an optional "params" argument as either
// a JSON object/array, a JSON-encoded string, or returns nil when
// the key is absent. Used by instantiate_subflow where the caller's
// instance overrides are optional.

// instanceParamsParam reads an optional "params" argument as either
// a JSON object/array, a JSON-encoded string, or returns nil when
// the key is absent. Used by instantiate_subflow where the caller's
// instance overrides are optional.
func instanceParamsParam(req mcp.CallToolRequest, key string) (json.RawMessage, error) {
	args := req.GetArguments()
	v, ok := args[key]
	if !ok || v == nil {
		return nil, nil
	}
	switch x := v.(type) {
	case string:
		raw := json.RawMessage(x)
		if !json.Valid(raw) {
			return nil, fmt.Errorf("%q must be a JSON-encoded object or a JSON object passed directly", key)
		}
		return raw, nil
	case map[string]any:
		raw, err := json.Marshal(x)
		if err != nil {
			return nil, fmt.Errorf("encoding %q: %w", key, err)
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("%q must be a JSON-encoded object or a JSON object passed directly", key)
	}
}

// handleListNodes lists the installed palette modules.
