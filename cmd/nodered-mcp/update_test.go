package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareVersions_TableDriven(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.5.9", "0.5.8", 1},
		{"0.5.8", "0.5.9", -1},
		{"0.5.8", "0.5.8", 0},
		{"0.6.0", "0.5.9", 1},
		{"0.5.10", "0.5.9", 1},
		{"1.0.0", "0.99.99", 1},
		{"dev", "0.5.8", -1}, // missing parts pad to 0; "0" beats "" but loses to 5.
		{"0.5.9-rc1", "0.5.8", 1},
	}
	for _, c := range cases {
		got := compareVersions(c.a, c.b)
		if got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestVersionIsNewer(t *testing.T) {
	if !versionIsNewer("0.5.9", "0.5.8") {
		t.Error("0.5.9 should be newer than 0.5.8")
	}
	if versionIsNewer("0.5.8", "0.5.9") {
		t.Error("0.5.8 should not be newer than 0.5.9")
	}
	if versionIsNewer("0.5.8", "0.5.8") {
		t.Error("0.5.8 should not be newer than 0.5.8")
	}
}

func TestParseVersionPart(t *testing.T) {
	cases := map[string]int{
		"":      0,
		"0":     0,
		"5":     5,
		"10":    10,
		"9-rc1": 9,
		"foo":   0,
		"5.2":   5,
	}
	for in, want := range cases {
		if got := parseVersionPart(in); got != want {
			t.Errorf("parseVersionPart(%q) = %d, want %d", in, got, want)
		}
	}
}

// fakeExecer records the command it was asked to run.
type fakeExecer struct {
	called bool
	name   string
	args   []string
	err    error
}

func (f *fakeExecer) Run(ctx context.Context, name string, args ...string) error {
	f.called = true
	f.name = name
	f.args = args
	return f.err
}

func TestFetchLatestNPMVersion_RegistryBody(t *testing.T) {
	// Pin the contract that fetchLatestNPMVersion expects from the
	// registry response: a top-level `version` string field.
	body := []byte(`{"version":"9.9.9"}`)
	var doc struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Version != "9.9.9" {
		t.Errorf("version = %q", doc.Version)
	}
}

func TestPackageJSONNeighbour_NPM(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "package.json")
	if err := os.WriteFile(pkg, []byte(`{"name":"@fgjcarlos/nodered-mcp"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	origExecutable := osExecutable
	osExecutable = func() (string, error) { return filepath.Join(dir, "nodered-mcp"), nil }
	defer func() { osExecutable = origExecutable }()

	if !packageJSONNeighbour() {
		t.Error("expected npm channel: package.json neighbour found")
	}
}

func TestPackageJSONNeighbour_ParentDir(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "bin")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(dir, "package.json")
	if err := os.WriteFile(pkg, []byte(`{"name":"@fgjcarlos/nodered-mcp"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	origExecutable := osExecutable
	osExecutable = func() (string, error) { return filepath.Join(subdir, "nodered-mcp"), nil }
	defer func() { osExecutable = origExecutable }()

	if !packageJSONNeighbour() {
		t.Error("expected npm channel: package.json in parent dir")
	}
}

func TestPackageJSONNeighbour_WrongName(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "package.json")
	if err := os.WriteFile(pkg, []byte(`{"name":"some-other-package"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	origExecutable := osExecutable
	osExecutable = func() (string, error) { return filepath.Join(dir, "nodered-mcp"), nil }
	defer func() { osExecutable = origExecutable }()

	if packageJSONNeighbour() {
		t.Error("did not expect npm channel: package name does not match")
	}
}

func TestPackageJSONNeighbour_Missing(t *testing.T) {
	dir := t.TempDir()

	origExecutable := osExecutable
	osExecutable = func() (string, error) { return filepath.Join(dir, "nodered-mcp"), nil }
	defer func() { osExecutable = origExecutable }()

	if packageJSONNeighbour() {
		t.Error("did not expect npm channel: no package.json")
	}
}

func TestDetectChannel_DockerSkipped(t *testing.T) {
	// /.dockerenv is the canonical Docker marker. We can't create it
	// (root-only mount), but if it is present we are already inside a
	// container — skip the negative case.
	if detectChannel() == channelDocker {
		t.Skip("running inside a container")
	}
}

func TestConfirm_Yes(t *testing.T) {
	r := strings.NewReader("y\n")
	w := &strings.Builder{}
	if !confirm(r, w) {
		t.Error("expected 'y' to confirm")
	}
}

func TestConfirm_No(t *testing.T) {
	for _, in := range []string{"n\n", "\n", "no\n", "garbage\n"} {
		if confirm(strings.NewReader(in), io.Discard) {
			t.Errorf("expected %q to NOT confirm", in)
		}
	}
}

func TestConfirm_YesLong(t *testing.T) {
	if !confirm(strings.NewReader("yes\n"), io.Discard) {
		t.Error("expected 'yes' to confirm")
	}
}

// TestConfirm_FailClosedOnReadError is the regression guard for issue
// #71: a closed stdin, EOF, or scanner failure must NOT launch npm.
// Before the fix, fmt.Fscan returned err but it was discarded, leaving
// `line` as the empty string — which the no-match branch already
// handled by accident. The test pins the contract explicitly so the
// behaviour cannot drift back to "ignore scan errors".
func TestConfirm_FailClosedOnReadError(t *testing.T) {
	cases := []struct {
		name string
		r    io.Reader
	}{
		{"closed reader returns EOF", strings.NewReader("")},
		{"broken reader", errReader{}},
		{"empty input", strings.NewReader("")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if confirm(tc.r, io.Discard) {
				t.Errorf("expected confirm to return false on %s", tc.name)
			}
		})
	}
}

type errReader struct{}

func (errReader) Read(_ []byte) (int, error) {
	return 0, fmt.Errorf("simulated read failure")
}

// withUpdateStubs swaps the package-level seams for the duration of t and
// restores them via t.Cleanup. Centralises the boilerplate so individual
// tests can stay focused on the assertion.
func withUpdateStubs(t *testing.T, execPath string, latest string, fake execer) {
	t.Helper()
	origExecutable := osExecutable
	osExecutable = func() (string, error) { return execPath, nil }
	t.Cleanup(func() { osExecutable = origExecutable })

	origFetcher := npmLatestFetcher
	npmLatestFetcher = func() (string, error) { return latest, nil }
	t.Cleanup(func() { npmLatestFetcher = origFetcher })

	origExec := defaultExecer
	defaultExecer = fake
	t.Cleanup(func() { defaultExecer = origExec })

	origVersion := version
	version = "0.5.8"
	t.Cleanup(func() { version = origVersion })
}

func TestRunUpdate_NPMChannel_HappyPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"@fgjcarlos/nodered-mcp"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &fakeExecer{}
	withUpdateStubs(t, filepath.Join(dir, "nodered-mcp"), "9.9.9", fake)

	if err := runUpdate([]string{"--yes"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if !fake.called {
		t.Fatal("expected npm install to be invoked")
	}
	if fake.name != "npm" {
		t.Errorf("expected `npm` to be invoked, got %q", fake.name)
	}
	wantArgs := []string{"install", "-g", "@fgjcarlos/nodered-mcp@latest"}
	if len(fake.args) != len(wantArgs) {
		t.Fatalf("expected args %v, got %v", wantArgs, fake.args)
	}
	for i, a := range wantArgs {
		if fake.args[i] != a {
			t.Errorf("args[%d] = %q, want %q", i, fake.args[i], a)
		}
	}
}

func TestRunUpdate_NPMChannel_UpToDate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"@fgjcarlos/nodered-mcp"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &fakeExecer{}
	withUpdateStubs(t, filepath.Join(dir, "nodered-mcp"), "0.5.8", fake)

	if err := runUpdate([]string{"--yes"}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if fake.called {
		t.Error("did not expect npm install on up-to-date version")
	}
}

func TestRunUpdate_NPMChannel_NpmFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"@fgjcarlos/nodered-mcp"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &fakeExecer{err: io.EOF}
	withUpdateStubs(t, filepath.Join(dir, "nodered-mcp"), "9.9.9", fake)

	if err := runUpdate([]string{"--yes"}); err == nil {
		t.Fatal("expected npm failure to surface as error")
	}
}

func TestRunUpdate_NPMChannel_ConfirmDeclined(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"@fgjcarlos/nodered-mcp"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &fakeExecer{}
	withUpdateStubs(t, filepath.Join(dir, "nodered-mcp"), "9.9.9", fake)

	// Stdin says "n"; no --yes flag.
	r, w, _ := os.Pipe()
	origStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = origStdin })
	go func() {
		_, _ = w.Write([]byte("n\n"))
		_ = w.Close()
	}()

	if err := runUpdate(nil); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if fake.called {
		t.Error("did not expect npm install when user declined")
	}
}

func TestRunUpdate_NPMChannel_ConfirmAccepted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"@fgjcarlos/nodered-mcp"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &fakeExecer{}
	withUpdateStubs(t, filepath.Join(dir, "nodered-mcp"), "9.9.9", fake)

	r, w, _ := os.Pipe()
	origStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = origStdin })
	go func() {
		_, _ = w.Write([]byte("yes\n"))
		_ = w.Close()
	}()

	if err := runUpdate(nil); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if !fake.called {
		t.Error("expected npm install after user confirmed")
	}
}

// TestUpdateChannel_String covers the Stringer for all three constants
// plus the zero/unknown default. Cheap table-driven test, no fixtures.
func TestUpdateChannel_String(t *testing.T) {
	cases := map[updateChannel]string{
		channelNPM:    "npm",
		channelDocker: "docker",
		channelBinary: "binary",
		// Any unrecognised value should fall through to "unknown".
		updateChannel(99): "unknown",
	}
	for in, want := range cases {
		if got := in.String(); got != want {
			t.Errorf("updateChannel(%d).String() = %q, want %q", in, got, want)
		}
	}
}

// TestRealExecer_Run exercises the production execer wrapper without
// actually invoking a subprocess. A non-existent command on PATH is
// the smallest check that fails the same way a real `npm` failure
// would (non-zero exit / ENOENT), so we avoid a network or filesystem
// dependency.
func TestRealExecer_Run(t *testing.T) {
	err := (realExecer{}).Run(context.Background(), "this-binary-does-not-exist-xyz", "arg1")
	if err == nil {
		t.Error("realExecer.Run should fail when the binary is missing")
	}
}

// TestRunUpdate_DockerChannel confirms the docker early-return path
// prints the pull hint and exits nil without touching npm.
func TestRunUpdate_DockerChannel(t *testing.T) {
	out := captureStderr(t, func() {
		orig := detectChannel
		detectChannel = func() updateChannel { return channelDocker }
		t.Cleanup(func() { detectChannel = orig })

		if err := runUpdate(nil); err != nil {
			t.Errorf("runUpdate should return nil on docker channel; got %v", err)
		}
	})
	if !strings.Contains(out, "docker pull ghcr.io/fgjcarlos/nodered-mcp:latest") {
		t.Errorf("docker channel hint missing pull command; got:\n%s", out)
	}
}

// TestRunUpdate_BinaryChannel confirms the standalone-binary early-return
// path prints the go install hint and exits nil without touching npm.
//
// After #193 the shell install scripts are retired; the binary channel
// now tells the user to re-run `go install`. If this assertion breaks,
// the hint in update.go has drifted from the supported upgrade command.
func TestRunUpdate_BinaryChannel(t *testing.T) {
	out := captureStderr(t, func() {
		orig := detectChannel
		detectChannel = func() updateChannel { return channelBinary }
		t.Cleanup(func() { detectChannel = orig })

		if err := runUpdate(nil); err != nil {
			t.Errorf("runUpdate should return nil on binary channel; got %v", err)
		}
	})
	want := "go install github.com/fgjcarlos/nodered-mcp/cmd/nodered-mcp@latest"
	if !strings.Contains(out, want) {
		t.Errorf("binary channel hint missing %q; got:\n%s", want, out)
	}
	// Belt-and-suspenders: the shell install scripts are gone, so no
	// hint should ever point at them again.
	for _, banned := range []string{"install.sh", "install.ps1", "raw.githubusercontent.com"} {
		if strings.Contains(out, banned) {
			t.Errorf("binary channel hint must not reference %q; got:\n%s", banned, out)
		}
	}
}

// captureStderr swaps os.Stderr for a buffer for the duration of fn,
// restoring it on return. Returns whatever the wrapped code wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = orig })

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()
	_ = w.Close()
	return <-done
}

// withExitCapture swaps exitWith for a recorder so tests can assert
// on the chosen exit code without killing the test process. Returns
// the slice that will be appended to (caller's t.Cleanup restores
// the original exitWith after reading the captured codes).
func withExitCapture(t *testing.T) *[]int {
	t.Helper()
	var captured []int
	orig := exitWith
	exitWith = func(code int) error {
		captured = append(captured, code)
		return nil
	}
	t.Cleanup(func() { exitWith = orig })
	return &captured
}

// withVersion pins the package-level `version` variable for the
// duration of the test so checkCheck's "current == latest" path is
// deterministic. The value the tests inject is the canonical
// "running binary version" checkCheck compares against `latest`.
func withVersion(t *testing.T, v string) {
	t.Helper()
	origVersion := version
	version = v
	t.Cleanup(func() { version = origVersion })
}

// TestCheck_NPM_Current: the "we are on the latest" path. Exit 0
// is the headline behaviour: scripts branch on "did the version
// check pass?" with success meaning "either current or update-
// available", and current is the no-action case.
func TestCheck_NPM_Current(t *testing.T) {
	withVersion(t, "0.5.8")
	origDetect := detectChannel
	detectChannel = func() updateChannel { return channelNPM }
	t.Cleanup(func() { detectChannel = origDetect })
	origFetch := npmLatestFetcher
	npmLatestFetcher = func() (string, error) { return "0.5.8", nil }
	t.Cleanup(func() { npmLatestFetcher = origFetch })

	captured := withExitCapture(t)

	captureStderr(t, func() {
		if err := runCheck(false); err != nil {
			t.Fatalf("runCheck: %v", err)
		}
	})
	if len(*captured) != 1 || (*captured)[0] != checkExitCurrent {
		t.Errorf("exit code = %v, want [%d]", *captured, checkExitCurrent)
	}
}

// TestCheck_NPM_UpdateAvailable: the "newer version exists" path.
// Exit 10 is the script-branchable signal: CI scripts can use the
// exit code directly without parsing text.
func TestCheck_NPM_UpdateAvailable(t *testing.T) {
	withVersion(t, "0.5.8")
	origDetect := detectChannel
	detectChannel = func() updateChannel { return channelNPM }
	t.Cleanup(func() { detectChannel = origDetect })
	origFetch := npmLatestFetcher
	npmLatestFetcher = func() (string, error) { return "9.9.9", nil }
	t.Cleanup(func() { npmLatestFetcher = origFetch })

	captured := withExitCapture(t)

	captureStderr(t, func() {
		if err := runCheck(false); err != nil {
			t.Fatalf("runCheck: %v", err)
		}
	})
	if len(*captured) != 1 || (*captured)[0] != checkExitUpdateAvailable {
		t.Errorf("exit code = %v, want [%d]", *captured, checkExitUpdateAvailable)
	}
}

// TestCheck_Docker_Unsupported: the docker channel cannot auto-
// update. Exit 20, NOT exit 0. The acceptance criterion "Output
// does not claim success when the check cannot be completed"
// applies here too: an exit 0 would tell scripts "you're good"
// when the user actually has to pull the image on the host.
func TestCheck_Docker_Unsupported(t *testing.T) {
	withVersion(t, "0.5.8")
	origDetect := detectChannel
	detectChannel = func() updateChannel { return channelDocker }
	t.Cleanup(func() { detectChannel = origDetect })

	captured := withExitCapture(t)

	out := captureStderr(t, func() {
		if err := runCheck(false); err != nil {
			t.Fatalf("runCheck: %v", err)
		}
	})
	if len(*captured) != 1 || (*captured)[0] != checkExitUnsupported {
		t.Errorf("exit code = %v, want [%d]", *captured, checkExitUnsupported)
	}
	if !strings.Contains(out, "unsupported") {
		t.Errorf("expected 'unsupported' label in output; got:\n%s", out)
	}
	if !strings.Contains(out, "docker") {
		t.Errorf("expected 'docker' channel in output; got:\n%s", out)
	}
}

// TestCheck_Binary_Unsupported: same contract as docker, different
// exit code path. Exit 20 again because the contract is by state,
// not by channel: any channel that cannot auto-update is exit 20.
func TestCheck_Binary_Unsupported(t *testing.T) {
	withVersion(t, "0.5.8")
	origDetect := detectChannel
	detectChannel = func() updateChannel { return channelBinary }
	t.Cleanup(func() { detectChannel = origDetect })

	captured := withExitCapture(t)

	out := captureStderr(t, func() {
		if err := runCheck(false); err != nil {
			t.Fatalf("runCheck: %v", err)
		}
	})
	if len(*captured) != 1 || (*captured)[0] != checkExitUnsupported {
		t.Errorf("exit code = %v, want [%d]", *captured, checkExitUnsupported)
	}
	if !strings.Contains(out, "go install") {
		t.Errorf("expected 'go install' hint in unsupported output; got:\n%s", out)
	}
}

// TestCheck_NPM_RegistryError: the registry is unreachable. Exit
// 30, state "error", and the output MUST NOT claim success. This
// is the acceptance criterion: "Output does not claim success when
// the check cannot be completed".
func TestCheck_NPM_RegistryError(t *testing.T) {
	withVersion(t, "0.5.8")
	origDetect := detectChannel
	detectChannel = func() updateChannel { return channelNPM }
	t.Cleanup(func() { detectChannel = origDetect })
	origFetch := npmLatestFetcher
	npmLatestFetcher = func() (string, error) { return "", errors.New("connection refused") }
	t.Cleanup(func() { npmLatestFetcher = origFetch })

	captured := withExitCapture(t)

	out := captureStderr(t, func() {
		if err := runCheck(false); err != nil {
			t.Fatalf("runCheck: %v", err)
		}
	})
	if len(*captured) != 1 || (*captured)[0] != checkExitError {
		t.Errorf("exit code = %v, want [%d]", *captured, checkExitError)
	}
	// The error path must not claim success. We banned phrases
	// that would imply the check completed cleanly; the field
	// name "current=..." in the output is fine (it is the binary
	// version) because it appears after the "error" label, not
	// as a leading state.
	for _, banned := range []string{"You are on the latest version", "update-available", "update check: current"} {
		if strings.Contains(out, banned) {
			t.Errorf("error path must not claim success via %q; got:\n%s", banned, out)
		}
	}
	if !strings.Contains(out, "connection refused") {
		t.Errorf("error path must surface the registry error message; got:\n%s", out)
	}
	if !strings.Contains(out, "error (") {
		t.Errorf("error path must label the state explicitly; got:\n%s", out)
	}
}

// TestCheck_JSON_Shape: with --json the wire format is a single
// JSON object on stdout with the documented schema. No text on
// stderr, no extra newlines, the schema is stable.
func TestCheck_JSON_Shape(t *testing.T) {
	withVersion(t, "0.5.8")
	origDetect := detectChannel
	detectChannel = func() updateChannel { return channelNPM }
	t.Cleanup(func() { detectChannel = origDetect })
	origFetch := npmLatestFetcher
	npmLatestFetcher = func() (string, error) { return "9.9.9", nil }
	t.Cleanup(func() { npmLatestFetcher = origFetch })

	captured := withExitCapture(t)

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = stdoutW
	t.Cleanup(func() { os.Stdout = origStdout })

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, stdoutR)
		done <- buf.String()
	}()

	captureStderr(t, func() {
		if err := runCheck(true); err != nil {
			t.Fatalf("runCheck: %v", err)
		}
	})

	_ = stdoutW.Close()
	out := <-done

	if len(*captured) != 1 || (*captured)[0] != checkExitUpdateAvailable {
		t.Errorf("exit code = %v, want [%d]", *captured, checkExitUpdateAvailable)
	}

	// Parse the JSON, then assert on the schema. We do NOT use
	// string comparison because field order in the encoded JSON
	// is non-significant for downstream parsers.
	var got checkResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("JSON parse: %v; raw=%q", err, out)
	}
	if got.State != stateUpdateAvailable {
		t.Errorf("state = %q, want %q", got.State, stateUpdateAvailable)
	}
	if got.Channel != "npm" {
		t.Errorf("channel = %q, want npm", got.Channel)
	}
	if got.CurrentVersion == "" {
		t.Errorf("current_version empty; want the running binary version")
	}
	if got.LatestVersion != "9.9.9" {
		t.Errorf("latest_version = %q, want 9.9.9", got.LatestVersion)
	}
}

// TestRenderCheckHuman: pin the human-readable output format so
// downstream docs can copy it without re-deriving the wording.
func TestRenderCheckHuman(t *testing.T) {
	cases := []struct {
		name string
		r    checkResult
		want string
	}{
		{
			"current with latest",
			checkResult{State: stateCurrent, Channel: "npm", CurrentVersion: "0.5.8", LatestVersion: "0.5.8"},
			"current (channel=npm, version=0.5.8, latest=0.5.8)",
		},
		{
			"current without latest",
			checkResult{State: stateCurrent, Channel: "npm", CurrentVersion: "0.5.8"},
			"current (channel=npm, version=0.5.8)",
		},
		{
			"update-available",
			checkResult{State: stateUpdateAvailable, Channel: "npm", CurrentVersion: "0.5.8", LatestVersion: "9.9.9"},
			"update-available (channel=npm, current=0.5.8, latest=9.9.9)",
		},
		{
			"unsupported",
			checkResult{State: stateUnsupported, Channel: "docker", CurrentVersion: "0.5.8", Message: "pull the image on the host"},
			"unsupported (channel=docker, current=0.5.8): pull the image on the host",
		},
		{
			"error",
			checkResult{State: stateError, Channel: "npm", CurrentVersion: "0.5.8", Message: "registry down"},
			"error (channel=npm, current=0.5.8): registry down",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderCheckHuman(tc.r); got != tc.want {
				t.Errorf("renderCheckHuman = %q, want %q", got, tc.want)
			}
		})
	}
}
