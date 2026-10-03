package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// handleListBackups lists saved flow snapshots.
func (s *Server) handleListBackups(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	slog.Debug("tool: list_backups")

	backups, err := s.nrClient.ListBackups()
	if err != nil {
		slog.Error("list_backups failed", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("reading backups: %v", err)), nil
	}
	if len(backups) == 0 {
		return mcp.NewToolResultText("No backups saved yet."), nil
	}
	out, err := json.MarshalIndent(backups, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding backups: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("```json\n%s\n```", string(out))), nil
}

// handleDiffFlows compares two flow configurations.

// handleDiffFlows compares two flow configurations.
func (s *Server) handleDiffFlows(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	from, err := req.RequireString("from")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	to := req.GetString("to", "current")
	slog.Debug("tool: diff_flows", "from", from, "to", to)

	before, err := s.resolveFlowSource(ctx, from)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("reading %q: %v", from, err)), nil
	}
	after, err := s.resolveFlowSource(ctx, to)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("reading %q: %v", to, err)), nil
	}

	diff := nodered.DiffFlows(before, after)
	if diff.Empty() {
		return mcp.NewToolResultText(fmt.Sprintf("%q and %q are identical.", from, to)), nil
	}
	out, err := json.MarshalIndent(diff, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding diff: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf(
		"%d difference(s) between %q and %q: %d added, %d removed, %d changed.\n\n```json\n%s\n```",
		diff.Total(), from, to, len(diff.Added), len(diff.Removed), len(diff.Changed), string(out),
	)), nil
}

// resolveFlowSource turns a diff operand into a flow configuration. "current"
// reads the live instance; anything else names a backup, with "latest" meaning
// the most recent one.

// resolveFlowSource turns a diff operand into a flow configuration. "current"
// reads the live instance; anything else names a backup, with "latest" meaning
// the most recent one.
func (s *Server) resolveFlowSource(ctx context.Context, name string) (nodered.RawFlow, error) {
	if name == "current" {
		return s.nrClient.ListFlows(ctx)
	}
	if name == "latest" {
		backups, err := s.nrClient.ListBackups()
		if err != nil {
			return nil, err
		}
		if len(backups) == 0 {
			return nil, errors.New("no backups have been saved yet")
		}
		name = backups[0].Name
	}
	return s.nrClient.ReadBackup(name)
}

// handleRestoreBackup restores the full flow config from a saved backup.

// handleRestoreBackup restores the full flow config from a saved backup.
func (s *Server) handleRestoreBackup(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("backup")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// "latest" resolves to the most recent backup.
	if name == "latest" {
		backups, err := s.nrClient.ListBackups()
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("reading backups: %v", err)), nil
		}
		if len(backups) == 0 {
			return mcp.NewToolResultError("no backups to restore"), nil
		}
		name = backups[0].Name
	}
	slog.Debug("tool: restore_backup", "backup", name)

	// Issue #310: the restore's HTTP work and the helper-pointer
	// invalidation must run under the same lock acquisition that
	// handleSetContext holds across its whole critical section.
	// A restore that took the lock only around the pointer clear
	// would let POST /flows race with an in-flight set_context:
	// the deploy would land while a caller was still using the
	// pre-restore helper.
	//
	// The lock is taken BEFORE the HTTP work and held through the
	// pointer clear so both routes (this one and
	// handleSetContext) obey the same ctxHelperMu -> writeMu
	// ordering inside the nodered.Client. nodered.Client.writeMu
	// does not need to guard the inject — ctxHelperMu held across
	// the inject dispatch in handleSetContext is sufficient.
	//
	// On a failed restore we do NOT clear the helper: the runtime
	// is unchanged, so the existing helper still points at valid
	// runtime state. Clearing it on failure would force a
	// re-provision (4 NR round-trips) for no benefit.
	content, err := s.nrClient.ReadBackup(name)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("reading backup: %v", err)), nil
	}

	// Issue #311: a restore is a full deployment, so it carries the
	// same MCP_NODE_DENYLIST obligation as every other write tool. A
	// backup saved before the policy changed can hold a type that is
	// denied now, and restoring it would reintroduce the exact RCE
	// path #81 closed. Reject here, before the snapshot and before the
	// POST, and name the type so the operator can act.
	//
	// nodered.FlowArray is the same extractor RestoreFlows deploys
	// with, so a shape the client accepts cannot slip past this check.
	// A nil array means the payload is not a flow document at all; the
	// client rejects those with its own error, so we leave that message
	// untouched rather than shadowing it.
	if flows := nodered.FlowArray(content); flows != nil {
		if denied, t := s.findDeniedNodeInFlowsArray(flows); denied {
			return mcp.NewToolResultError(fmt.Sprintf(
				"backup %q contains node type %q, which is in MCP_NODE_DENYLIST; "+
					"remove it from the denylist, or edit the backup before restoring (see SECURITY.md)",
				name, t,
			)), nil
		}
	}

	s.ctxHelperMu.Lock()
	defer s.ctxHelperMu.Unlock()
	if err := s.nrClient.RestoreFlows(ctx, content); err != nil {
		slog.Error("restore_backup failed", "error", err, "backup", name)
		return mcp.NewToolResultError(fmt.Sprintf("restoring: %v", err)), nil
	}
	s.ctxHelper = nil
	return mcp.NewToolResultText(fmt.Sprintf("Restored flow config from %q (current state was backed up first).", name)), nil
}

// prettyJSON indents raw JSON for display, falling back to the raw bytes if
// it doesn't parse.
