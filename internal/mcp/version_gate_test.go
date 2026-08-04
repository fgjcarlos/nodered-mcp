package mcp

import (
	"strings"
	"testing"
)

func TestMinVersionFor_KnownTools(t *testing.T) {
	cases := []struct {
		name     string
		tool     string
		wantMaj  int
		wantMin  int
		wantPat  int
		wantKnow bool
	}{
		{"get_diagnostics -> 3.1", "get_diagnostics", 3, 1, 0, true},
		{"inject_node -> 5.0", "inject_node", 5, 0, 0, true},
		{"set_context -> 5.0", "set_context", 5, 0, 0, true},
	}
	for _, c := range cases {
		v, ok := MinVersionForKnown(c.tool)
		if !ok {
			t.Errorf("%s: expected entry in map, got ok=false", c.name)
			continue
		}
		if !v.Known || v.Major != c.wantMaj || v.Minor != c.wantMin || v.Patch != c.wantPat {
			t.Errorf("%s: got %+v, want %d.%d.%d", c.name, v, c.wantMaj, c.wantMin, c.wantPat)
		}
	}
}

func TestMinVersionFor_UnknownTool(t *testing.T) {
	if v := MinVersionFor("list_flows"); v.Known {
		t.Errorf("list_flows should have no minimum, got %+v", v)
	}
	if _, ok := MinVersionForKnown("list_flows"); ok {
		t.Errorf("list_flows: ok should be false")
	}
}

func TestMinVersionAnnotation(t *testing.T) {
	if got := MinVersionAnnotation("get_diagnostics"); !strings.Contains(got, "3.1.0") {
		t.Errorf("annotation should contain 3.1.0, got %q", got)
	}
	if got := MinVersionAnnotation("list_flows"); got != "" {
		t.Errorf("annotation for tool with no minimum should be empty, got %q", got)
	}
}

// TestServerAttachMinVersion proves registerTools actually consults
// the table — a tool with a known minimum gets the annotation
// appended, one without does not.
func TestServerAttachMinVersion(t *testing.T) {
	s := newTestServer(t, false)
	// Walk the registered tool list and assert annotations land where
	// the table says they should.
	tools := s.tools
	if len(tools) == 0 {
		t.Skip("server has no tools registered; helper may have changed shape")
	}
	for _, tool := range tools {
		want := MinVersionAnnotation(tool.Name)
		// Either the annotation is present (last segment matches) or
		// the tool has no entry (annotation is empty and tool.Description
		// should not end with the NR marker).
		switch {
		case want == "":
			if strings.HasSuffix(tool.Description, "5.0.0)") || strings.HasSuffix(tool.Description, "3.1.0)") {
				t.Errorf("%s has no entry in the table but Description ends with a version annotation: %q", tool.Name, tool.Description)
			}
		default:
			if !strings.HasSuffix(tool.Description, want) {
				t.Errorf("%s should have annotation %q appended, description: %q", tool.Name, want, tool.Description)
			}
		}
	}
}
