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
	// Trust boundary: `nodered-mcp update` shells out to npm with a
	// pinned, fixed argv to upgrade the package the binary itself is
	// shipped through. There is no operator-supplied data on the
	// command line — argv is the literal `npm install -g
	// @fgjcarlos/nodered-mcp@latest` constructed in runUpdate. This
	// is the documented update channel (see cmd/nodered-mcp/update.go
	// and SECURITY.md).
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
	check := fs.Bool("check", false, "exit non-zero if a newer version exists; print state to stderr (use --json for machine-readable)")
	jsonOut := fs.Bool("json", false, "with --check: emit a single JSON object on stdout instead of human-readable text")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	if *check {
		return runCheck(*jsonOut)
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
		fmt.Fprintln(os.Stderr, "Detected a stand-alone binary install (go install, or a manual build).")
		fmt.Fprintln(os.Stderr, "To upgrade, re-run the same go install command:")
		fmt.Fprintln(os.Stderr, "  go install github.com/fgjcarlos/nodered-mcp/cmd/nodered-mcp@latest")
		return nil
	}

	// NPM channel.
	latest, err := npmLatestFetcher()
	if err != nil {
		return fmt.Errorf("querying npm registry: %w", err)
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

// Exit codes for `nodered-mcp update --check`. The values are spaced
// far enough apart that scripts can branch on them and CI never
// collapses them into "the binary crashed" (1). The numbers are
// part of the public contract (#228); do not renumber without
// updating the docs and the focused tests in update_test.go.
const (
	checkExitCurrent         = 0
	checkExitUpdateAvailable = 10
	checkExitUnsupported     = 20
	checkExitError           = 30
)

// checkState names the outcome of `update --check`. The strings
// appear in the JSON output and are stable; scripts can branch on
// the integer exit code OR the string. Both are documented.
type checkState string

const (
	stateCurrent         checkState = "current"
	stateUpdateAvailable checkState = "update-available"
	stateUnsupported     checkState = "unsupported"
	stateError           checkState = "error"
)

// checkResult is the structured payload emitted by --check --json.
// It is the canonical wire format that automation reads; the
// human-readable text path mirrors the same fields but in prose.
type checkResult struct {
	State          checkState `json:"state"`
	Channel        string     `json:"channel"`
	CurrentVersion string     `json:"current_version"`
	LatestVersion  string     `json:"latest_version,omitempty"`
	Message        string     `json:"message,omitempty"`
}

// runCheck is the entry point for `nodered-mcp update --check`. It
// classifies the running binary's channel, asks the appropriate
// upstream (only NPM today — docker and binary are unsupported),
// and emits the outcome as a structured checkResult plus a
// deterministic exit code. The shape is the contract documented
// in #228.
//
//	jsonOut=false → human-readable text on stderr, exit code per state.
//	jsonOut=true  → single JSON object on stdout (single line, stable
//	                schema), same exit code. Nothing else is written
//	                to stdout in --json mode so scripts can pipe it.
func runCheck(jsonOut bool) error {
	res := checkCheck(jsonOut)
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		if err := enc.Encode(res); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stderr, "update check: %s\n", renderCheckHuman(res))
	}
	switch res.State {
	case stateCurrent:
		return exitWith(checkExitCurrent)
	case stateUpdateAvailable:
		return exitWith(checkExitUpdateAvailable)
	case stateUnsupported:
		return exitWith(checkExitUnsupported)
	default: // stateError
		return exitWith(checkExitError)
	}
}

