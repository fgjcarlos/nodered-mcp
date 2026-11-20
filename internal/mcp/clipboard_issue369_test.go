package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// TestIssue369_RemintMessageCountsCollisionsAndReminted: when 1 of the
// copy's 3 node ids exists on the target, the message reports 1 collision
// and 3 reminted ids, not "3 node ids already existed".
func TestIssue369_RemintMessageCountsCollisionsAndReminted(t *testing.T) {
	_, s := newFakeRuntime(t, `[
	  {"id":"other","type":"tab","label":"Other"},
	  {"id":"c1","type":"comment","z":"other","x":1,"y":1,"wires":[]}
	]`)
	res := importClipboard(t, s, `[
	  {"id":"t1","type":"tab","label":"T"},
	  {"id":"c1","type":"comment","z":"t1","x":1,"y":1,"wires":[]},
	  {"id":"c2","type":"comment","z":"t1","x":2,"y":2,"wires":[]},
	  {"id":"c3","type":"comment","z":"t1","x":3,"y":3,"wires":[]}
	]`)
	if res.IsError {
		t.Fatalf("import refused: %s", resultFirstText(res))
	}
	want := ` 1 of the copy's 3 node ids already existed on this instance, so all 3 were reminted and their wires and references rewritten.`
	if got := resultFirstText(res); !strings.HasSuffix(got, want) {
		t.Errorf("message = %q, want suffix %q", got, want)
	}
}

// TestIssue369_LegacyMultiTabRefusedAsTabs: a nested clipboard with two
// tabs keeps the message earlier releases gave and reaches no POST.
func TestIssue369_LegacyMultiTabRefusedAsTabs(t *testing.T) {
	rt, s := newFakeRuntime(t, `[]`)
	res := importClipboard(t, s, `[{"id":"a","label":"A","nodes":[]},{"id":"b","label":"B","nodes":[]}]`)
	want := "clipboard has 2 tabs; import_flow only accepts a single tab — split the import by hand if you want them on separate tabs"
	if !res.IsError || resultFirstText(res) != want {
		t.Fatalf("got %q (error=%v), want %q", resultFirstText(res), res.IsError, want)
	}
	if len(rt.posted) != 0 {
		t.Error("a refused clipboard reached POST /flow")
	}
}

// TestIssue369_ExportReadErrorKeepsReleasedPrefix: a failed read keeps the
// "reading flow:" prefix v0.7.2 gave.
func TestIssue369_ExportReadErrorKeepsReleasedPrefix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	c, err := nodered.NewClient(nodered.Options{SkipVersionProbe: true, BaseURL: srv.URL, BackupDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	res, err := New(c, Options{Version: "test"}).handleExportFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"id": "tab1"}},
	})
	if err != nil || !res.IsError {
		t.Fatalf("want a tool error, got err=%v res=%v", err, res)
	}
	if got := resultFirstText(res); !strings.HasPrefix(got, "reading flow: ") {
		t.Errorf("message = %q, want prefix %q", got, "reading flow: ")
	}
}
