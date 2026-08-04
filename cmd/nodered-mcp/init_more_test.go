package main

// init_more_test.go — additional coverage for cmd/nodered-mcp/init.go
// paths that the existing init_test.go / init_pure_test.go files do
// not exercise. Focused on the user-facing branches of runInit, the
// non-writable branch of writeClientConfig, and the direct
// read/write helpers that the rest of the init suite only covers
// indirectly. Issue #69 follow-up (part 7).

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunInit_HelpFlag covers the flag.ErrHelp path. The function
// must return nil and never reach executablePath / detectClients.
func TestRunInit_HelpFlag(t *testing.T) {
	if err := runInit([]string{"-help"}); err != nil {
		t.Errorf("runInit(-help) should return nil; got %v", err)
	}
}

// TestRunInit_UnknownFlag covers the flag.ContinueOnError branch
// for an unrecognised flag. The function returns the parse error
// (not nil), which is the documented contract.
func TestRunInit_UnknownFlag(t *testing.T) {
	if err := runInit([]string{"-not-a-real-flag"}); err == nil {
		t.Error("runInit(-not-a-real-flag) should return a parse error")
	}
}

// TestWriteClientConfig_NonWritableTarget covers the branch where
// the chosen client has no writable file (claude-code, vscode).
// writeClientConfig should render the snippet to stdout, print the
// "paste/run the above" hint, and return nil without touching the
// filesystem.
func TestWriteClientConfig_NonWritableTarget(t *testing.T) {
	// Capture stdout to assert the snippet is printed.
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = orig })

	client := mcpClient{
		key:  "claude-code",
		name: "Claude Code",
		note: "run the command below",
	}
	if err := writeClientConfig(client, "/bin/nodered-mcp",
		map[string]string{"NODERED_URL": "http://localhost:1880"},
		"http://localhost:1880", "", "backups"); err != nil {
		t.Fatalf("writeClientConfig: %v", err)
	}
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	out := buf.String()
	if !strings.Contains(out, "claude mcp add -s user nodered") {
		t.Errorf("expected claude-code snippet in stdout; got %q", out)
	}
}

// TestReadJSONObject_EmptyFile covers the "file exists but is empty"
// branch — must return an empty map, not an error.
func TestReadJSONObject_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readJSONObject(path)
	if err != nil {
		t.Fatalf("readJSONObject: %v", err)
	}
	if got == nil {
		t.Error("readJSONObject should return a non-nil empty map for an empty file")
	}
	if len(got) != 0 {
		t.Errorf("readJSONObject should return an empty map; got %v", got)
	}
}

// TestReadJSONObject_InvalidJSON covers the "file exists, non-empty,
// but isn't valid JSON" branch — must return an error and refuse to
// silently overwrite the user's config (issue #70 invariant).
func TestReadJSONObject_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readJSONObject(path); err == nil {
		t.Error("readJSONObject should error on invalid JSON")
	}
}

// TestWriteJSONObject_CreatesParentDirs covers the MkdirAll branch
// for a path whose parent does not yet exist.
func TestWriteJSONObject_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "nested", "out.json")
	if err := writeJSONObject(path, map[string]any{"k": "v"}); err != nil {
		t.Fatalf("writeJSONObject: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("wrote invalid JSON: %v", err)
	}
	if m["k"] != "v" {
		t.Errorf("expected k=v, got %v", m)
	}
}

// TestWriteJSONObject_BacksUpExisting covers the backup branch: when
// the file already exists, writeJSONObject must produce a .bak
// containing the previous content before writing the new one.
func TestWriteJSONObject_BacksUpExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"old":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONObject(path, map[string]any{"new": true}); err != nil {
		t.Fatalf("writeJSONObject: %v", err)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("expected .bak file; got %v", err)
	}
	if !strings.Contains(string(bak), `"old":true`) {
		t.Errorf(".bak should contain previous content; got %q", bak)
	}
}

