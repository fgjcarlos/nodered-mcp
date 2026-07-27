package mcp

import (
	"testing"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// readOnlyTools is the exact set a server started with readOnly=true must
// expose. Anything that writes config, mutates the palette, or fires a node
// into a live runtime is deliberately absent — inject_node included: firing an
// inject can send a real command to a real device.
var readOnlyTools = []string{
	"list_flows",
	"search_flows",
	"get_diagnostics",
	"list_plugins",
	"get_context",
	"get_debug_messages",
	"get_flow",
	"list_nodes",
	"get_node_info",
	"search_nodes",
	"get_settings",
	"get_flows_state",
	"list_backups",
	"diff_flows",
}

// totalTools is the full-mode count: every read tool plus every mutating one.
const totalTools = 29

func newTestServer(t *testing.T, readOnly bool) *Server {
	t.Helper()
	c, err := nodered.NewClient(nodered.Options{BaseURL: "http://localhost:1880"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return New(c, "test", readOnly)
}

func toolNames(s *Server) map[string]bool {
	out := make(map[string]bool, len(s.tools))
	for _, tool := range s.tools {
		out[tool.Name] = true
	}
	return out
}

func TestReadOnlyExposesOnlyReadTools(t *testing.T) {
	got := toolNames(newTestServer(t, true))

	for _, want := range readOnlyTools {
		if !got[want] {
			t.Errorf("read-only server is missing read tool %q", want)
		}
	}
	if len(got) != len(readOnlyTools) {
		t.Errorf("read-only server exposed %d tools, want %d: %v", len(got), len(readOnlyTools), got)
	}
}

func TestReadOnlyWithholdsEveryMutatingTool(t *testing.T) {
	got := toolNames(newTestServer(t, true))

	// Explicit list rather than "everything not in readOnlyTools": if a new
	// mutating tool is added and forgotten, that derived form would pass while
	// this one keeps failing until the tool is classified on purpose.
	for _, banned := range []string{
		"create_flow", "update_flow", "delete_flow", "set_flows",
		"inject_node", "install_node", "uninstall_node",
		"enable_node", "disable_node", "set_flows_state", "restore_backup",
	} {
		if got[banned] {
			t.Errorf("read-only server exposed mutating tool %q", banned)
		}
	}
}

func TestFullServerExposesEveryTool(t *testing.T) {
	got := toolNames(newTestServer(t, false))

	if len(got) != totalTools {
		t.Errorf("full server exposed %d tools, want %d: %v", len(got), totalTools, got)
	}
	for _, want := range readOnlyTools {
		if !got[want] {
			t.Errorf("full server is missing read tool %q", want)
		}
	}
	for _, want := range []string{"create_flow", "set_flows", "restore_backup", "inject_node"} {
		if !got[want] {
			t.Errorf("full server is missing mutating tool %q", want)
		}
	}
}

// Registration state must live on the Server, not on the package. Two servers
// in one process (a test binary, or an http host serving both modes) would
// otherwise accumulate into the same slice and report each other's tools.
func TestServersDoNotShareRegistrationState(t *testing.T) {
	full := newTestServer(t, false)
	readOnly := newTestServer(t, true)

	if len(full.tools) != totalTools {
		t.Errorf("first server has %d tools after a second was built, want %d", len(full.tools), totalTools)
	}
	if len(readOnly.tools) != len(readOnlyTools) {
		t.Errorf("second server has %d tools, want %d", len(readOnly.tools), len(readOnlyTools))
	}
	if len(full.resources) != len(readOnly.resources) {
		t.Errorf("resource counts diverged: %d vs %d", len(full.resources), len(readOnly.resources))
	}
}

// Resources and prompts are read-only surfaces, so read-only mode keeps them.
func TestReadOnlyKeepsResourcesAndPrompts(t *testing.T) {
	s := newTestServer(t, true)

	if len(s.resources) != 3 {
		t.Errorf("read-only server exposed %d resources, want 3", len(s.resources))
	}
	if len(s.prompts) != 2 {
		t.Errorf("read-only server exposed %d prompts, want 2", len(s.prompts))
	}
}
