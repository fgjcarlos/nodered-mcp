package nodered

import (
	"context"
	"errors"
	"time"
)

// installTimeout bounds install/uninstall calls. npm runs under the hood and
// can take well over the default read timeout, so these get their own budget.
const installTimeout = 5 * time.Minute

// ListNodes returns the set of node modules installed in the running
// Node-RED instance. This is what the editor shows in the palette.
func (c *Client) ListNodes(ctx context.Context) ([]NodeModuleInfo, error) {
	var nodes []NodeModuleInfo
	if err := c.do(ctx, "GET", "/nodes", nil, &nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}

// GetNodeInfo returns the metadata for a single node module. The
// `module` argument is the npm package name (e.g. "node-red-node-mqtt").
func (c *Client) GetNodeInfo(ctx context.Context, module string) (*InstallInfo, error) {
	if module == "" {
		return nil, errors.New("module name is required")
	}
	var info InstallInfo
	if err := c.do(ctx, "GET", "/nodes/"+module, nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// InstallNode installs a node module from npm into the running instance
// (POST /nodes). version is optional; empty installs the latest. This
// mutates the runtime and can take minutes, so it gets a longer deadline.
func (c *Client) InstallNode(ctx context.Context, module, version string) (*InstallInfo, error) {
	if module == "" {
		return nil, errors.New("module name is required")
	}
	ctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()

	body := map[string]string{"module": module}
	if version != "" {
		body["version"] = version
	}
	var info InstallInfo
	if err := c.do(ctx, "POST", "/nodes", body, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// UninstallNode removes an installed node module (DELETE /nodes/:module).
// Node-RED refuses to remove a module whose nodes are still in use; that
// error is surfaced verbatim to the caller.
func (c *Client) UninstallNode(ctx context.Context, module string) error {
	if module == "" {
		return errors.New("module name is required")
	}
	ctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()

	return c.do(ctx, "DELETE", "/nodes/"+module, nil, nil)
}

// SetNodeEnabled enables or disables an installed module, or a single node
// set within it when set is non-empty (PUT /nodes/:module[/:set]). Disabling
// keeps the module installed but inert — useful to debug a broken palette.
func (c *Client) SetNodeEnabled(ctx context.Context, module, set string, enabled bool) (*InstallInfo, error) {
	if module == "" {
		return nil, errors.New("module name is required")
	}
	path := "/nodes/" + module
	if set != "" {
		path += "/" + set
	}
	var info InstallInfo
	if err := c.do(ctx, "PUT", path, map[string]bool{"enabled": enabled}, &info); err != nil {
		return nil, err
	}
	return &info, nil
}
