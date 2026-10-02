package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type mcpClient struct {
	key, name string
	commands  []string // CLI executables that establish installation evidence.
	probes    []string // Explicit config files or platform application markers.
	note      string
	format    string // Non-standard renderer; empty uses the rootKey JSON shape.
	writePath string // Empty when safe automatic merging is unsupported.
	rootKey   string
	example   string
}

func knownClients() []mcpClient {
	home, _ := os.UserHomeDir()
	cfg, _ := os.UserConfigDir()
	return clientCatalog(runtime.GOOS, home, cfg, os.Getenv("APPDATA"), os.Getenv("LOCALAPPDATA"))
}

func clientCatalog(goos, home, cfg, appData, localAppData string) []mcpClient {
	claudeDesktopConfig := filepath.Join(cfg, "Claude", "claude_desktop_config.json")
	if goos == "windows" && appData != "" {
		claudeDesktopConfig = filepath.Join(appData, "Claude", "claude_desktop_config.json")
	}
	claudeDesktop := []string{claudeDesktopConfig}
	cursor := []string{filepath.Join(home, ".cursor", "mcp.json")}
	var vscode []string
	if goos == "windows" {
		claudeDesktop = append(claudeDesktop, filepath.Join(localAppData, "Programs", "Claude", "Claude.exe"))
		cursor = append(cursor, filepath.Join(localAppData, "Programs", "Cursor", "Cursor.exe"))
		vscode = []string{
			filepath.Join(appData, "Code", "User", "mcp.json"),
			filepath.Join(localAppData, "Programs", "Microsoft VS Code", "Code.exe"),
		}
	} else if goos == "darwin" {
		claudeDesktop = append(claudeDesktop, "/Applications/Claude.app", filepath.Join(home, "Applications", "Claude.app"))
		cursor = append(cursor, "/Applications/Cursor.app", filepath.Join(home, "Applications", "Cursor.app"))
		vscode = append(vscode, "/Applications/Visual Studio Code.app", filepath.Join(home, "Applications", "Visual Studio Code.app"))
	} else {
		vscode = append(vscode,
			filepath.Join(cfg, "Code", "User", "mcp.json"),
			"/usr/share/applications/code.desktop",
			filepath.Join(home, ".local", "share", "applications", "code.desktop"),
		)
		claudeDesktop = append(claudeDesktop, "/usr/share/applications/claude-desktop.desktop", filepath.Join(home, ".local", "share", "applications", "claude-desktop.desktop"))
		cursor = append(cursor, "/usr/share/applications/cursor.desktop", filepath.Join(home, ".local", "share", "applications", "cursor.desktop"))
	}

	return []mcpClient{
		{
			key: "claude-desktop", name: "Claude Desktop", probes: claudeDesktop,
			note:      "paste into " + claudeDesktopConfig,
			writePath: claudeDesktopConfig, rootKey: "mcpServers",
			example: "examples/claude_desktop_config.json",
		},
		{key: "claude-code", name: "Claude Code", commands: []string{"claude"}, note: "run the command below", format: "claude-code"},
		{
			key: "cursor", name: "Cursor", commands: []string{"cursor"}, probes: cursor,
			note:      "paste into .cursor/mcp.json (workspace) or ~/.cursor/mcp.json (global)",
			writePath: filepath.Join(home, ".cursor", "mcp.json"),
			rootKey:   "mcpServers", example: "examples/cursor_mcp.json",
		},
		{
			key: "vscode", name: "VS Code", commands: []string{"code"}, probes: vscode,
			note: "paste into .vscode/mcp.json", rootKey: "servers",
			example: "examples/vscode_mcp.json",
		},
		{
			key: "gemini", name: "Gemini CLI", commands: []string{"gemini"},
			note:      "paste into " + filepath.Join(home, ".gemini", "settings.json"),
			writePath: filepath.Join(home, ".gemini", "settings.json"),
			rootKey:   "mcpServers", example: "examples/gemini_settings.json",
		},
		{
			key: "opencode", name: "OpenCode", commands: []string{"opencode"},
			note:   "paste into " + filepath.Join(cfg, "opencode", "opencode.json"),
			format: "opencode", rootKey: "mcp", example: "examples/opencode_config.json",
		},
		{
			key: "pi", name: "Pi", commands: []string{"pi"},
			note:   "paste into " + filepath.Join(home, ".pi", "agent", "mcp.json"),
			format: "pi", rootKey: "mcpServers", example: "examples/pi_mcp_config.json",
		},
	}
}

var detectClients = detectClientsImpl

func detectClientsImpl(all bool) []mcpClient {
	var out []mcpClient
	for _, c := range knownClients() {
		if all || clientInstalled(c) {
			out = append(out, c)
		}
	}
	return out
}

