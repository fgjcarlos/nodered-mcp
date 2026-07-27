package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// Tests in this file only cover the prompt handlers. The tool handlers
// delegate straight to nodered.Client, which is fully covered by
// internal/nodered/*_test.go; spinning up a full mcp-go server just to
// re-verify those would test the SDK, not us.

func TestHandleExplainFlowPrompt(t *testing.T) {
	srv := &Server{}
	res, err := srv.handleExplainFlowPrompt(context.Background(), mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{Arguments: map[string]string{"flow_id": "tab1"}},
	})
	if err != nil {
		t.Fatalf("handleExplainFlowPrompt: %v", err)
	}
	if res.Description == "" {
		t.Error("expected non-empty description")
	}
	if len(res.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(res.Messages))
	}
	text, ok := res.Messages[0].Content.(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Messages[0].Content)
	}
	if !strings.Contains(text.Text, `id "tab1"`) {
		t.Errorf("prompt body should reference the flow id, got: %s", text.Text)
	}
}

func TestHandleGenerateFlowPrompt(t *testing.T) {
	srv := &Server{}
	res, err := srv.handleGenerateFlowPrompt(context.Background(), mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{Arguments: map[string]string{"description": "log every temperature reading to a file"}},
	})
	if err != nil {
		t.Fatalf("handleGenerateFlowPrompt: %v", err)
	}
	if res.Description == "" {
		t.Error("expected non-empty description")
	}
	if len(res.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(res.Messages))
	}
	text, ok := res.Messages[0].Content.(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Messages[0].Content)
	}
	for _, want := range []string{"temperature reading", "create_flow", "search_nodes"} {
		if !strings.Contains(text.Text, want) {
			t.Errorf("prompt body should mention %q, got: %s", want, text.Text)
		}
	}
}