// renderCheckHuman renders a checkResult as a one-line human-
// readable summary for stderr. Pure function so tests can assert
// on it directly without swapping any global seams.
func renderCheckHuman(r checkResult) string {
	switch r.State {
	case stateCurrent:
		if r.LatestVersion != "" {
			return fmt.Sprintf("current (channel=%s, version=%s, latest=%s)", r.Channel, r.CurrentVersion, r.LatestVersion)
		}
		return fmt.Sprintf("current (channel=%s, version=%s)", r.Channel, r.CurrentVersion)
	case stateUpdateAvailable:
		return fmt.Sprintf("update-available (channel=%s, current=%s, latest=%s)", r.Channel, r.CurrentVersion, r.LatestVersion)
	case stateUnsupported:
		return fmt.Sprintf("unsupported (channel=%s, current=%s): %s", r.Channel, r.CurrentVersion, r.Message)
	default: // stateError
		return fmt.Sprintf("error (channel=%s, current=%s): %s", r.Channel, r.CurrentVersion, r.Message)
	}
}

// exitWith terminates the process with the given code. Pulled out
// as a package-level seam so tests can swap it for a recorder;
// production wires os.Exit. Tests verify the chosen exit code
// without the test process actually dying.
var exitWith = func(code int) error {
	os.Exit(code)
	return nil
}

// checkCheck runs the actual classification. Pulled out of
// runCheck so the seam exitWith is the only thing tests need to
// swap — the rest of the function is pure and inspectable.
func checkCheck(jsonOut bool) checkResult {
	current := resolveVersion()
	channel := detectChannel()

	switch channel {
	case channelDocker:
		return checkResult{
			State:          stateUnsupported,
			Channel:        "docker",
			CurrentVersion: current,
			Message:        "auto-update is not supported inside a running container; pull the image on the host instead",
		}
	case channelBinary:
		return checkResult{
			State:          stateUnsupported,
			Channel:        "binary",
			CurrentVersion: current,
			Message:        "auto-update is not supported for stand-alone binary installs; re-run the original go install command",
		}
	}

	latest, err := npmLatestFetcher()
	if err != nil {
		// Error path: the check did NOT succeed. Surface the
		// failure to the user with the state=error contract
		// and exit code 30. Critically, do NOT print
		// "You are on the latest version." here — the
		// acceptance criterion is "Output does not claim
		// success when the check cannot be completed".
		return checkResult{
			State:          stateError,
			Channel:        "npm",
			CurrentVersion: current,
			Message:        "could not reach the npm registry: " + err.Error(),
		}
	}

	if versionIsNewer(latest, current) {
		return checkResult{
			State:          stateUpdateAvailable,
			Channel:        "npm",
			CurrentVersion: current,
			LatestVersion:  latest,
			Message:        "a newer version is available",
		}
	}
	return checkResult{
		State:          stateCurrent,
		Channel:        "npm",
		CurrentVersion: current,
		LatestVersion:  latest,
	}
}

// detectChannel decides how the binary was installed. Order matters: a
// `package.json` neighbour wins over the docker check, so a developer
// running the binary inside a node-style install but with /.dockerenv
// still gets the npm path.
//
// detectChannel is a package-level variable (not a function) so tests
// can swap the implementation. Production callers go through the
// function-form detectChannelImpl so the call site reads the same as
// a free function.
var detectChannel = detectChannelImpl

func detectChannelImpl() updateChannel {
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
	// Accept the meta package itself and any of its scoped platform
	// packages (@fgjcarlos/nodered-mcp-<plat>-<arch>). With #257 the
	// binary ships inside one of the six platform packages, so the
	// neighbouring package.json is the platform manifest, not the
	// root one — matching only the meta name silently misroutes
	// `update` to the binary channel.
	const metaPrefix = "@fgjcarlos/nodered-mcp"
	return doc.Name == metaPrefix || strings.HasPrefix(doc.Name, metaPrefix+"-")
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
// answer or anything starting with "n" or "N" is treated as no. A read
// failure (EOF, closed stdin, scanner error) is also treated as no so
// that npm is never launched without an explicit operator "y".
func confirm(r io.Reader, w io.Writer) bool {
	fmt.Fprint(w, "Run `npm install -g @fgjcarlos/nodered-mcp@latest`? [y/N] ")
	var line string
	if _, err := fmt.Fscan(r, &line); err != nil {
		// EOF, closed pipe, or scanner failure: stay fail-closed.
		return false
	}
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
