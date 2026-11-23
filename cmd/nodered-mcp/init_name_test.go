package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunInit_NameFormats(t *testing.T) {
	const name = "plant_1-west"
	const url = "http://localhost:1881"
	oldDetect := detectClients
	t.Cleanup(func() { detectClients = oldDetect })
	for _, c := range knownClients() {
		t.Run(c.key, func(t *testing.T) {
			detectClients = func(bool) []mcpClient { return []mcpClient{c} }
			got, stderr, err := runInitOutput(t, url+"\n\n", "--name", name)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stderr, "use a distinct absolute backup directory") {
				t.Fatalf("missing backup recommendation: %q", stderr)
			}
			plain, plainStderr, err := runInitOutput(t, url+"\n\n")
			if err != nil || strings.Contains(plainStderr, "use a distinct absolute backup directory") {
				t.Fatalf("default init: %v, stderr = %q", err, plainStderr)
			}
			if c.key == "claude-code" {
				want := "claude mcp add -s user " + name + " -e 'NODERED_URL=" + url + "' -- " + shellQuote(executablePath()) + "\n"
				if got != want || plain != strings.Replace(want, name, "nodered", 1) {
					t.Fatalf("named/default command = %q / %q; want %q with selected name", got, plain, want)
				}
				return
			}
			if got != strings.Replace(plain, `"nodered":`, `"`+name+`":`, 1) {
				t.Fatalf("named snippet must differ only by entry name:\n%s\n%s", got, plain)
			}
			var doc map[string]json.RawMessage
			// OpenCode also emits a string-valued $schema property.
			var root map[string]json.RawMessage
			if err := json.Unmarshal([]byte(got), &root); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(root[c.rootKey], &doc); err != nil || len(doc) != 1 || doc[name] == nil {
				t.Fatalf("missing sole %s.%s entry: %s (%v)", c.rootKey, name, got, err)
			}
		})
	}
}

func TestRunInit_NameRejectsInvalid(t *testing.T) {
	oldDetect := detectClients
	t.Cleanup(func() { detectClients = oldDetect })
	detectClients = func(bool) []mcpClient { t.Fatal("invalid name reached client detection"); return nil }
	for _, name := range []string{"", "two words", "bad.name", "-flag", "plánt", "x/y"} {
		t.Run(name, func(t *testing.T) {
			out, stderr, err := runInitOutput(t, "", "--write", "--name", name)
			if err == nil || !strings.HasPrefix(err.Error(), "invalid --name ") || out != "" || stderr != "" {
				t.Fatalf("out = %q, stderr = %q, err = %v; want only the invalid-name error", out, stderr, err)
			}
		})
	}
}

func TestRunInit_NameMerge(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	oldDetect := detectClients
	t.Cleanup(func() { detectClients = oldDetect })
	detectClients = func(bool) []mcpClient { return []mcpClient{clientByKey("cursor")} }
	path := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	const original = `{"mcpServers":{"nodered":{"command":"original"}},"other":true}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	backup := filepath.Join(home, "instance-backups")
	input := "http://localhost:1881\n\n" + backup + "\n"
	preview, stderr, err := runInitOutput(t, input, "--name", "second", "--dry-run")
	if err != nil || strings.Contains(stderr, "use a distinct absolute backup directory") || strings.Contains(stderr, "confirmation") {
		t.Fatalf("new entry preview: %v, stderr = %q", err, stderr)
	}
	var root struct {
		Servers map[string]struct {
			Command string
			Env     map[string]string
		} `json:"mcpServers"`
		Other bool
	}
	if err := json.Unmarshal([]byte(preview), &root); err != nil || len(root.Servers) != 2 ||
		root.Servers["nodered"].Command != "original" || !root.Other ||
		root.Servers["second"].Env["NODERED_BACKUP_DIR"] != backup || root.Servers["second"].Env["NODERED_URL"] != "http://localhost:1881" {
		t.Fatalf("preview must merge only the named entry: %s (%v)", preview, err)
	}
	if _, err := os.Stat(path + ".bak"); read(path) != original || !os.IsNotExist(err) {
		t.Fatal("preview changed config or created a backup")
	}
	out, stderr, err := runInitOutput(t, input, "--name", "second", "--write")
	if err != nil || out != "" || !strings.Contains(stderr, "wrote 'second' server") || read(path) != preview || read(path+".bak") != original {
		t.Fatalf("named write must match preview and back up the original: %v, out = %q, stderr = %q", err, out, stderr)
	}

	updatedInput := strings.Replace(input, ":1881", ":1882", 1)
	updated, stderr, err := runInitOutput(t, updatedInput, "--name", "second", "--dry-run")
	if err != nil || updated != strings.Replace(preview, ":1881", ":1882", 1) || !strings.Contains(stderr, "requires confirmation") || read(path) != preview || read(path+".bak") != original {
		t.Fatalf("collision preview must show the change without writing: %v, out = %q, stderr = %q", err, updated, stderr)
	}
	out, stderr, err = runInitOutput(t, updatedInput, "--name", "second", "--write")
	if err == nil || err.Error() != `refusing to overwrite existing server "second" without confirmation` || out != "" || !strings.Contains(stderr, "[no]") || read(path) != preview || read(path+".bak") != original {
		t.Fatalf("EOF must refuse the overwrite without writing: %v, out = %q, stderr = %q", err, out, stderr)
	}
	out, stderr, err = runInitOutput(t, updatedInput+"yes\n", "--name", "second", "--write")
	if err != nil || out != "" || !strings.Contains(stderr, "wrote 'second' server") || read(path) != updated || read(path+".bak") != preview {
		t.Fatalf("confirmed write must replace only the named entry: %v, out = %q, stderr = %q", err, out, stderr)
	}
	legacyPreview, stderr, err := runInitOutput(t, input, "--dry-run")
	if err != nil || strings.Contains(stderr, "confirmation") {
		t.Fatalf("default preview changed: %v, stderr = %q", err, stderr)
	}
	out, stderr, err = runInitOutput(t, input, "--write")
	if err != nil || out != "" || !strings.Contains(stderr, "wrote 'nodered' server") || strings.Contains(stderr, "[no]") || read(path) != legacyPreview || read(path+".bak") != updated {
		t.Fatalf("default write must still update nodered without a prompt: %v, out = %q, stderr = %q", err, out, stderr)
	}
}
