package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseIndex_Valid(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want int
	}{
		{"1", 5, 0},
		{"3", 5, 2},
		{"5", 5, 4},
	}
	for _, tc := range cases {
		if got := parseIndex(tc.in, tc.n); got != tc.want {
			t.Errorf("parseIndex(%q, %d) = %d, want %d", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestParseIndex_Invalid(t *testing.T) {
	cases := []struct {
		in string
		n  int
	}{
		{"", 5},     // empty
		{"0", 5},    // 1-based; 0 invalid
		{"6", 5},    // out of range
		{"abc", 5},  // non-numeric
		{"1abc", 5}, // partial numeric
		{"-1", 5},   // negative
		{"1.5", 5},  // non-integer
	}
	for _, tc := range cases {
		if got := parseIndex(tc.in, tc.n); got != -1 {
			t.Errorf("parseIndex(%q, %d) = %d, want -1", tc.in, tc.n, got)
		}
	}
}

func TestBuildEnv_OnlyURL(t *testing.T) {
	env := buildEnv("http://localhost:1880", "", "")
	if got := env["NODERED_URL"]; got != "http://localhost:1880" {
		t.Errorf("NODERED_URL: got %q", got)
	}
	if _, ok := env["NODERED_TOKEN"]; ok {
		t.Error("empty token should not produce NODERED_TOKEN entry")
	}
	if _, ok := env["NODERED_BACKUP_DIR"]; ok {
		t.Error("default backup dir should not produce NODERED_BACKUP_DIR entry")
	}
}

func TestBuildEnv_WithToken(t *testing.T) {
	env := buildEnv("http://localhost:1880", "secret-token", "")
	if env["NODERED_TOKEN"] != "secret-token" {
		t.Errorf("NODERED_TOKEN: got %q", env["NODERED_TOKEN"])
	}
}

func TestBuildEnv_WithCustomBackupDir(t *testing.T) {
	env := buildEnv("http://localhost:1880", "", "/var/backups/nodered")
	if env["NODERED_BACKUP_DIR"] != "/var/backups/nodered" {
		t.Errorf("NODERED_BACKUP_DIR: got %q", env["NODERED_BACKUP_DIR"])
	}
}

func TestBuildEnv_DefaultBackupDirOmitted(t *testing.T) {
	// "backups" is the documented default; emitting NODERED_BACKUP_DIR=backups
	// would be redundant and bloat the rendered config.
	env := buildEnv("http://localhost:1880", "", "backups")
	if _, ok := env["NODERED_BACKUP_DIR"]; ok {
		t.Errorf("default backup dir should be omitted; got %q", env["NODERED_BACKUP_DIR"])
	}
}

func TestEnvWithoutToken_OmitsToken(t *testing.T) {
	in := map[string]string{
		"NODERED_URL":        "http://localhost:1880",
		"NODERED_TOKEN":      "real-secret",
		"NODERED_BACKUP_DIR": "/var/backups",
	}
	out := envWithoutToken(in)
	if _, ok := out["NODERED_TOKEN"]; ok {
		t.Error("NODERED_TOKEN must be omitted rather than replaced with a placeholder")
	}
	if out["NODERED_URL"] != "http://localhost:1880" {
		t.Error("non-token envs must be passed through unchanged")
	}
	// Mutating the returned map must not affect the input.
	out["NODERED_URL"] = "changed"
	if in["NODERED_URL"] != "http://localhost:1880" {
		t.Error("envWithoutToken must return a defensive copy")
	}
}

func TestEnvWithoutToken_NoTokenIsNoOp(t *testing.T) {
	in := map[string]string{"NODERED_URL": "http://localhost:1880"}
	out := envWithoutToken(in)
	if _, ok := out["NODERED_TOKEN"]; ok {
		t.Error("token entry must not appear when no token was set")
	}
}

func TestMarshalIndentedJSON_ValidAndFormatted(t *testing.T) {
	out, err := marshalIndentedJSON(map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("marshalIndentedJSON: %v", err)
	}
	if !strings.Contains(string(out), "\n") {
		t.Error("expected indented (multiline) output")
	}
	var round map[string]string
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if round["k"] != "v" {
		t.Errorf("roundtrip: got %q", round["k"])
	}
}

func TestMarshalIndentedJSON_NoHTMLEscape(t *testing.T) {
	// marshalIndentedJSON sets SetEscapeHTML(false); verify a default
	// encoder would have escaped the angle brackets and ours did not.
	out, err := marshalIndentedJSON(map[string]string{"url": "http://x/?a=1&b=<2>"})
	if err != nil {
		t.Fatalf("marshalIndentedJSON: %v", err)
	}
	if strings.Contains(string(out), `&amp;`) || strings.Contains(string(out), `&lt;`) {
		t.Errorf("HTML entities should not be escaped; got %s", out)
	}
}

// ask reads a single line from the scanner and returns it (or the default
// on EOF / read failure).
func TestAsk_ReturnsInput(t *testing.T) {
	in := bufio.NewScanner(strings.NewReader("hello\n"))
	if got := ask(in, "label", "def"); got != "hello" {
		t.Errorf("ask: got %q want %q", got, "hello")
	}
}

func TestAsk_DefaultsOnEOF(t *testing.T) {
	in := bufio.NewScanner(strings.NewReader(""))
	if got := ask(in, "label", "fallback"); got != "fallback" {
		t.Errorf("ask EOF: got %q want %q", got, "fallback")
	}
}

func TestAsk_DefaultsOnReadError(t *testing.T) {
	in := bufio.NewScanner(io.NopCloser(strings.NewReader(""))) // empty -> no tokens
	in.Split(func(_ []byte, _ bool) (int, []byte, error) { return 0, nil, io.ErrUnexpectedEOF })
	if got := ask(in, "label", "fb"); got != "fb" {
		t.Errorf("ask err: got %q want %q", got, "fb")
	}
}

func TestDetectClients_AllReturnsCatalog(t *testing.T) {
	all := detectClients(true)
	if len(all) != 7 {
		t.Fatalf("detectClients(--all) returned %d clients, want 7", len(all))
	}
}

func TestClientInstalledEvidence(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T) mcpClient
		want    bool
	}{
		{
			name: "no executable or path evidence",
			prepare: func(t *testing.T) mcpClient {
				t.Setenv("PATH", t.TempDir())
				return mcpClient{commands: []string{"missing-test-client"}, probes: []string{filepath.Join(t.TempDir(), "missing")}}
			},
			want: false,
		},
		{
			name: "stale Cursor directory without config or app",
			prepare: func(t *testing.T) mcpClient {
				home := t.TempDir()
				dir := filepath.Join(home, ".cursor")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				return mcpClient{probes: []string{filepath.Join(dir, "mcp.json")}}
			},
			want: false,
		},
		{
			name: "CLI executable before first-run config",
			prepare: func(t *testing.T) mcpClient {
				if runtime.GOOS == "windows" {
					t.Skip("executable mode bits differ on Windows")
				}
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "gemini"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", dir)
				return mcpClient{commands: []string{"gemini"}}
			},
			want: true,
		},
		{
			name: "stale CLI config without executable",
			prepare: func(t *testing.T) mcpClient {
				home := t.TempDir()
				config := filepath.Join(home, ".gemini", "settings.json")
				if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(config, []byte(`{}`), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", t.TempDir())
				for _, client := range clientCatalog(runtime.GOOS, home, filepath.Join(home, "config"), "", "") {
					if client.key == "gemini" {
						return client
					}
				}
				t.Fatal("Gemini client missing from catalog")
				return mcpClient{}
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clientInstalled(tc.prepare(t)); got != tc.want {
				t.Fatalf("clientInstalled() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestClientCatalogPlatformProbes(t *testing.T) {
	home, cfg, appData, local := "/users/test", "/config", "/roaming", "/local"
	cases := []struct {
		goos, key string
		want      []string
	}{
		{"windows", "claude-desktop", []string{filepath.Join(appData, "Claude", "claude_desktop_config.json"), filepath.Join(local, "Programs", "Claude", "Claude.exe")}},
		{"windows", "cursor", []string{filepath.Join(home, ".cursor", "mcp.json"), filepath.Join(local, "Programs", "Cursor", "Cursor.exe")}},
		{"darwin", "cursor", []string{filepath.Join(home, ".cursor", "mcp.json"), "/Applications/Cursor.app", filepath.Join(home, "Applications", "Cursor.app")}},
		{"linux", "vscode", []string{filepath.Join(cfg, "Code", "User", "mcp.json"), "/usr/share/applications/code.desktop", filepath.Join(home, ".local", "share", "applications", "code.desktop")}},
	}
	for _, tc := range cases {
		t.Run(tc.goos+"/"+tc.key, func(t *testing.T) {
			var got []string
			for _, client := range clientCatalog(tc.goos, home, cfg, appData, local) {
				if client.key == tc.key {
					got = client.probes
					break
				}
			}
			for _, want := range tc.want {
				found := false
				for _, path := range got {
					if path == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("catalog probes %q missing %q", got, want)
				}
			}
		})
	}
}

func TestClientCatalogMatchesDocumentationAndExamples(t *testing.T) {
	clients := clientCatalog(runtime.GOOS, "/home/test", "/config", "/appdata", "/localappdata")
	repoRoot := filepath.Join("..", "..")
	doc, err := os.ReadFile(filepath.Join(repoRoot, "docs", "clients.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Non-client sections are allowed, but the list is explicit so a new
	// heading cannot slip in unnoticed.
	expectedSections := map[string]bool{"HTTP variant": true, "Previewing a merge": true}
	expectedExamples := make(map[string]bool)
	for _, client := range clients {
		expectedSections[client.name] = true
		if !strings.Contains(string(doc), "## "+client.name+"\n") {
			t.Errorf("client %q is missing a docs/clients.md section", client.name)
		}
		if client.example != "" {
			expectedExamples[filepath.Base(client.example)] = true
			if _, err := os.Stat(filepath.Join(repoRoot, client.example)); err != nil {
				t.Errorf("client %q example %q is missing: %v", client.key, client.example, err)
			}
			if !strings.Contains(string(doc), "../"+client.example) {
				t.Errorf("client %q example %q is not linked from docs/clients.md", client.key, client.example)
			}
		}
	}
	for _, line := range strings.Split(string(doc), "\n") {
		if strings.HasPrefix(line, "## ") {
			section := strings.TrimPrefix(line, "## ")
			if !expectedSections[section] {
				t.Errorf("docs/clients.md section %q is not in the client catalog", section)
			}
		}
	}
	files, err := os.ReadDir(filepath.Join(repoRoot, "examples"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if filepath.Ext(file.Name()) == ".json" && !expectedExamples[file.Name()] {
			t.Errorf("example %q is not assigned to a client in the catalog", file.Name())
		}
	}
}
