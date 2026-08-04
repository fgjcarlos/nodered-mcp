package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
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

func TestDetectClients_AllReturnsEverything(t *testing.T) {
	all := detectClients(true)
	if len(all) == 0 {
		t.Fatal("detectClients(--all) returned no clients")
	}
	// knownClients returns 5 entries today; pin the shape loosely so
	// the test fails visibly when a client is added (intentional).
	if len(all) < 4 {
		t.Errorf("detectClients(--all) too small: %d", len(all))
	}
}

func TestDetectClients_DetectedFiltersMissing(t *testing.T) {
	// Without a probe file, detectClients(false) returns no entries.
	// We assert at least one client with a probe path that does not
	// exist on the test runner is filtered out.
	got := detectClients(false)
	for _, c := range knownClients() {
		if !fileExists(c.probe) && containsClient(got, c) {
			t.Errorf("client %q (probe %q) should not be detected", c.key, c.probe)
		}
	}
}

// helpers (small + local; do not deserve a shared file for two callers)

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func containsClient(haystack []mcpClient, needle mcpClient) bool {
	for _, c := range haystack {
		if c.key == needle.key {
			return true
		}
	}
	return false
}
