package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

func TestFilterLogsByTime_KeepAfterSince(t *testing.T) {
	// Three entries: before, equal, after. The "equal" entry must be
	// kept because the filter is non-strict (e.Before is false for
	// equal timestamps).
	cutoff := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	in := nodered.Logs{
		{Timestamp: cutoff.Add(-time.Hour)},
		{Timestamp: cutoff},
		{Timestamp: cutoff.Add(time.Minute)},
	}
	out := filterLogsByTime(in, cutoff)
	if len(out) != 2 {
		t.Fatalf("expected 2 entries after cutoff, got %d", len(out))
	}
}

func TestFilterLogsByTime_DropsZeroTimestamp(t *testing.T) {
	// Entries with a zero Timestamp are dropped — they cannot be
	// meaningfully compared against a wall-clock cutoff.
	cutoff := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	in := nodered.Logs{
		{Timestamp: time.Time{}, Level: "info", Message: "no-ts"},
		{Timestamp: cutoff.Add(time.Hour), Level: "info", Message: "ok"},
	}
	out := filterLogsByTime(in, cutoff)
	if len(out) != 1 || out[0].Message != "ok" {
		t.Errorf("expected only the timestamped entry, got %v", out)
	}
}

func TestStatusTailLastError_EmptyReturnsEmpty(t *testing.T) {
	if got := statusTailLastError(""); got != "" {
		t.Errorf("empty input should return empty string, got %q", got)
	}
}

func TestStatusTailLastError_NonEmptyReturnsParenthetical(t *testing.T) {
	got := statusTailLastError("boom")
	if !strings.HasPrefix(got, " (last error: ") || !strings.HasSuffix(got, ")") {
		t.Errorf("expected ' (last error: boom)', got %q", got)
	}
}

func TestRenderFlowStatus_ConnectedIncludesAllNodes(t *testing.T) {
	// All three nodes are in the snapshot — the report should list
	// each one and report the stream as connected.
	s := &Server{}
	snap := nodered.StatusSnapshot{
		Connected: true,
		Tracked:   3,
		Entries: []nodered.StatusEntry{
			{ID: "n1"},
			{ID: "n2"},
			{ID: "n3"},
		},
	}
	res, err := s.renderFlowStatus("tab1", []string{"n1", "n2", "n3"}, snap)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	body := tc.Text
	for _, id := range []string{"n1", "n2", "n3"} {
		if !strings.Contains(body, id) {
			t.Errorf("body should mention %q, got %q", id, body)
		}
	}
}

func TestRenderFlowStatus_UnknownNodesAreFlagged(t *testing.T) {
	// n_unknown is in the flow but not in the snapshot — handler
	// must render it as "unknown" rather than dropping it.
	s := &Server{}
	snap := nodered.StatusSnapshot{
		Connected: true,
		Tracked:   1,
		Entries:   []nodered.StatusEntry{{ID: "n1"}},
	}
	res, err := s.renderFlowStatus("tab1", []string{"n1", "n_unknown"}, snap)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "n_unknown") {
		t.Errorf("body should mention unknown node, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "unknown") {
		t.Errorf("body should flag unknown status, got %q", tc.Text)
	}
}

func TestRenderFlowStatus_DisconnectedAppendsLastError(t *testing.T) {
	// Disconnected stream + a recorded last error → the parenthetical
	// must surface that error in the body.
	s := &Server{}
	snap := nodered.StatusSnapshot{
		Connected: false,
		LastError: "ws handshake failed",
		Tracked:   0,
	}
	res, _ := s.renderFlowStatus("tab1", nil, snap)
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "disconnected") {
		t.Errorf("body should report disconnected, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "ws handshake failed") {
		t.Errorf("body should include last error, got %q", tc.Text)
	}
}
