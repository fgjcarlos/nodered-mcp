package mcp

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// Rendering of the client-side guards.
//
// Two guards stop a write before the runtime is contacted, and both need
// to say so: the operator has to know that nothing was deployed, and the
// model has to know that the fix is in the payload it is about to send
// and not in a runtime that needs restarting.
//
//   - the effective-configuration guard (issue #334), which judges the
//     whole configuration a full deployment would install and reports
//     every problem it finds;
//   - the per-document guard, the one the tab write path has always
//     applied, which the subflow write path now shares.

// configValidationResult renders a refusal by the effective-configuration
// guard: the problems, one per line, and what to do about them.
//
// The issues are rendered with their kind, so the caller can tell a
// dangling wire from a node that belongs to no tab, and the list reads
// the same way the validate_flow tool renders it.
func configValidationResult(err error, advice string) (*mcp.CallToolResult, bool) {
	var invalid *nodered.ConfigValidationError
	if !errors.As(err, &invalid) {
		return nil, false
	}
	issues := invalid.Issues
	lines := make([]string, 0, len(issues))
	for _, issue := range issues {
		lines = append(lines, fmt.Sprintf("- [%s] %s", issue.Kind, issue.Message))
	}
	return mcp.NewToolResultError(fmt.Sprintf(
		"the configuration was not deployed: %d structural problem(s)\n\n%s\n\n%s",
		len(issues), strings.Join(lines, "\n"), advice,
	)), true
}

// flowGuardResult renders a refusal by the per-document guard: the same
// rules the tab write path applies, now also applied to a subflow
// definition.
func flowGuardResult(err error) (*mcp.CallToolResult, bool) {
	var guard *nodered.FlowGuardError
	if !errors.As(err, &guard) {
		return nil, false
	}
	return mcp.NewToolResultError(
		"the write was refused before the runtime was contacted: " + guard.Message,
	), true
}
