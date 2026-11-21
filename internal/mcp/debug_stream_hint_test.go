package mcp

import (
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/config"
	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

var debugStreamHintValue = regexp.MustCompile("MCP_DEBUG_STREAM=([A-Za-z0-9]+)")

// Issue #402: every hint that tells the operator how to enable the debug
// stream must name a value config.Load accepts and reads as "on". The hints
// said `MCP_DEBUG_STREAM=on`, which strconv.ParseBool rejects, so following
// any of them produced a server that did not start.
func TestDebugStreamHintsNameAValueConfigAccepts(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	s := newTestServerDebugStream(t, false, false)
	slog.SetDefault(prev)

	toolText := func(res *mcp.CallToolResult) string {
		t.Helper()
		if res == nil || len(res.Content) == 0 {
			t.Fatal("expected a result with an error message")
		}
		return res.Content[0].(mcp.TextContent).Text
	}
	debugRes, _ := s.handleGetDebugMessages(context.Background(), mcp.CallToolRequest{})
	statusRes, _ := s.handleGetNodeStatus(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"node_id": "n1"}},
	})
	_, remedy := capabilityGuidance("get_debug_messages", CapStreamDisabled, RuntimeProbe{})
	var statusDesc string
	for _, tool := range s.tools {
		if tool.Name == "get_node_status" {
			statusDesc = tool.Description
		}
	}

	hints := map[string]string{
		"startup warning":            logs.String(),
		"get_debug_messages error":   toolText(debugRes),
		"get_node_status error":      toolText(statusRes),
		"capability remedy":          remedy,
		"get_node_status tool desc":  statusDesc,
		"ErrStatusStreamUnavailable": nodered.ErrStatusStreamUnavailable.Error(),
	}
	for name, hint := range hints {
		matches := debugStreamHintValue.FindAllStringSubmatch(hint, -1)
		if len(matches) == 0 {
			t.Errorf("%s should name MCP_DEBUG_STREAM=<value>, got %q", name, hint)
			continue
		}
		for _, m := range matches {
			if m[1] != "true" {
				t.Errorf("%s recommends MCP_DEBUG_STREAM=%s, want MCP_DEBUG_STREAM=true: %q", name, m[1], hint)
			}
			t.Setenv("NODERED_URL", "http://localhost:1880")
			t.Setenv("MCP_DEBUG_STREAM", m[1])
			cfg, err := config.Load()
			if err != nil {
				t.Errorf("%s recommends MCP_DEBUG_STREAM=%s, which config rejects: %v", name, m[1], err)
				continue
			}
			if !cfg.MCPDebugStream {
				t.Errorf("%s recommends MCP_DEBUG_STREAM=%s, which config reads as off", name, m[1])
			}
		}
	}
}
