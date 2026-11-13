package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
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
		{"install_node", 4 * time.Minute, 6 * time.Minute},
		{"uninstall_node", 4 * time.Minute, 6 * time.Minute},
		// set_flows / set_flows_state / restore_backup: a full deploy, a
		// runtime restart, or a restore on a busy instance.
		{"set_flows", 90 * time.Second, 150 * time.Second},
		{"set_flows_state", 90 * time.Second, 150 * time.Second},
		{"restore_backup", 90 * time.Second, 150 * time.Second},
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
