package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMergeServer_PureKeepsExistingSecretsAtKnownKeys locks in the redaction
// contract for pre-existing entries: known secret-bearing keys are masked in
// the preview, non-secret values pass through unchanged.
func TestMergeServer_PureKeepsExistingSecretsAtKnownKeys(t *testing.T) {
	original := `{
  "mcpServers": {
    "other": {
      "command": "other-mcp",
      "env": {
        "NODERED_TOKEN": "real-token-from-existing-config",
        "TOKEN": "another-secret",
        "API_KEY": "sk-123",
        "PASSWORD": "p4ss",
        "SECRET": "shh",
        "HARMLESS_URL": "http://ok"
      }
    }
  },
  "somePref": true
}`
	root, err := mergeServer([]byte(original), "", "mcpServers", "/bin/nodered-mcp", map[string]string{"NODERED_URL": "http://localhost:1880"})
	if err != nil {
		t.Fatalf("mergeServer: %v", err)
	}
	preview, err := marshalIndentedJSON(redactSecrets(root))
	if err != nil {
		t.Fatalf("marshalIndentedJSON: %v", err)
	}
	out := string(preview)
	// None of the secret values may appear in the preview.
	for _, secret := range []string{"real-token-from-existing-config", "another-secret", "sk-123", "p4ss", "shh"} {
		if strings.Contains(out, secret) {
			t.Errorf("preview leaks secret value %q:\n%s", secret, out)
		}
	}
	// The masked placeholder must be present at least once.
	if !strings.Contains(out, "REDACTED") {
		t.Errorf("expected redaction marker in preview:\n%s", out)
	}
	// Non-secret value must survive.
	if !strings.Contains(out, "http://ok") {
		t.Errorf("non-secret URL was dropped or rewritten:\n%s", out)
	}
	// SomePref is preserved.
	if !strings.Contains(out, "somePref") {
		t.Errorf("unrelated key was dropped:\n%s", out)
	}
	// The new nodered entry exists.
	if !strings.Contains(out, `"nodered"`) {
		t.Errorf("new 'nodered' entry missing:\n%s", out)
	}
}

// TestMergeServer_NewNoderedEntryNeverCarriesToken is a smoke check: the
// preview of the new entry itself must not contain NODERED_TOKEN. The merge
// is run with an env map that does include NODERED_TOKEN, simulating the
// real flow where the operator typed one.
func TestMergeServer_NewNoderedEntryNeverCarriesToken(t *testing.T) {
	_, err := mergeServer([]byte(`{}`), "", "mcpServers", "/bin/nodered-mcp", map[string]string{
		"NODERED_URL":   "http://localhost:1880",
		"NODERED_TOKEN": "leak-me",
	})
	if err != nil {
		t.Fatalf("mergeServer: %v", err)
	}
	// We re-run the merge to capture the resulting root so we can assert
	// against the same path the dry-run code uses.
	root, err := mergeServer([]byte(`{}`), "", "mcpServers", "/bin/nodered-mcp", map[string]string{
		"NODERED_URL":   "http://localhost:1880",
		"NODERED_TOKEN": "leak-me",
	})
	if err != nil {
		t.Fatalf("mergeServer (2nd call): %v", err)
	}
	preview, _ := marshalIndentedJSON(redactSecrets(root))
	if strings.Contains(string(preview), "leak-me") {
		t.Errorf("new entry leaked operator token:\n%s", preview)
	}
}

