package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// updateChannel is the install path detected for the running binary.
type updateChannel int

const (
	channelNPM updateChannel = iota
	channelDocker
	channelBinary
)

func (c updateChannel) String() string {
	switch c {
	case channelNPM:
		return "npm"
	case channelDocker:
		return "docker"
	case channelBinary:
		return "binary"
	}
	return "unknown"
}

// execer runs a subprocess. Production wires this to exec.CommandContext;
// tests inject a fake. Stdout/stderr are inherited so the user sees npm's
// progress live.
type execer interface {
	Run(ctx context.Context, name string, args ...string) error
}

type realExecer struct{}

func (realExecer) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

var defaultExecer execer = realExecer{}

// osExecutable and npmLatestFetcher are package-level seams so tests can
// swap them out without touching the real registry or the real
// os.Executable. Production code reads them via osExecutable() and
// npmLatestFetcher().
var (
	osExecutable     = os.Executable
	npmLatestFetcher = fetchLatestNPMVersion
)

// runUpdate is the entry point for `nodered-mcp update`. Flags match the
// user-facing shape in issue #11: --check exits 0/1 silently, --yes skips
// the confirmation prompt.
func runUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	check := fs.Bool("check", false, "exit 0 if a newer version exists, 1 otherwise; silent")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	current := resolveVersion()
	channel := detectChannel()

	switch channel {
	case channelDocker:
		fmt.Fprintln(os.Stderr, "Cannot update from inside a running container.")
		fmt.Fprintln(os.Stderr, "To update the image, run on the host:")
		fmt.Fprintln(os.Stderr, "  docker pull ghcr.io/fgjcarlos/nodered-mcp:latest")
		fmt.Fprintln(os.Stderr, "  docker compose up -d   # or however you started the container")
		return nil
	case channelBinary:
		fmt.Fprintln(os.Stderr, "Detected a standalone binary install. Re-run the install script from the README")
		fmt.Fprintln(os.Stderr, "to upgrade:")
		fmt.Fprintln(os.Stderr, "  curl -sSL https://raw.githubusercontent.com/fgjcarlos/nodered-mcp/main/install.sh | sh")
		return nil
	}

	// NPM channel.
	latest, err := npmLatestFetcher()
	if err != nil {
		return fmt.Errorf("querying npm registry: %w", err)
	}

	if *check {
		// Silent on stderr; print only the latest version on stdout when newer.
		if versionIsNewer(latest, current) {
			fmt.Println(latest)
			return nil
		}
		os.Exit(1)
	}

	fmt.Printf("Current version: %s\n", current)
	fmt.Printf("Latest version:  %s\n", latest)
	if !versionIsNewer(latest, current) {
		fmt.Println("You are on the latest version.")
		return nil
	}

	if !*yes && !confirm(os.Stdin, os.Stderr) {
		fmt.Println("Update cancelled.")
		return nil
	}

	fmt.Println("Updating ...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := defaultExecer.Run(ctx, "npm", "install", "-g", "@fgjcarlos/nodered-mcp@latest"); err != nil {
		return fmt.Errorf("npm install: %w", err)
	}
	fmt.Printf("Updated to %s.\n", latest)
	return nil
}

// detectChannel decides how the binary was installed. Order matters: a
// `package.json` neighbour wins over the docker check, so a developer
// running the binary inside a node-style install but with /.dockerenv
// still gets the npm path.
func detectChannel() updateChannel {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return channelDocker
	}
	if packageJSONNeighbour() {
		return channelNPM
	}
	return channelBinary
}

// packageJSONNeighbour returns true if a package.json whose `name` is
// `@fgjcarlos/nodered-mcp` sits next to the running binary, or one
// directory up. Covers both the npm global layout and a user who symlinked
// the binary from a project.
func packageJSONNeighbour() bool {
	exec, err := osExecutable()
	if err != nil || exec == "" {
		return false
	}
	dir := filepath.Dir(exec)
	for _, candidate := range []string{
		filepath.Join(dir, "package.json"),
		filepath.Join(dir, "..", "package.json"),
	} {
		if packageNameMatches(candidate) {
			return true
		}
	}
	return false
}

func packageNameMatches(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var doc struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return false
	}
	return doc.Name == "@fgjcarlos/nodered-mcp"
}

// fetchLatestNPMVersion hits the public registry. No auth, no rate-limit
// concerns at the per-user scale `update` operates at.
func fetchLatestNPMVersion() (string, error) {
	const url = "https://registry.npmjs.org/@fgjcarlos/nodered-mcp/latest"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var doc struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", err
	}
	if doc.Version == "" {
		return "", errors.New("registry response had no version field")
	}
	return doc.Version, nil
}

// confirm reads a single line from r and prints the prompt to w. Empty
// answer or anything starting with "n" or "N" is treated as no.
func confirm(r io.Reader, w io.Writer) bool {
	fmt.Fprint(w, "Run `npm install -g @fgjcarlos/nodered-mcp@latest`? [y/N] ")
	var line string
	fmt.Fscan(r, &line)
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}

// versionIsNewer reports whether a > b, treating both as semver-ish
// dot-separated numeric strings. Returns false on parse error:
//
//	compareVersions("0.5.9", "0.5.8") =  1
//	compareVersions("0.5.8", "0.5.9") = -1
//	compareVersions("0.5.10", "0.5.9") = 1
//	compareVersions("0.5.8", "0.5.8") = 0
func versionIsNewer(a, b string) bool {
	return compareVersions(a, b) > 0
}

func compareVersions(a, b string) int {
	pa := strings.Split(a, ".")
	pb := strings.Split(b, ".")
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var ai, bi int
		if i < len(pa) {
			ai = parseVersionPart(pa[i])
		}
		if i < len(pb) {
			bi = parseVersionPart(pb[i])
		}
		if ai != bi {
			if ai > bi {
				return 1
			}
			return -1
		}
	}
	return 0
}

// parseVersionPart takes the leading numeric prefix of a dot-separated
// component. Pre-release tags like "0.5.9-rc1" parse as "0.5.9" which is
// the conservative read: we don't pretend to know whether the operator
// wants to upgrade over a pre-release.
func parseVersionPart(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