func clientInstalled(c mcpClient) bool {
	for _, command := range c.commands {
		if _, err := exec.LookPath(command); err == nil {
			return true
		}
	}
	for _, path := range c.probes {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	all := fs.Bool("all", false, "show every known client, not just detected ones")
	write := fs.Bool("write", false, "write config without a token (configure NODERED_TOKEN outside the file)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	clients := detectClients(*all)
	if len(clients) == 0 {
		return fmt.Errorf("no supported MCP client detected; install one or rerun with --all to generate a config manually")
	}

	bin := executablePath()
	in := bufio.NewScanner(os.Stdin)
	url := ask(in, "Node-RED URL", "http://localhost:1880")
	token := ask(in, "Node-RED token (optional, Enter to skip)", "")
	backupDir := ask(in, "Backup directory", "backups")

	target := chooseClient(in, clients)
	env := buildEnv(url, token, backupDir)

	if *write {
		return writeClientConfig(target, bin, env, url, token, backupDir)
	}

	fmt.Fprintf(os.Stderr, "\n--- %s: %s ---\n", target.name, target.note)
	fmt.Println(renderConfig(target.key, bin, url, token, backupDir))
	if token != "" {
		printTokenOmittedNote()
	}
	return nil
}

// executablePath returns the path to write into the generated MCP config.
// Keep the invoked symlink path when available; os.Executable resolves it on
// Linux and would otherwise store a version-specific package-manager target.
// If the invocation name cannot be resolved, use the stable command name, which
// requires nodered-mcp to be on the client's PATH.
func executablePath() string {
	bin, err := os.Executable()
	if err != nil || bin == "" {
		bin = "nodered-mcp"
	}
	return executablePathFor(os.Args[0], bin)
}

func executablePathFor(invoked, fallback string) string {
	if invoked == "" {
		return fallback
	}
	path := invoked
	if filepath.Base(path) == path {
		var err error
		path, err = exec.LookPath(path)
		if err != nil {
			return "nodered-mcp"
		}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return fallback
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return path
	}
	return fallback
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
		if token != "" {
			printTokenOmittedNote()
		}
		fmt.Fprintf(os.Stderr, "\n(--write isn't supported for %s — paste/run the above)\n", c.name)
		return fmt.Errorf("--write is not supported for %s: configuration was not applied", c.name)
	}
	if err := mergeServerIntoFile(path, rootKey, bin, env); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "✓ wrote 'nodered' server to %s\n", path)
	if _, ok := env["NODERED_TOKEN"]; ok {
		printTokenOmittedNote()
	}
	fmt.Fprintln(os.Stderr, "  (previous file saved as .bak) — restart the client to load it.")
	return nil
}

func printTokenOmittedNote() {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "# NODERED_TOKEN was omitted to avoid persisting a secret.")
	fmt.Fprintln(os.Stderr, "# Set it in the environment, OS keychain, or secret manager before starting the client.")
}

// writableTarget uses the catalog's safe global write target, if one exists.
func writableTarget(key string) (path, rootKey string, ok bool) {
	for _, client := range knownClients() {
		if client.key == key && client.writePath != "" {
			return client.writePath, client.rootKey, true
		}
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
	var servers map[string]any
	value, exists := root[rootKey]
	if !exists {
		servers = map[string]any{}
	} else {
		var ok bool
		servers, ok = value.(map[string]any)
		if !ok {
			return fmt.Errorf("existing config at %s has non-object %q; refusing to overwrite it", path, rootKey)
		}
	}
	servers["nodered"] = map[string]any{"command": bin, "env": envWithoutToken(env)}
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
//
// Safety invariants enforced here (issue #70):
//   - The parent directory is created with owner-only permissions.
//   - A failed backup of the existing config is an error: we never
//     silently overwrite the user's config when a backup could not be
//     taken.
//   - The new file is written atomically via atomicWriteFile: a
//     partially-written config cannot replace a valid one if the
//     process is killed mid-write.
//   - The final file is owner-only (0o600).
func writeJSONObject(path string, m map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if data, err := os.ReadFile(path); err == nil {
		if err := atomicWriteFile(path+".bak", data, 0o600); err != nil {
			return fmt.Errorf("backup existing config to %q: %w", path+".bak", err)
		}
	}
	out, err := marshalIndentedJSON(m)
	if err != nil {
		return err
	}
	return atomicWriteFile(path, out, 0o600)
}

// atomicWriteFile writes data to path atomically: it stages a
// sibling temp file in the same directory, fsyncs it, and renames
// it over the target. A failure at any step leaves the original
// file untouched (or removes the staged temp on rename failure).
//
// Same-directory staging guarantees the rename is atomic on the
// same filesystem (POSIX rename(2)). The temp file is created with
// 0o600 so a process death between CreateTemp and rename cannot
// leave a world-readable copy on disk.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	_ = os.Chmod(path, perm)
	return nil
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

func envWithoutToken(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for key, value := range env {
		if key == "NODERED_TOKEN" {
			continue
		}
		out[key] = value
	}
	return out
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func marshalIndentedJSON(value any) ([]byte, error) {
	var out strings.Builder
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return []byte(out.String()), nil
}

func renderConfig(key, bin, url, token, backupDir string) string {
	env := envWithoutToken(buildEnv(url, token, backupDir))
	client := clientByKey(key)

	if client.format == "claude-code" {
		var b strings.Builder
		// User scope avoids binding the server to the working directory.
		b.WriteString("claude mcp add -s user nodered")
		for _, k := range []string{"NODERED_URL", "NODERED_TOKEN", "NODERED_BACKUP_DIR"} {
			if v, ok := env[k]; ok {
				fmt.Fprintf(&b, " -e %s", shellQuote(k+"="+v))
			}
		}
		b.WriteString(" -- " + shellQuote(bin))
		return b.String()
	}

	server := map[string]any{"command": bin, "env": env}
	var doc map[string]any
	switch client.format {
	case "opencode":
		doc = map[string]any{
			"$schema": "https://opencode.ai/config.json",
			"mcp": map[string]any{"nodered": map[string]any{
				"type": "local", "command": []string{bin}, "enabled": true, "environment": env,
			}},
		}
	default:
		if client.format == "pi" {
			server["lifecycle"] = "keep-alive"
		}
		rootKey := client.rootKey
		if rootKey == "" {
			rootKey = "mcpServers"
		}
		doc = map[string]any{rootKey: map[string]any{"nodered": server}}
	}
	out, _ := marshalIndentedJSON(doc)
	return strings.TrimSuffix(string(out), "\n")
}

func clientByKey(key string) mcpClient {
	for _, client := range knownClients() {
		if client.key == key {
			return client
		}
	}
	return mcpClient{}
}