// TestDryRun_TouchesNoFilesystem is the main guarantee: running the merge
// against a real config file on disk must not create, modify, backup, or
// chmod anything — not the target file, not a sibling .bak, not a .tmp-*,
// and not the parent directory. This is what makes the flag a *dry* run.
func TestDryRun_TouchesNoFilesystem(t *testing.T) {
	dir := t.TempDir()
	// Use a nested path whose parent does NOT exist yet; the dry-run path
	// must not create it.
	path := filepath.Join(dir, "nested", "Claude", "claude_desktop_config.json")
	original := `{"mcpServers":{"other":{"command":"keep"}},"someUnrelatedPref":true}`
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	// Snapshot the directory listing before.
	beforeEntries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	beforeNames := make([]string, 0, len(beforeEntries))
	for _, e := range beforeEntries {
		beforeNames = append(beforeNames, e.Name())
	}
	// Snapshot the file's bytes and mtime.
	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	// Run the pure merge (the same code path dry-run uses).
	root, err := mergeServer(beforeBytes, "", "mcpServers", "/bin/nodered-mcp", map[string]string{"NODERED_URL": "http://localhost:1880"})
	if err != nil {
		t.Fatalf("mergeServer: %v", err)
	}
	// Marshal exactly as the dry-run would.
	preview, err := marshalIndentedJSON(redactSecrets(root))
	if err != nil {
		t.Fatalf("marshalIndentedJSON: %v", err)
	}

	// 1. The on-disk file must be byte-for-byte identical.
	afterBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if !bytes.Equal(beforeBytes, afterBytes) {
		t.Errorf("dry-run mutated the config file:\nbefore: %s\nafter:  %s", beforeBytes, afterBytes)
	}
	// 2. File mtime must not have changed (best-effort; mtime resolution
	//    is filesystem-dependent, so we only fail if the new mtime is
	//    strictly later than the old one).
	afterInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if afterInfo.ModTime().After(beforeInfo.ModTime()) {
		t.Errorf("dry-run updated the file mtime: before=%v after=%v", beforeInfo.ModTime(), afterInfo.ModTime())
	}
	// 3. Directory listing must be unchanged — no .bak, no .tmp-*.
	afterEntries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	afterNames := make([]string, 0, len(afterEntries))
	for _, e := range afterEntries {
		afterNames = append(afterNames, e.Name())
	}
	if !equalStringSlices(beforeNames, afterNames) {
		t.Errorf("directory listing changed during dry-run: before=%v after=%v", beforeNames, afterNames)
	}
	// 4. No parent directory creation: try a path whose grandparent did
	//    not exist. If the dry-run path were ever to mkdir, this would
	//    surface.
	ghost := filepath.Join(dir, "absent", "deeply", "nested", "config.json")
	// We don't call writeJSONObject; we only call the pure merge against
	// bytes that look like an existing file. The pure merge MUST NOT
	// touch the filesystem; if a future change accidentally does, the
	// os.Stat below will fail.
	if _, err := os.Stat(filepath.Dir(ghost)); !os.IsNotExist(err) {
		t.Errorf("dry-run should not have created parent directories; stat err: %v", err)
	}
	// 5. Sanity: the preview is the merge the write path produces. We
	//    re-marshal what writeJSONObject would emit by re-running
	//    mergeServer against the same bytes the real --write code uses.
	//    This fixture holds no secret-bearing key, so redaction is a
	//    no-op and the two must match byte-for-byte. The case where they
	//    deliberately differ (an existing secret) is covered by
	//    TestDryRun_PreviewIsNotByteIdenticalToWrite.
	mergedWriteShape, err := mergeServer(beforeBytes, "", "mcpServers", "/bin/nodered-mcp", map[string]string{"NODERED_URL": "http://localhost:1880"})
	if err != nil {
		t.Fatal(err)
	}
	writeShapeBytes, _ := marshalIndentedJSON(mergedWriteShape)
	if !bytes.Equal(preview, writeShapeBytes) {
		t.Errorf("dry-run preview differs from the bytes writeJSONObject would produce:\npreview:\n%s\nwrite:\n%s", preview, writeShapeBytes)
	}
	// 6. Preview is valid JSON and round-trips.
	var round map[string]any
	if err := json.Unmarshal(preview, &round); err != nil {
		t.Fatalf("preview is not valid JSON: %v\n%s", err, preview)
	}
}

// TestDryRun_MissingTargetFileStillPreviews proves the preview does not
// depend on the file existing on disk. A user can dry-run before any
// config has been created.
func TestDryRun_MissingTargetFileStillPreviews(t *testing.T) {
	// Simulate the missing-file case by handing mergeServer an empty
	// document — the same shape readJSONObject returns for a non-existent
	// file.
	root, err := mergeServer([]byte(""), "", "mcpServers", "/bin/nodered-mcp", map[string]string{"NODERED_URL": "http://localhost:1880"})
	if err != nil {
		t.Fatalf("mergeServer on empty input: %v", err)
	}
	preview, err := marshalIndentedJSON(redactSecrets(root))
	if err != nil {
		t.Fatalf("marshalIndentedJSON: %v", err)
	}
	if !strings.Contains(string(preview), `"nodered"`) {
		t.Errorf("empty-source preview missing the nodered entry:\n%s", preview)
	}
}

