package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// handleGetContext reads a context store, or one key within it.
func (s *Server) handleGetContext(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	scope, err := req.RequireString("scope")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	id := req.GetString("id", "")
	key := req.GetString("key", "")
	slog.Debug("tool: get_context", "scope", scope, "id", id, "key", key)

	raw, err := s.nrClient.GetContext(ctx, scope, id, key)
	if err != nil {
		slog.Error("get_context failed", "error", err, "scope", scope, "id", id)
		return mcp.NewToolResultError(fmt.Sprintf("reading context: %v", err)), nil
	}
	// An empty store is a legitimate answer and a common one. Saying so beats
	// returning "{}" and letting the model read it as a failure.
	if trimmed := strings.TrimSpace(string(raw)); trimmed == "" || trimmed == "{}" || trimmed == `{"memory":{}}` {
		// For node/flow scope, verify the id actually exists so a typo'd id
		// surfaces as "not found" rather than silently looking like empty context.
		// (Node-RED returns HTTP 200 {} for any unknown id — issue #105.)
		if (scope == "node" || scope == "flow") && id != "" {
			exists, lookupErr := s.nrClient.NodeExists(ctx, id)
			if lookupErr == nil && !exists {
				return mcp.NewToolResultError(fmt.Sprintf(
					"%s id %q not found in current deployment", scope, id,
				)), nil
			}
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"No context values are set for scope %q%s.", scope, describeContextTarget(id, key),
		)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", prettyJSON(raw))), nil
}

// describeContextTarget renders the id/key part of a context message, so the
// empty-store reply names exactly what was looked up.

// describeContextTarget renders the id/key part of a context message, so the
// empty-store reply names exactly what was looked up.
func describeContextTarget(id, key string) string {
	switch {
	case id != "" && key != "":
		return fmt.Sprintf(" (id %q, key %q)", id, key)
	case id != "":
		return fmt.Sprintf(" (id %q)", id)
	case key != "":
		return fmt.Sprintf(" (key %q)", key)
	}
	return ""
}

// handleSetContext writes to Node-RED context via the managed helper.
//
// Flow:
//  1. Lazy-provision the helper flow tab + inject + function on first
//     call (see ensureSetContextHelper in setcontext.go).
//  2. Build a JSON body containing the (scope, key, value) and the
//     magic __user_inject_props__ field that makes the POST
//     /inject/:id body become the inject's msg on Node-RED 5.x.
//  3. POST the body to /inject/<helper-inject-id>. The function node
//     downstream reads msg and writes to global/flow/context.
//
// Scope "node" writes to the helper's function node's own context;
// scope "flow" writes to the helper's flow context. Both are
// limitations of how a function node reaches context, not of this
// tool — the description states this so callers do not assume
// arbitrary-target writes.

// handleSetContext writes to Node-RED context via the managed helper.
//
// Flow:
//  1. Lazy-provision the helper flow tab + inject + function on first
//     call (see ensureSetContextHelper in setcontext.go).
//  2. Build a JSON body containing the (scope, key, value) and the
//     magic __user_inject_props__ field that makes the POST
//     /inject/:id body become the inject's msg on Node-RED 5.x.
//  3. POST the body to /inject/<helper-inject-id>. The function node
//     downstream reads msg and writes to global/flow/context.
//
// Scope "node" writes to the helper's function node's own context;
// scope "flow" writes to the helper's flow context. Both are
// limitations of how a function node reaches context, not of this
// tool — the description states this so callers do not assume
// arbitrary-target writes.
func (s *Server) handleSetContext(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	scope, err := req.RequireString("scope")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	key, err := req.RequireString("key")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	valueRaw, err := req.RequireString("value")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	id := req.GetString("id", "")
	slog.Debug("tool: set_context", "scope", scope, "id", id, "key", key)

	// Scope + id shape mirrors get_context so a caller that reads
	// with one and writes with the other cannot get a silent
	// mismatch. For "global" the id is ignored by the runtime; for
	// the other two it is required AND must point at the helper's
	// own target, because the helper is the only place that can
	// actually accept the write.
	switch scope {
	case "global", "flow", "node":
	default:
		return mcp.NewToolResultError(
			fmt.Sprintf(`scope must be "global", "flow" or "node", got %q`, scope),
		), nil
	}

	var value any
	if err := json.Unmarshal([]byte(valueRaw), &value); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf(
			"value is not valid JSON: %v (pass it as a JSON-encoded string, e.g. \"42\" or \"\\\"hello\\\"\")",
			err,
		)), nil
	}

	helper, err := s.ensureSetContextHelper(ctx)
	if err != nil {
		slog.Error("set_context: provisioning helper failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("provisioning set_context helper: %v", err)), nil
	}

	switch scope {
	case "flow":
		if id == "" {
			return mcp.NewToolResultError(
				`scope "flow" requires an id; call list_flows to find the helper flow labelled "__mcp_context_helper__"`,
			), nil
		}
		if id != helper.flowID && id != setContextHelperFlowID {
			return mcp.NewToolResultError(fmt.Sprintf(
				`scope "flow" can only target the helper's own flow context, so the id must be the helper flow id (got %q); call list_flows to discover it`,
				id,
			)), nil
		}
	case "node":
		if id == "" {
			return mcp.NewToolResultError(
				`scope "node" requires an id; call list_flows to find the function node named "__mcp_context_helper_set__"`,
			), nil
		}
		if id != helper.functionID && id != setContextHelperFunctionID {
			return mcp.NewToolResultError(fmt.Sprintf(
				`scope "node" can only target the helper's function node's own context, so the id must be the helper function id (got %q)`,
				id,
			)), nil
		}
	}

	// Build the inject body. The shape is:
	//   { "scope": "...", "key": "...", "value": <any>, "id": "...",
	//     "__user_inject_props__": [] }
	// The first four become the inject's msg; the last one is the
	// flag that makes the body actually arrive as msg (see
	// InjectNodeWithBody docs and the 20-inject.js handler).
	body := map[string]any{
		"scope":                 scope,
		"key":                   key,
		"value":                 value,
		"id":                    id,
		"__user_inject_props__": []any{},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding set_context body: %v", err)), nil
	}

	if err := s.nrClient.InjectNodeWithBody(ctx, helper.injectID, bodyBytes); err != nil {
		slog.Error("set_context failed", "error", err, "scope", scope, "id", id, "key", key)
		return mcp.NewToolResultError(fmt.Sprintf("dispatching set_context: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf(
		"Set context %s key %q to %s (via helper flow %q, inject %q). "+
			"Read it back with get_context; the helper is reused, not re-created.",
		scope, key, prettyJSONValue(value), helper.flowID, helper.injectID,
	)), nil
}

// prettyJSONValue renders a decoded value the way get_context shows one:
// as JSON. Used in the success reply so the caller sees exactly what
// was written (an int, a string, an object) instead of having to parse
// prose.

// prettyJSONValue renders a decoded value the way get_context shows one:
// as JSON. Used in the success reply so the caller sees exactly what
// was written (an int, a string, an object) instead of having to parse
// prose.
func prettyJSONValue(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// handleGetFlowsState returns the current runtime state of Node-RED.
