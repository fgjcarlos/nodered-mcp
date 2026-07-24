package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// mcpClient is one MCP client we can generate config for.
type mcpClient struct {
	key  string
	name string
	// probe is the path whose existence means the client is installed.
	probe string
	// note tells the user where the snippet goes.
	note string
}

// knownClients lists the clients init can target, with per-OS config paths.
func knownClients() []mcpClient {
	home, _ := os.UserHomeDir()
	cfg, _ := os.UserConfigDir()
	return []mcpClient{
		{"claude-desktop", "Claude Desktop", filepath.Join(cfg, "Claude", "claude_desktop_config.json"),
			"paste into " + filepath.Join(cfg, "Claude", "claude_desktop_config.json")},
		{"claude-code", "Claude Code", filepath.Join(home, ".claude.json"),
			"run the command below"},
		{"cursor", "Cursor", filepath.Join(home, ".cursor"),
			"paste into .cursor/mcp.json (workspace) or ~/.cursor/mcp.json (global)"},
		{"vscode", "VS Code", filepath.Join(cfg, "Code", "User"),
			"paste into .vscode/mcp.json"},
		{"gemini", "Gemini CLI", filepath.Join(home, ".gemini"),
			"paste into " + filepath.Join(home, ".gemini", "settings.json")},
	}
}

func detectClients(all bool) []mcpClient {
	var out []mcpClient
	for _, c := range knownClients() {
		if all {
			out = append(out, c)
			continue
		}
		if _, err := os.Stat(c.probe); err == nil {
			out = append(out, c)
		}
	}
	return out
}

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	all := fs.Bool("all", false, "show every known client, not just detected ones")
	write := fs.Bool("write", false, "write the config into the client instead of printing it")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	// The config must point at the real binary — this is what prevents the
	// stale-path "spawn ENOENT" failure.
	bin, err := os.Executable()
	if err != nil || bin == "" {
		bin = "nodered-mcp"
	}

	in := bufio.NewScanner(os.Stdin)
	url := ask(in, "Node-RED URL", "http://localhost:1880")
	token := ask(in, "Node-RED token (optional, Enter to skip)", "")
	backupDir := ask(in, "Backup directory", "backups")

	clients := detectClients(*all)
	if len(clients) == 0 {
		fmt.Fprintln(os.Stderr, "No known MCP client detected. Re-run with --all to pick one manually.")
		return nil
	}

	target := chooseClient(in, clients)
	env := buildEnv(url, token, backupDir)

	if *write {
		return writeClientConfig(target, bin, env, url, token, backupDir)
	}

	fmt.Fprintf(os.Stderr, "\n--- %s: %s ---\n", target.name, target.note)
	fmt.Println(renderConfig(target.key, bin, url, token, backupDir))
	return nil
}

// writeClientConfig merges the 'nodered' server into the client's config file
// for clients whose target file is unambiguous. For workspace-scoped (VS Code)
// or CLI-managed (Claude Code) clients it falls back to printing, since
// auto-writing to a guessed location would be worse than a copy-paste.
func writeClientConfig(c mcpClient, bin string, env map[string]string, url, token, backupDir string) error {
	path, rootKey, ok := writableTarget(c.key)
	if !ok {
		fmt.Fprintf(os.Stderr, "\n--- %s: %s ---\n", c.name, c.note)
		fmt.Println(renderConfig(c.key, bin, url, token, backupDir))
		fmt.Fprintf(os.Stderr, "\n(--write isn't supported for %s — paste/run the above)\n", c.name)
		return nil
	}
	if err := mergeServerIntoFile(path, rootKey, bin, env); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "✓ wrote 'nodered' server to %s\n", path)
	fmt.Fprintln(os.Stderr, "  (previous file saved as .bak) — restart the client to load it.")
	return nil
}

