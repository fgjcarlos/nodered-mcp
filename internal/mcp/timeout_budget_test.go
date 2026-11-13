package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// TestIssue339ToolBudgets is the headline regression for issue #339: the
// palette and runtime-state tools must get the budget their own client call
// asks for, not the flat 15-second default every tool used to get.
//
// The wrapper's deadline is the *parent* of the client's, and a child
// context cannot extend its parent's deadline: with a 15-second parent the
// client's own 5-minute (palette) and 2-minute (runtime state) budgets never
// took effect and every npm install was cut short.
//
// The numbers are asserted as ranges, not as the constants, so the test
// states the contract (which budget each category gets) instead of
// restating a constant.
func TestIssue339ToolBudgets(t *testing.T) {
	cases := []struct {
		tool string
		min  time.Duration
		max  time.Duration
	}{
		// install_node / uninstall_node: an npm install runs under the hood.
		// The wrapper grants the client's budget plus the headroom, so the
		// range starts a little above the client's own figure.
		{"install_node", 4*time.Minute + toolBudgetHeadroom, 6*time.Minute + toolBudgetHeadroom},
		{"uninstall_node", 4*time.Minute + toolBudgetHeadroom, 6*time.Minute + toolBudgetHeadroom},
		// set_flows / set_flows_state / restore_backup: a full deploy, a
		// runtime restart, or a restore on a busy instance.
		{"set_flows", 90*time.Second + toolBudgetHeadroom, 150*time.Second + toolBudgetHeadroom},
		{"set_flows_state", 90*time.Second + toolBudgetHeadroom, 150*time.Second + toolBudgetHeadroom},
		{"restore_backup", 90*time.Second + toolBudgetHeadroom, 150*time.Second + toolBudgetHeadroom},
		// Everything else keeps the 15-second default.
		{"create_flow", 10 * time.Second, 20 * time.Second},
	}
	s := &Server{}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			var deadline time.Time
			handler := func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				deadline, _ = ctx.Deadline()
				return mcp.NewToolResultText("ok"), nil
			}
			wrapped := s.withTimeoutAttempts(tc.tool, handler, 1)
			if _, err := wrapped(context.Background(), mcp.CallToolRequest{}); err != nil {
				t.Fatalf("wrapped call: %v", err)
			}
			if deadline.IsZero() {
				t.Fatal("handler got no deadline")
			}
			got := time.Until(deadline)
			if got < tc.min || got > tc.max {
				t.Errorf("tool %q got a %s budget, want between %s and %s", tc.tool, got, tc.min, tc.max)
			}
		})
	}
}

// TestIssue339WrapperBudgetExceedsTheClientBudget pins the relationship the
// R3 reliability review asked for: the wrapper's deadline is the parent of the
// client's, so it has to be *later* than the client's own budget.
//
// With the two equal, the parent's fires first and the client's budget never
// binds the runtime work: the client would ask for five minutes and get the
// wrapper's five minutes counted from before the call was even queued. The
// headroom is what lets the client's budget be the binding one.
func TestIssue339WrapperBudgetExceedsTheClientBudget(t *testing.T) {
	cases := []struct {
		tool   string
		client time.Duration
	}{
		{"install_node", nodered.InstallTimeout},
		{"uninstall_node", nodered.InstallTimeout},
		{"set_flows", nodered.SettingsStateTimeout},
		{"set_flows_state", nodered.SettingsStateTimeout},
		{"restore_backup", nodered.SettingsStateTimeout},
	}
	s := &Server{}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			var deadline time.Time
			handler := func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				deadline, _ = ctx.Deadline()
				return mcp.NewToolResultText("ok"), nil
			}
			wrapped := s.withTimeoutAttempts(tc.tool, handler, 1)
			if _, err := wrapped(context.Background(), mcp.CallToolRequest{}); err != nil {
				t.Fatalf("wrapped call: %v", err)
			}
			got := time.Until(deadline)
			if got <= tc.client {
				t.Errorf("tool %q got a %s budget, want more than the client's %s so the client's budget binds the work", tc.tool, got, tc.client)
			}
		})
	}
}
