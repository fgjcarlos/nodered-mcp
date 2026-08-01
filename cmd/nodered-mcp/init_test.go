package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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

func TestInit_WritesPlaceholderUnderWrite(t *testing.T) {
	dir := t.TempDir()
	var path string
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", dir)
		path = filepath.Join(dir, "Claude", "claude_desktop_config.json")
	case "darwin":
		t.Setenv("HOME", dir)
		path = filepath.Join(dir, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "plan9":
		t.Setenv("home", dir)
		path = filepath.Join(dir, "lib", "Claude", "claude_desktop_config.json")
	default:
		t.Setenv("XDG_CONFIG_HOME", dir)
		path = filepath.Join(dir, "Claude", "claude_desktop_config.json")
	}

	const literalToken = "supersecret-test-token-12345"
	env := buildEnv("http://localhost:1880", literalToken, "backups")
	client := mcpClient{key: "claude-desktop", name: "Claude Desktop"}
	if err := writeClientConfig(client, "/bin/nodered-mcp", env, "http://localhost:1880", literalToken, "backups"); err != nil {
		t.Fatalf("writeClientConfig: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	if !strings.Contains(string(data), `"NODERED_TOKEN": "<NODERED_TOKEN>"`) {
		t.Errorf("written config must contain the token placeholder:\n%s", data)
	}
	if strings.Contains(string(data), literalToken) {
		t.Errorf("written config leaked the literal token:\n%s", data)
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
	const literalToken = "supersecret-test-token-12345"
	out := renderConfig("vscode", "/bin/nodered-mcp", "http://localhost:1880", literalToken, "backups")
	if !strings.Contains(out, `"servers"`) {
		t.Errorf("VS Code snippet must use the 'servers' root key:\n%s", out)
	}
	if strings.Contains(out, `"mcpServers"`) {
		t.Errorf("VS Code snippet must not use 'mcpServers':\n%s", out)
	}
	// A Node-RED admin token can authorize dangerous deployments, so rendered output must only contain a placeholder.
	if !strings.Contains(out, `"NODERED_TOKEN": "<NODERED_TOKEN>"`) {
		t.Errorf("non-empty token must render as a placeholder:\n%s", out)
	}
	if strings.Contains(out, literalToken) {
		t.Errorf("rendered config leaked the literal token:\n%s", out)
	}
}

func TestInit_RendersPlaceholderForClaudeCode(t *testing.T) {
	const literalToken = "supersecret-test-token-12345"
	out := renderConfig("claude-code", "/bin/nodered-mcp", "http://localhost:1880", literalToken, "custom-backups")
	if !strings.HasPrefix(out, "claude mcp add nodered") {
		t.Errorf("expected a `claude mcp add` command, got:\n%s", out)
	}
	if !strings.Contains(out, "-e NODERED_URL=http://localhost:1880") {
		t.Errorf("URL flag missing:\n%s", out)
	}
	if !strings.Contains(out, "-e NODERED_TOKEN="+tokenPlaceholder) {
		t.Errorf("token flag must contain the placeholder:\n%s", out)
	}
	if strings.Contains(out, literalToken) {
		t.Errorf("rendered command leaked the literal token:\n%s", out)
	}
	if !strings.Contains(out, "-e NODERED_BACKUP_DIR=custom-backups") {
		t.Errorf("custom backup dir should be included:\n%s", out)
	}
	if !strings.HasSuffix(out, "-- /bin/nodered-mcp") {
		t.Errorf("command must end with the binary path:\n%s", out)
	}
}

// TestMergeServerIntoFile_BackupFailureFailsMerge covers the first
// half of the issue #70 atomic-write story: a successful merge MUST
// only happen when the .bak snapshot of the existing config is on
// disk. We simulate a backup failure by making the parent directory
// read-only: on POSIX, that prevents creation of new entries, so the
// atomic write to path+".bak" fails.
//
// Skipped on Windows where POSIX mode bits are not honoured the same
// way (a `chmod 0o500` directory still accepts new file creations
// under the standard ACLs the Windows runner uses), so this trap
// does not reproduce the failure mode there.
func TestMergeServerIntoFile_BackupFailureFailsMerge(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows POSIX mode bits differ; the read-only-dir trap does not reproduce the failure mode there")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "claude_desktop_config.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil { // owner: r-x, no write
		t.Fatal(err)
	}
	// Restore perms so TempDir cleanup works even on test failure.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	env := map[string]string{"NODERED_URL": "http://localhost:1880"}
	err := mergeServerIntoFile(path, "mcpServers", "/bin/nodered-mcp", env)
	if err == nil {
		t.Fatal("expected merge to fail when .bak cannot be created, got nil")
	}
	// The original config must still be intact: the failure happened
	// before the atomic write of the new file.
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("original config unreadable after failed merge: %v", readErr)
	}
	if !strings.Contains(string(data), "mcpServers") {
		t.Errorf("original config was modified despite failure: %s", data)
	}
}

// TestMergeServerIntoFile_NoStaleTempLeftBehind covers the second
// half of the atomic-write story: on success, no .tmp-* file should
// remain in the destination directory. On failure (we don't simulate
// one here), the helper removes its own staged temp; the happy path
// does too because the rename consumes it.
func TestMergeServerIntoFile_NoStaleTempLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude_desktop_config.json")
	env := map[string]string{"NODERED_URL": "http://localhost:1880"}
	if err := mergeServerIntoFile(path, "mcpServers", "/bin/nodered-mcp", env); err != nil {
		t.Fatalf("mergeServerIntoFile: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), filepath.Base(path)+".tmp-") {
			t.Errorf("stale temp file left behind after successful merge: %s", e.Name())
		}
	}
}

// TestMergeServerIntoFile_FileModeOwnerOnly covers the third invariant
// of #70: the resulting config file is owner-readable and not
// group/world readable. Skipped on Windows where mode bits are not
// honoured the same way.
func TestMergeServerIntoFile_FileModeOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows mode bits differ; permission tests run on Unix only")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "claude_desktop_config.json")
	env := map[string]string{"NODERED_URL": "http://localhost:1880"}
	if err := mergeServerIntoFile(path, "mcpServers", "/bin/nodered-mcp", env); err != nil {
		t.Fatalf("mergeServerIntoFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	// Owner must be able to read and write; group and world must
	// have no permissions.
	if perm&0o700 != 0o600 {
		t.Errorf("expected owner-only 0o600, got %04o", perm)
	}
	if perm&0o077 != 0 {
		t.Errorf("expected no group/world bits, got %04o", perm)
	}
}
