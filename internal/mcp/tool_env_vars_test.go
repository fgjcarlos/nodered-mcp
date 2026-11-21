package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// envVarName matches the operator-facing knobs, the same pattern
// internal/config/docs_sync_test.go scans config.go with.
var envVarName = regexp.MustCompile(`\b(?:MCP|NODERED)_[A-Z_]+\b`)

// TestToolDescriptionsNameOnlyRealEnvVars keeps the model from being
// told about a knob that does not exist: search_nodes advertised
// NODERED_SEARCH_BASE_URL, which nothing read (#403). Every env name in
// a tool (description or parameter schema) must be read by config.go.
func TestToolDescriptionsNameOnlyRealEnvVars(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "internal", "config", "config.go"))
	if err != nil {
		t.Fatalf("read config.go: %v", err)
	}
	known := map[string]bool{}
	for _, name := range envVarName.FindAllString(string(src), -1) {
		known[name] = true
	}

	for _, tool := range newTestServer(t, false).tools {
		raw, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("marshal %s: %v", tool.Name, err)
		}
		for _, name := range envVarName.FindAllString(string(raw), -1) {
			if !known[name] {
				t.Errorf("%s names %s, which config.go does not read", tool.Name, name)
			}
		}
		if tool.Name == "search_nodes" && !strings.Contains(tool.Description, "registry.npmjs.org") {
			t.Errorf("search_nodes description must name registry.npmjs.org: %s", tool.Description)
		}
	}
}
