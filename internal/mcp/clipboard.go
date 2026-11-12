package mcp

// export_flow / import_flow (issue #50) — the editor clipboard model,
// exposed as MCP tools so a flow can be moved between instances (or
// between the editor and an instance) without copy-paste in the UI.
//
// The format is the one the editor's export produces (issue #337): a
// flat JSON array whose elements are the tab ({"type":"tab"}), each of
// its nodes, and the global config nodes and subflow definitions they
// depend on. clipboard_convert.go converts between that array and the
// nested document POST /flow takes.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// handleExportFlow returns the editor clipboard for one flow tab,
// pretty-printed so the LLM (or a human) can read it directly. Elements
// are the bytes Node-RED itself stores, so wires and every node field
// round-trip intact.
func (s *Server) handleExportFlow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: export_flow", "id", id)

	current, err := s.currentFlowItems(ctx)
	if err != nil {
		slog.Error("export_flow failed", "error", err, "id", id)
		return toolResultError(err, fmt.Sprintf("reading flows: %v", err))
	}
	items, err := exportClipboard(current, id)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	clipboard, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding clipboard: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", string(clipboard))), nil
}

// handleImportFlow accepts an editor clipboard and creates a new flow tab
// from it. Exactly one element must be a tab, so a "copy several tabs by
// accident" never silently lands as one import. The nested single-tab
// shape earlier releases exported is still accepted.
//
// Node-RED assigns the tab id on POST /flow; member ids are kept unless
// one already exists on this instance, in which case all of them are
// reminted (see clipboardImport.resolve).
func (s *Server) handleImportFlow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw, err := clipboardParam(req, "clipboard")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	doc, err := parseClipboard(raw)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// Refuse a forbidden type before any request, as the nested path did;
	// CreateFlow judges the final document again.
	for _, t := range doc.types {
		if !s.nrClient.NodeTypeAllowed(t) {
			return mcp.NewToolResultError(deniedNodeMessage(t)), nil
		}
	}
	current, err := s.currentFlowItems(ctx)
	if err != nil {
		return toolResultError(err, fmt.Sprintf("reading flows: %v", err))
	}
	nested, reminted, err := doc.resolve(current)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	tab, err := normalizeFlowDoc(nested, "", true)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("clipboard is not a valid flow document: %v", err)), nil
	}

	slog.Debug("tool: import_flow", "bytes", len(tab), "reminted", reminted)

	created, err := s.nrClient.CreateFlow(ctx, tab)
	if err != nil {
		slog.Error("import_flow failed", "error", err)
		return toolResultError(err, fmt.Sprintf("creating flow: %v", err))
	}
	newID, err := extractFlowID(created)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("created flow has no id: %v (response: %s)", err, string(created))), nil
	}
	msg := fmt.Sprintf("Flow imported. New id: %q. Node-RED rewrites the tab id on POST /flow; call export_flow with the new id to learn the runtime-assigned one.", newID)
	if reminted > 0 {
		msg += fmt.Sprintf(" %d node ids already existed on this instance, so every node id of the copy was reminted and its wires and references rewritten.", reminted)
	}
	return mcp.NewToolResultText(msg), nil
}

// currentFlowItems reads the instance's flat configuration.
func (s *Server) currentFlowItems(ctx context.Context) ([]json.RawMessage, error) {
	raw, err := s.nrClient.ListFlows(ctx)
	if err != nil {
		return nil, err
	}
	items := nodered.FlowArray(raw)
	if items == nil {
		return nil, fmt.Errorf("unrecognizable flow configuration from GET /flows")
	}
	return items, nil
}

// clipboardParam reads the "clipboard" argument as either a JSON-
// encoded string or a raw array. Mirrors flowParam's dual-shape
// support: an LLM should not have to serialize a literal by hand.
func clipboardParam(req mcp.CallToolRequest, key string) (json.RawMessage, error) {
	args := req.GetArguments()
	v, ok := args[key]
	if !ok {
		return nil, fmt.Errorf("required argument %q not found", key)
	}
	switch x := v.(type) {
	case string:
		if x == "" {
			return nil, fmt.Errorf("argument %q is empty", key)
		}
		// Validate the string is JSON before returning; better to
		// surface a clear error than let the unmarshal fail later.
		if !json.Valid([]byte(x)) {
			return nil, fmt.Errorf("argument %q is not valid JSON", key)
		}
		return json.RawMessage(x), nil
	case []interface{}:
		// Re-marshal to canonical JSON bytes so the downstream
		// helpers can json.Unmarshal it the same way regardless of
		// whether the input was a string or an array.
		b, err := json.Marshal(x)
		if err != nil {
			return nil, fmt.Errorf("encoding clipboard array: %w", err)
		}
		return b, nil
	case nil:
		return nil, fmt.Errorf("argument %q is null", key)
	default:
		return nil, fmt.Errorf("argument %q must be a JSON array or a JSON-encoded string, got %T", key, v)
	}
}

// extractFlowIDFromCreateResponse is no longer needed here — the
// shared extractFlowID helper in setcontext.go handles the same
// response shape (nested tab object or flat-array element) and
// returns a clean id or a typed error.
