package mcp

import (
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// deniedNodeMessage renders the operator-facing refusal for a node type the
// configured policy forbids. The wording names the environment variable that
// carries the policy, because that is what the operator has to change.
func deniedNodeMessage(nodeType string) string {
	return fmt.Sprintf(
		"node type %q is in MCP_NODE_DENYLIST; remove it from the denylist or use a different node type (see SECURITY.md)",
		nodeType,
	)
}

// deniedBackupMessage renders the same refusal for the restore path, where
// the actionable step is editing the backup rather than the live flow.
func deniedBackupMessage(backupName, nodeType string) string {
	return fmt.Sprintf(
		"backup %q contains node type %q, which is in MCP_NODE_DENYLIST; remove it from the denylist, or edit the backup before restoring (see SECURITY.md)",
		backupName, nodeType,
	)
}

// deniedNodeType reports the node type a client-side policy refusal names,
// and whether err was such a refusal at all.
//
// The policy lives in the client (see nodered.SetNodePolicy), because the
// client is the only layer that knows the effective configuration a write
// would deploy. The MCP layer owns the wording, so it recognizes the
// refusal here and renders it as the operator-facing message.
func deniedNodeType(err error) (string, bool) {
	var denied *nodered.DeniedNodeError
	if !errors.As(err, &denied) {
		return "", false
	}
	return denied.NodeType, true
}

// toolDeniedNode converts a client-side policy refusal into the tool error
// the caller should return. It reports false when err is not a refusal, so
// the caller can fall through to its usual error handling.
func toolDeniedNode(err error) (*mcp.CallToolResult, bool) {
	nodeType, denied := deniedNodeType(err)
	if !denied {
		return nil, false
	}
	return mcp.NewToolResultError(deniedNodeMessage(nodeType)), true
}
