package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeServerIntoFile_PreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude_desktop_config.json")
	original := `{
  "mcpServers": { "other": { "command": "keep-me" } },
  "someUnrelatedPref": true
}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{"NODERED_URL": "http://localhost:7880"}
	if err := mergeServerIntoFile(path, "mcpServers", "/bin/nodered-mcp", env); err != nil {
		t.Fatalf("mergeServerIntoFile: %v", err)
	}

	var doc struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
		SomeUnrelatedPref bool `json:"someUnrelatedPref"`
	}
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if _, ok := doc.MCPServers["other"]; !ok {
		t.Error("existing 'other' server was dropped")
	}
	if !doc.SomeUnrelatedPref {
		t.Error("unrelated preference was dropped")
	}
	if doc.MCPServers["nodered"].Command != "/bin/nodered-mcp" {
		t.Errorf("nodered command not written, got %q", doc.MCPServers["nodered"].Command)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Error("expected a .bak backup of the original file")
	}
}

func TestMergeServerIntoFile_RefusesInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mergeServerIntoFile(path, "mcpServers", "/bin/x", map[string]string{}); err == nil {
		t.Fatal("expected refusal to overwrite invalid JSON, got nil")
	}
}

func TestMergeServerIntoFile_CreatesMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "mcp.json") // parent dir missing on purpose
	env := map[string]string{"NODERED_URL": "http://localhost:1880"}
	if err := mergeServerIntoFile(path, "mcpServers", "/bin/nodered-mcp", env); err != nil {
		t.Fatalf("mergeServerIntoFile on missing file: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file was not created: %v", err)
	}
}

func TestRenderConfig_ClaudeDesktop(t *testing.T) {
	out := renderConfig("claude-desktop", "/path/to/nodered-mcp", "http://localhost:7880", "", "backups")

	var doc struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	srv, ok := doc.MCPServers["nodered"]
	if !ok {
		t.Fatal("missing 'nodered' server entry")
	}
	if srv.Command != "/path/to/nodered-mcp" {
		t.Errorf("command not set to binary path, got %q", srv.Command)
	}
	if srv.Env["NODERED_URL"] != "http://localhost:7880" {
		t.Errorf("URL not carried into env, got %q", srv.Env["NODERED_URL"])
	}
	if _, present := srv.Env["NODERED_TOKEN"]; present {
		t.Error("empty token should be omitted from env")
	}
	if _, present := srv.Env["NODERED_BACKUP_DIR"]; present {
		t.Error("default backup dir should be omitted from env")
	}
}

func TestRenderConfig_VSCodeUsesServersKey(t *testing.T) {
	out := renderConfig("vscode", "/bin/nodered-mcp", "http://localhost:1880", "tok", "backups")
	if !strings.Contains(out, `"servers"`) {
		t.Errorf("VS Code snippet must use the 'servers' root key:\n%s", out)
	}
	if strings.Contains(out, `"mcpServers"`) {
		t.Errorf("VS Code snippet must not use 'mcpServers':\n%s", out)
	}
	if !strings.Contains(out, `"NODERED_TOKEN": "tok"`) {
		t.Errorf("non-empty token must appear in env:\n%s", out)
	}
}

func TestRenderConfig_ClaudeCodeIsCommand(t *testing.T) {
	out := renderConfig("claude-code", "/bin/nodered-mcp", "http://localhost:1880", "", "custom-backups")
	if !strings.HasPrefix(out, "claude mcp add nodered") {
		t.Errorf("expected a `claude mcp add` command, got:\n%s", out)
	}
	if !strings.Contains(out, "-e NODERED_URL=http://localhost:1880") {
		t.Errorf("URL flag missing:\n%s", out)
	}
	if !strings.Contains(out, "-e NODERED_BACKUP_DIR=custom-backups") {
		t.Errorf("custom backup dir should be included:\n%s", out)
	}
	if !strings.HasSuffix(out, "-- /bin/nodered-mcp") {
		t.Errorf("command must end with the binary path:\n%s", out)
	}
}