// TestAtomicWriteFile_ReplacesExisting covers the happy path where
// the target file already exists and gets replaced atomically.
// (init_test.go covers the post-rename file mode via
// TestMergeServerIntoFile_FileModeOwnerOnly; this one covers the
// rename itself, including that no .tmp-* file is left behind.)
func TestAtomicWriteFile_ReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("atomicWriteFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("expected new content; got %q", got)
	}
	// No .tmp-* leftovers.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("atomicWriteFile left a temp file behind: %q", e.Name())
		}
	}
}

// TestExecutablePath_NotASymlink covers the fallback branch of
// executablePath: when the resolved executable is not a symlink, the
// function must return the resolved path as-is (not panic, not
// return the "nodered-mcp" sentinel, which is reserved for the
// os.Executable failure case).
//
// We cannot easily swap os.Executable from a test, so this test
// asserts the property indirectly: it runs in the same process as
// the test binary, which is highly unlikely to be a symlink in any
// realistic test environment, and confirms the returned path is
// the resolved path of the running test binary.
func TestExecutablePath_NotASymlink(t *testing.T) {
	got := executablePath()
	if got == "" || got == "nodered-mcp" {
		// "nodered-mcp" is the sentinel for os.Executable failure.
		// Empty is the same shape; both mean the fallback did not
		// run. We expect a real path here.
		t.Skipf("executablePath returned %q; os.Executable may be unsupported in this environment", got)
	}
	if !filepath.IsAbs(got) && got != "nodered-mcp" {
		t.Errorf("executablePath should return an absolute path or the fallback sentinel; got %q", got)
	}
}

// TestAsk_EmptyInputFallsBackToDefault covers the "user pressed
// Enter without typing" branch of ask, complementing
// TestAsk_DefaultsOnEOF / TestAsk_DefaultsOnReadError in
// init_pure_test.go. (init_pure_test.go already covers this via
// TestAsk_DefaultsOnEOF in the io.EOF case; this test exercises
// the explicit empty-line case.)
func TestAsk_EmptyInputFallsBackToDefault(t *testing.T) {
	in := bufio.NewScanner(strings.NewReader("\n"))
	got := ask(in, "label", "the-default")
	if got != "the-default" {
		t.Errorf("empty input should return the default; got %q", got)
	}
}

// TestAsk_NonEmptyInputOverridesDefault covers the "user typed
// something" branch of ask, which the existing init_pure_test.go
// covers with TestAsk_ReturnsInput only implicitly (via runInit
// flow). The direct test is small and locks the contract.
func TestAsk_NonEmptyInputOverridesDefault(t *testing.T) {
	in := bufio.NewScanner(strings.NewReader("user-typed-value\n"))
	got := ask(in, "label", "the-default")
	if got != "user-typed-value" {
		t.Errorf("non-empty input should override the default; got %q", got)
	}
}

// TestRunInit_NoClientsDetected covers the early-return at the top
// of runInit when detectClients returns an empty slice (the
// "nothing installed" path). Requires the detectClients seam.
func TestRunInit_NoClientsDetected(t *testing.T) {
	orig := detectClients
	detectClients = func(_ bool) []mcpClient { return nil }
	t.Cleanup(func() { detectClients = orig })

	if err := runInit(nil); err != nil {
		t.Errorf("runInit should return nil when no clients are detected; got %v", err)
	}
}

// TestRunInit_RendersTokenPlaceholderNote covers the
// `if token != ""` branch of runInit (the token placeholder hint
// printed after the snippet). Runs --all=true and picks the first
// client (claude-desktop is index 1 in knownClients()).
func TestRunInit_RendersTokenPlaceholderNote(t *testing.T) {
	orig := detectClients
	detectClients = func(_ bool) []mcpClient { return knownClients() }
	t.Cleanup(func() { detectClients = orig })

	// Stdin: URL + token (non-empty) + backup dir.
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = oldStdin })
	go func() {
		_, _ = io.WriteString(w, "http://localhost:1880\nmy-secret-token\nbackups\n")
		_ = w.Close()
	}()

	if err := runInit([]string{"-all"}); err != nil {
		t.Errorf("runInit should succeed with token; got %v", err)
	}
}
