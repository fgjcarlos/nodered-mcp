package mcp

// export_flow / import_flow (issue #50) — the editor clipboard model,
// exposed as MCP tools so a flow can be moved between instances (or
// between the editor and an instance) without copy-paste in the UI.
//
// The format is the same one Ctrl+C produces in the editor: a JSON
// array whose single element is the flow tab document. Round-trip
// (export → import → export) returns the same bytes, so a caller can
// diff the two exports and trust any delta to be runtime state, not
// the copy itself.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
)

// handleExportFlow returns the editor clipboard representation of a
// single flow tab. The shape is `[{...tab...}]` — a single-element
// array containing the tab document that GET /flow/:id returned,
// pretty-printed so the LLM (or a human) can read it directly.
//
// Wires round-trip intact because we hand back the bytes Node-RED
// itself produced. No re-encoding, no field dropping.
func (s *Server) handleExportFlow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	slog.Debug("tool: export_flow", "id", id)

	tab, err := s.nrClient.GetFlow(ctx, id)
	if err != nil {
		slog.Error("export_flow failed", "error", err, "id", id)
		return mcp.NewToolResultError(fmt.Sprintf("reading flow: %v", err)), nil
	}

	clipboard, err := wrapTabInClipboardArray(tab)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding clipboard: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", string(clipboard))), nil
}

// handleImportFlow accepts an editor clipboard JSON document and
// creates a new flow tab from it. The tool enforces a single-tab
// import (one element in the array) to match the editor's "copy one
// tab" verb and to keep the import boundary obvious to the caller.
//
// The tab's id is rewritten by Node-RED on POST /flow (it ignores
// what we send), so the returned id is the runtime-assigned one, not
// the one in the clipboard. The caller should re-export to learn it
// if needed.
func (s *Server) handleImportFlow(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw, err := clipboardParam(req, "clipboard")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	tabBytes, err := extractSingleTabFromClipboard(raw)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// Build the nested tab document Node-RED's POST /flow expects.
	// normalizeFlowDoc accepts both the flat-array shape and the
	// nested-tab shape; either is fine, the helper reconciles.
	tab, err := normalizeFlowDoc(tabBytes, "", true)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("clipboard is not a valid flow document: %v", err)), nil
	}

	slog.Debug("tool: import_flow", "bytes", len(tab))

	created, err := s.nrClient.CreateFlow(ctx, tab)
	if err != nil {
		slog.Error("import_flow failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("creating flow: %v", err)), nil
	}
	newID, err := extractFlowID(created)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("created flow has no id: %v (response: %s)", err, string(created))), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf(
		"Flow imported. New id: %q. Node-RED rewrites the tab id on POST /flow; call export_flow with the new id to learn the runtime-assigned one.",
		newID,
	)), nil
}

// wrapTabInClipboardArray produces the editor's clipboard shape: a
// single-element JSON array containing the tab document, pretty-
// printed. Pretty-printing is intentional: the export tool is meant
// to be readable by the LLM (and by humans) and there is no size
// reason to compact it.
func wrapTabInClipboardArray(tab json.RawMessage) ([]byte, error) {
	var prettyTab []byte
	if len(tab) > 0 {
		var anyTab interface{}
		if err := json.Unmarshal(tab, &anyTab); err != nil {
			return nil, fmt.Errorf("tab is not valid JSON: %w", err)
		}
		p, err := json.MarshalIndent(anyTab, "", "  ")
		if err != nil {
			return nil, err
		}
		prettyTab = p
	}
	clipboard := []json.RawMessage{prettyTab}
	out, err := json.MarshalIndent(clipboard, "", "  ")
	if err != nil {
		return nil, err
	}
	return out, nil
}

// extractSingleTabFromClipboard accepts a clipboard array and returns
// the single tab element. The editor's clipboard is `[<tab>, ...]`,
// but a user pasting one tab produces a 1-element array; the tool
// rejects anything else so a "copy several tabs by accident" never
// silently lands as one import.
func extractSingleTabFromClipboard(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("clipboard is empty")
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("clipboard is not a JSON array: %w", err)
	}
	if len(arr) == 0 {
		return nil, fmt.Errorf("clipboard array is empty")
	}
	if len(arr) > 1 {
		return nil, fmt.Errorf(
			"clipboard has %d tabs; import_flow only accepts a single tab — split the import by hand if you want them on separate tabs",
			len(arr),
		)
	}
	return arr[0], nil
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