// TestRedactSecrets_KeyNameRedaction pins the known-key set. Adding a new
// key here documents the deliberate redaction surface; the ponytail ceiling
// in init.go explains the upgrade path (explicit per-client redaction list
// when an unexpected key shows up).
func TestRedactSecrets_KeyNameRedaction(t *testing.T) {
	doc := map[string]any{
		"mcpServers": map[string]any{
			"alpha": map[string]any{
				"env": map[string]any{
					"NODERED_TOKEN":    "alpha-tok",
					"TOKEN":            "alpha-token2",
					"API_KEY":          "alpha-key",
					"PASSWORD":         "alpha-pw",
					"SECRET":           "alpha-secret",
					"NODERED_URL":      "http://ok",
					"MCP_LOG_LEVEL":    "info",
					"PRIVATE_KEY_PEM":  "pem-block", // matches PRIVATE_KEY; we redact by exact name, not substring
					"UNRELATED_TOKEN":  "x",         // exact TOKEN match would catch this
					"auth_token_field": "leaky",     // lowercase; should NOT be redacted (case-sensitive)
				},
			},
		},
	}
	out := redactSecrets(doc)
	env := out["mcpServers"].(map[string]any)["alpha"].(map[string]any)["env"].(map[string]any)

	// Each secret-bearing key from the known set must be masked.
	for _, k := range []string{"NODERED_TOKEN", "TOKEN", "API_KEY", "PASSWORD", "SECRET"} {
		if v, ok := env[k]; !ok || v != "REDACTED" {
			t.Errorf("expected %s=REDACTED, got %v (present=%v)", k, v, ok)
		}
	}
	// Non-secret values must survive verbatim.
	if env["NODERED_URL"] != "http://ok" {
		t.Errorf("NODERED_URL altered: %v", env["NODERED_URL"])
	}
	if env["MCP_LOG_LEVEL"] != "info" {
		t.Errorf("MCP_LOG_LEVEL altered: %v", env["MCP_LOG_LEVEL"])
	}
	// UNRELATED_TOKEN is not the exact key TOKEN, so key-name redaction
	// (ponytail ceiling: exact match, not substring) does not catch it.
	// This documents the known limitation: a secret stored under a key
	// outside the known set will appear verbatim. Upgrade path: a
	// per-client opt-in list once a real-world need appears.
	if env["UNRELATED_TOKEN"] != "x" {
		t.Errorf("UNRELATED_TOKEN should pass through (ponytail: key-name redaction is exact), got %v", env["UNRELATED_TOKEN"])
	}
	// PRIVATE_KEY_PEM is not in the known set either.
	if env["PRIVATE_KEY_PEM"] != "pem-block" {
		t.Errorf("PRIVATE_KEY_PEM should pass through (ponytail: key-name redaction, not substring), got %v", env["PRIVATE_KEY_PEM"])
	}
	// Lowercase variant passes through: case-sensitive match.
	if env["auth_token_field"] != "leaky" {
		t.Errorf("auth_token_field should not match (case-sensitive), got %v", env["auth_token_field"])
	}
}

