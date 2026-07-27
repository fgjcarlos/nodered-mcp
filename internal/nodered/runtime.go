package nodered

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// This file covers the runtime-introspection endpoints: what the instance is
// running on, what plugins it loaded, and what state its flows are holding.
// All three are read-only and all three are passed through as opaque JSON —
// Node-RED owns these shapes and they change between versions, so anything we
// parsed into a struct we would eventually drop.

// Diagnostics is the runtime report from GET /diagnostics: Node.js version and
// memory, OS and container detection, and the effective settings.
type Diagnostics = json.RawMessage

// Plugins is the list of loaded editor plugins from GET /plugins.
type Plugins = json.RawMessage

// ContextValue is a context store reading. A whole store comes back keyed by
// store name — {"memory":{"temperature":{"msg":"21.5","format":"number"}}} —
// while a single key comes back unwrapped: {"msg":"21.5","format":"number"}.
type ContextValue = json.RawMessage

// GetDiagnostics returns the runtime diagnostics report. Requires Node-RED 3.1
// or later; older versions answer 404. Read-only.
func (c *Client) GetDiagnostics(ctx context.Context) (Diagnostics, error) {
	var raw Diagnostics
	if err := c.do(ctx, "GET", "/diagnostics", nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// ListPlugins returns the editor plugins loaded by the runtime. Read-only.
//
// The Accept header set by do() matters here: asked for HTML, this endpoint
// serves the plugins' editor markup instead of their metadata.
func (c *Client) ListPlugins(ctx context.Context) (Plugins, error) {
	var raw Plugins
	if err := c.do(ctx, "GET", "/plugins", nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// contextScopes are the three scopes the admin API exposes. "flow" and "node"
// address a specific container or node and therefore need an id; "global" is
// instance-wide and takes none.
var contextScopes = map[string]bool{"global": true, "flow": true, "node": true}

// GetContext reads a context store, or a single key within it.
//
// Context is the state flows keep between messages, and it is invisible in the
// flow JSON — a flow can look correct and still misbehave because of what it
// stored earlier. This is the only way to see that from outside the editor.
//
// scope must be "global", "flow" or "node". flow and node require id (a tab or
// node id). key is optional: without it the whole store is returned. Read-only;
// the admin API exposes no way to write context.
func (c *Client) GetContext(ctx context.Context, scope, id, key string) (ContextValue, error) {
	if !contextScopes[scope] {
		return nil, fmt.Errorf("context scope must be \"global\", \"flow\" or \"node\", got %q", scope)
	}

	path := "/context/" + scope
	if scope != "global" {
		// Node-RED answers 404 for /context/flow with no id, which reaches the
		// caller as an unexplained HTTP error. Say what is actually missing.
		if id == "" {
			return nil, fmt.Errorf("scope %q requires an id (the flow tab or node id)", scope)
		}
		if err := checkPathSegment("id", id); err != nil {
			return nil, err
		}
		path += "/" + id
	}
	if key != "" {
		if err := checkPathSegment("key", key); err != nil {
			return nil, err
		}
		path += "/" + key
	}

	var raw ContextValue
	if err := c.do(ctx, "GET", path, nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// checkPathSegment rejects values that would escape the URL segment they are
// substituted into. A key of "../../flows" would otherwise read a different
// endpoint entirely — and these values can come straight from a model.
func checkPathSegment(what, v string) error {
	if strings.ContainsAny(v, `/\`) || strings.Contains(v, "..") {
		return fmt.Errorf("%s must be a single path segment, got %q", what, v)
	}
	return nil
}