// writableTarget returns the config file and root key for clients that can be
// written safely. ok is false for VS Code (workspace-scoped) and Claude Code
// (managed via `claude mcp add`).
func writableTarget(key string) (path, rootKey string, ok bool) {
	home, _ := os.UserHomeDir()
	cfg, _ := os.UserConfigDir()
	switch key {
	case "claude-desktop":
		return filepath.Join(cfg, "Claude", "claude_desktop_config.json"), "mcpServers", true
	case "cursor":
		return filepath.Join(home, ".cursor", "mcp.json"), "mcpServers", true
	case "gemini":
		return filepath.Join(home, ".gemini", "settings.json"), "mcpServers", true
	}
	return "", "", false
}

// mergeServerIntoFile adds/replaces the 'nodered' entry under rootKey, leaving
// every other key in the file untouched. Refuses to touch a file that exists
// but isn't valid JSON, and backs up the previous content to path+".bak".
func mergeServerIntoFile(path, rootKey, bin string, env map[string]string) error {
	root, err := readJSONObject(path)
	if err != nil {
		return err
	}
	servers, _ := root[rootKey].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers["nodered"] = map[string]any{"command": bin, "env": env}
	root[rootKey] = servers
	return writeJSONObject(path, root)
}

// readJSONObject reads a JSON object, returning an empty map if the file is
// missing or blank. A non-empty file that fails to parse is an error — we do
// NOT overwrite a config we can't understand.
func readJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("existing config at %s is not valid JSON — refusing to overwrite it: %w", path, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// writeJSONObject writes m as indented JSON, creating parent dirs and backing
// up any existing file to path+".bak" first.
func writeJSONObject(path string, m map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if data, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(path+".bak", data, 0o644)
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// ask prints a prompt to stderr and reads one line; empty input keeps def.
// Prompts go to stderr so stdout carries only the snippet (pipe-friendly).
func ask(in *bufio.Scanner, label, def string) string {
	if def != "" {
		fmt.Fprintf(os.Stderr, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(os.Stderr, "%s: ", label)
	}
	if !in.Scan() {
		return def
	}
	if v := strings.TrimSpace(in.Text()); v != "" {
		return v
	}
	return def
}

func chooseClient(in *bufio.Scanner, clients []mcpClient) mcpClient {
	if len(clients) == 1 {
		return clients[0]
	}
	fmt.Fprintln(os.Stderr, "\nDetected MCP clients:")
	for i, c := range clients {
		fmt.Fprintf(os.Stderr, "  %d) %s\n", i+1, c.name)
	}
	for {
		choice := ask(in, "Pick a client (number)", "1")
		if n := parseIndex(choice, len(clients)); n >= 0 {
			return clients[n]
		}
		fmt.Fprintln(os.Stderr, "  invalid choice")
	}
}

// parseIndex turns a 1-based string into a 0-based index, or -1 if invalid.
func parseIndex(s string, n int) int {
	i := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return -1
		}
		i = i*10 + int(r-'0')
	}
	if s == "" || i < 1 || i > n {
		return -1
	}
	return i - 1
}

// buildEnv assembles the env vars for the server, omitting empty/default ones.
func buildEnv(url, token, backupDir string) map[string]string {
	env := map[string]string{"NODERED_URL": url}
	if token != "" {
		env["NODERED_TOKEN"] = token
	}
	if backupDir != "" && backupDir != "backups" {
		env["NODERED_BACKUP_DIR"] = backupDir
	}
	return env
}

// renderConfig returns the ready-to-paste config for the given client.
func renderConfig(key, bin, url, token, backupDir string) string {
	env := buildEnv(url, token, backupDir)

	if key == "claude-code" {
		var b strings.Builder
		b.WriteString("claude mcp add nodered")
		// Fixed order keeps the command stable and testable.
		for _, k := range []string{"NODERED_URL", "NODERED_TOKEN", "NODERED_BACKUP_DIR"} {
			if v, ok := env[k]; ok {
				fmt.Fprintf(&b, " -e %s=%s", k, v)
			}
		}
		b.WriteString(" -- " + bin)
		return b.String()
	}

	rootKey := "mcpServers"
	if key == "vscode" {
		rootKey = "servers"
	}
	doc := map[string]any{
		rootKey: map[string]any{
			"nodered": map[string]any{"command": bin, "env": env},
		},
	}
	out, _ := json.MarshalIndent(doc, "", "  ") // map keys are sorted → deterministic
	return string(out)
}