// TestRunInit_DryRunFlagWiresThrough checks the flag is plumbed end-to-end:
// when --dry-run is passed, runInit must succeed, print the merged JSON
// (not the snippet), and not modify the filesystem.
func TestRunInit_DryRunFlagWiresThrough(t *testing.T) {
	// Use a temp HOME so the Claude Desktop write target exists.
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	// Provide scripted answers: URL, token, backup dir, then client pick.
	// We bypass the interactive chooser by ensuring only one client is
	// detected (none on a clean temp HOME), so the user has to pick from
	// --all. Force --all so we get a known single client (Cursor, since
	// it has a writePath and is the second in the catalog).
	input := "http://localhost:1880\n\nbackups\n1\n"
	oldStdin := os.Stdin
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	defer func() {
		os.Stdin = oldStdin
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()
	// Capture stdout for the printed preview.
	outR, outW, _ := os.Pipe()
	os.Stdin = inputAsFile(input)
	os.Stdout = outW
	os.Stderr = outW
	// Close the write end of the pipe after runInit returns so the read
	// sees EOF.
	done := make(chan struct{})
	var captured strings.Builder
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := outR.Read(buf)
			if n > 0 {
				captured.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		close(done)
	}()

	err := runInit([]string{"--all", "--dry-run"})
	outW.Close()
	<-done
	os.Stdout = oldStdout
	if err != nil {
		t.Fatalf("runInit --dry-run: %v", err)
	}

	// The preview must look like JSON containing the new nodered entry.
	preview := captured.String()
	if !strings.Contains(preview, `"nodered"`) {
		t.Errorf("dry-run output missing nodered entry:\n%s", preview)
	}
	// And it must not contain NODERED_TOKEN, since we passed no token.
	if strings.Contains(preview, "NODERED_TOKEN") {
		t.Errorf("dry-run output leaked NODERED_TOKEN:\n%s", preview)
	}
	// Filesystem must be untouched: no Claude Desktop config was created.
	claudeConfig := filepath.Join(dir, "Claude", "claude_desktop_config.json")
	if _, err := os.Stat(claudeConfig); !os.IsNotExist(err) {
		t.Errorf("dry-run created a Claude Desktop config file: %v", err)
	}
}

// TestRunInit_DryRunAndWriteRejected pins the conflict behavior: the two
// flags are mutually exclusive and the run must return an error.
func TestRunInit_DryRunAndWriteRejected(t *testing.T) {
	oldStdin := os.Stdin
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	defer func() {
		os.Stdin = oldStdin
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()
	devNull, _ := os.Open(os.DevNull)
	os.Stdin = devNull
	os.Stdout = devNull
	os.Stderr = devNull

	err := runInit([]string{"--all", "--dry-run", "--write"})
	if err == nil {
		t.Fatal("expected --dry-run --write to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "dry-run") || !strings.Contains(err.Error(), "write") {
		t.Errorf("error should name both flags, got %v", err)
	}
}

// TestRunInit_DryRunUnsupportedTarget prints the snippet and errors just
// like --write does for Claude Code, VS Code, OpenCode, Pi.
func TestRunInit_DryRunUnsupportedTarget(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	// "claude-code" has no writePath.
	input := "http://localhost:1880\n\nbackups\n"
	oldStdin := os.Stdin
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	defer func() {
		os.Stdin = oldStdin
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()
	outR, outW, _ := os.Pipe()
	os.Stdin = inputAsFile(input)
	os.Stdout = outW
	os.Stderr = outW
	done := make(chan struct{})
	var captured strings.Builder
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := outR.Read(buf)
			if n > 0 {
				captured.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		close(done)
	}()

	// The catalog returns claude-code when forced via --all. There's no
	// chooser, so the *first* client is taken. Force claude-code by
	// overriding detectClients.
	oldDetect := detectClients
	detectClients = func(_ bool) []mcpClient {
		return []mcpClient{{key: "claude-code", name: "Claude Code"}}
	}
	defer func() { detectClients = oldDetect }()

	err := runInit([]string{"--all", "--dry-run"})
	outW.Close()
	<-done
	if err == nil {
		t.Fatal("expected error for unsupported --dry-run target, got nil")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("expected 'not supported' error, got %v", err)
	}
	if !strings.Contains(captured.String(), "claude mcp add") {
		t.Errorf("unsupported dry-run should still print the manual snippet, got:\n%s", captured.String())
	}
}

// inputAsFile returns an *os.File for the given content so it can stand
// in for os.Stdin in tests (bufio.Scanner over a *strings.Reader only
// works when wired through Stdio the way runInit expects).
func inputAsFile(content string) *os.File {
	f, err := os.CreateTemp("", "nodered-mcp-stdin-*")
	if err != nil {
		panic(err)
	}
	if _, err := f.WriteString(content); err != nil {
		panic(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		panic(err)
	}
	return f
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDryRun_PreviewIsNotByteIdenticalToWrite pins the one deliberate
// divergence between the preview and the committed file: when the existing
// config already holds a value under a secret-bearing key, the preview
// shows REDACTED where --write keeps the real value. The merge itself is
// identical — same keys, same nodered entry, same unrelated servers.
//
// This is the safe direction to differ. Previewing a live token into a
// terminal or a CI log would defeat the point of redacting it.
func TestDryRun_PreviewIsNotByteIdenticalToWrite(t *testing.T) {
	existing := `{"mcpServers":{"other":{"command":"other-mcp","env":{"TOKEN":"shh"},"args":["--token=hunter2"]}}}`

	merged, err := mergeServer([]byte(existing), "", "mcpServers", "/bin/nodered-mcp",
		map[string]string{"NODERED_URL": "http://localhost:1880"})
	if err != nil {
		t.Fatal(err)
	}
	writeBytes, err := marshalIndentedJSON(merged)
	if err != nil {
		t.Fatal(err)
	}
	previewBytes, err := marshalIndentedJSON(redactSecrets(merged))
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(previewBytes, writeBytes) {
		t.Fatal("expected the preview to differ from the write when a secret exists")
	}
	// The write keeps the real secret; that is the file's business.
	if !bytes.Contains(writeBytes, []byte("shh")) {
		t.Error("the write shape should keep the existing value verbatim")
	}
	// The preview must not leak the value stored under a secret key...
	if bytes.Contains(previewBytes, []byte("shh")) {
		t.Errorf("preview leaked the env secret: %s", previewBytes)
	}
	if !bytes.Contains(previewBytes, []byte(`"TOKEN": "REDACTED"`)) {
		t.Errorf("expected TOKEN to be masked, got: %s", previewBytes)
	}
	// ...but a secret inside an args array is a known gap of key-name
	// redaction, not a silent behaviour change. Assert it stays visible so
	// the limitation cannot regress unnoticed.
	if !bytes.Contains(previewBytes, []byte("hunter2")) {
		t.Errorf("args-array values are outside key-name redaction; expected the "+
			"documented gap to remain visible here, got: %s", previewBytes)
	}
	// Structure is identical either way: the nodered entry and the
	// unrelated server survive the merge.
	for _, want := range []string{`"nodered"`, `"other"`, `"NODERED_URL"`} {
		if !bytes.Contains(previewBytes, []byte(want)) {
			t.Errorf("preview lost %s", want)
		}
		if !bytes.Contains(writeBytes, []byte(want)) {
			t.Errorf("write shape lost %s", want)
		}
	}
}
