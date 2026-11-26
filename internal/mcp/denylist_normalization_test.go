package mcp

import "testing"

func TestBuildDenylistNormalizesTypes(t *testing.T) {
	denied := buildDenylist([]string{"exec"})
	for _, nodeType := range []string{"exec", "Exec", "exec "} {
		if !denied(nodeType) {
			t.Errorf("expected %q to be denied", nodeType)
		}
	}
	if denied("executable") {
		t.Error("denied an unrelated type")
	}
}
