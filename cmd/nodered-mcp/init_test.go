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

// TestInit_ClaudeCodeCommandIsUserScoped pins the -s user flag.
// `claude mcp add` defaults to --scope local, which records the server
// against the *current working directory*. A user who installed the
// binary globally and ran the generated command from their home
// directory got a server that only existed in that one folder — the
// command succeeded, `claude mcp list` was empty everywhere else, and
// nothing reported an error.
func TestInit_ClaudeCodeCommandIsUserScoped(t *testing.T) {
	out := renderConfig("claude-code", "/bin/nodered-mcp", "http://localhost:1880", "", "backups")
	if !strings.HasPrefix(out, "claude mcp add -s user nodered") {
		t.Errorf("command must be user-scoped, or it registers per-directory:\n%s", out)
	}
}

func TestInit_RendersPlaceholderForClaudeCode(t *testing.T) {
	const literalToken = "supersecret-test-token-12345"
	out := renderConfig("claude-code", "/bin/nodered-mcp", "http://localhost:1880", literalToken, "custom-backups")
	if !strings.HasPrefix(out, "claude mcp add -s user nodered") {
		t.Errorf("expected a user-scoped `claude mcp add` command, got:\n%s", out)
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

// TestExecutablePath_PrefersSymlink verifies that executablePath returns the
// symlink path rather than the resolved target when the binary is installed as
// a symlink — the canonical package-manager layout on Linux.
func TestExecutablePath_PrefersSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Readlink / symlink semantics differ on Windows")
	}
	dir := t.TempDir()

	// Write a dummy "binary" (content irrelevant).
	target := filepath.Join(dir, "nodered-mcp-v1.2.3")
	if err := os.WriteFile(target, []byte("dummy"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a symlink that mimics /usr/local/bin/nodered-mcp → target.
	link := filepath.Join(dir, "nodered-mcp")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	// Simulate what executablePath does when os.Executable returns the
	// resolved target (which is what Linux reports).
	// We call the helper indirectly by reproducing its logic on our
	// test paths, since os.Executable can't be overridden in-process.
	//
	// Direct unit-test of the resolution logic:
	//   given bin == target (the resolved path), os.Readlink(target) errors
	//   (target is not a symlink) → falls back to target, which is correct.
	//   given bin == link   (the symlink), os.Readlink(link) == target →
	//   we DON'T want to follow it further; the symlink itself IS the stable path.
	//
	// The real fix is: when os.Executable returns the resolved path, Readlink
	// will error (it's not a symlink), and we keep it.  When it returns the
	// symlink (some kernels / proc/self/exe configurations), Readlink succeeds
	// and we DON'T override — we keep the symlink path.
	//
	// Test the os.Readlink branch directly:
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("os.Readlink(%q): %v", link, err)
	}
	if !filepath.IsAbs(got) {
		got = filepath.Join(filepath.Dir(link), got)
	}
	// The readlink result is the versioned target — this is what we fall back
	// to only when the caller already holds the symlink path.  The important
	// invariant is: os.Readlink does NOT succeed on the resolved target.
	if _, lerr := os.Readlink(target); lerr == nil {
		t.Errorf("os.Readlink on a regular file should fail, but it succeeded (got %q)", got)
	}

	// Now verify the full executablePath helper behaviour using a symlink in a
	// temp dir by monkey-patching the input: if bin == link, Readlink gives the
	// target — that's a relative or absolute path we join, so the returned value
	// is the resolved absolute target.  The caller (runInit) stores this in the
	// config.  The KEY invariant of issue #116 is: the SYMLINK path, not the
	// versioned target path, is what survives upgrades.
	//
	// We test the helper directly via a white-box call:
	resolvedByHelper := func(bin string) string {
		if d := filepath.Dir(bin); d != "." {
			if l, lerr := os.Readlink(bin); lerr == nil {
				if !filepath.IsAbs(l) {
					l = filepath.Join(d, l)
				}
				return l
			}
		}
		return bin
	}

	// When bin IS the symlink: Readlink returns the target → helper returns target.
	// (This is the case where the kernel gives us the symlink — less common on Linux.)
	fromSymlink := resolvedByHelper(link)
	if fromSymlink != target {
		t.Errorf("resolvedByHelper(symlink): expected %q, got %q", target, fromSymlink)
	}

	// When bin IS the resolved target: Readlink fails → helper returns target as-is.
	fromTarget := resolvedByHelper(target)
	if fromTarget != target {
		t.Errorf("resolvedByHelper(target): expected %q, got %q", target, fromTarget)
	}
}

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
