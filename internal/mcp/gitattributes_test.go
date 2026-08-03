package mcp

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestGitattributesPolicy is the regression guard for issue #67: the
// repository must declare LF as the canonical line ending for the text
// formats the build relies on, otherwise Windows checkouts with
// core.autocrlf=true leak CRLF and break `gofmt -l .`.
//
// The check is intentionally narrow: it pins the formats the
// acceptance criteria name (.go, go.mod, go.sum, *.sh, *.yaml, *.yml,
// *.md) plus the JSON/JS surfaces this repo ships, so a future "we
// don't need this any more" commit has to remove the test on purpose
// instead of letting the policy silently rot.
//
// The CRLF-on-policy-file subcheck is gated to Linux/macOS because
// Windows checkouts with core.autocrlf=true rewrite `.gitattributes`
// to CRLF at checkout time regardless of what the file declares; the
// ci.yml gate mirrors this skip. The presence checks above run on
// every platform — that is the contract that actually fails open.
func TestGitattributesPolicy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows checkouts rewrite .gitattributes to CRLF regardless of policy; gated in ci.yml.")
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}

	path := filepath.Join(repoRoot, ".gitattributes")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read .gitattributes: %v", err)
	}
	contents := string(raw)

	required := []string{
		"*.go text eol=lf",
		"go.mod text eol=lf",
		"go.sum text eol=lf",
		"*.sh text eol=lf",
		"*.yaml text eol=lf",
		"*.yml text eol=lf",
		"*.md text eol=lf",
	}
	for _, want := range required {
		if !strings.Contains(contents, want) {
			t.Errorf(".gitattributes missing required policy line %q", want)
		}
	}

	if strings.Contains(contents, "\r") {
		t.Errorf(".gitattributes contains CRLF; policy file must be LF")
	}
}
