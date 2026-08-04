package config

import (
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// envName matches the environment variables this package reads. Both
// the source and the docs are scanned with it, so a knob added to
// config.go without a row in docs/configuration.md fails the build.
// Eight variables (the rate limiter, the body cap, the node denylist,
// the list_flows threshold, the backup retention and the loopback
// acknowledgement) shipped undocumented before this test existed.
var envName = regexp.MustCompile(`\b(?:MCP|NODERED)_[A-Z_]+\b`)

const configDocs = "../../docs/configuration.md"

// names collects every distinct match, ignoring the surrounding syntax.
func names(src string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, m := range envName.FindAllString(src, -1) {
		out[m] = struct{}{}
	}
	return out
}

// TestEveryEnvVarIsDocumented pins docs/configuration.md to the config
// loader. It reads config.go as text rather than reflecting over the
// Config struct because the env name is the contract with the
// operator, and only the source literal carries it.
func TestEveryEnvVarIsDocumented(t *testing.T) {
	src, err := os.ReadFile("config.go")
	if err != nil {
		t.Fatalf("read config.go: %v", err)
	}
	docs, err := os.ReadFile(configDocs)
	if err != nil {
		t.Fatalf("read %s: %v", configDocs, err)
	}
	documented := names(string(docs))

	var missing []string
	for name := range names(string(src)) {
		if _, ok := documented[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("read by config.go but absent from %s: %v", configDocs, missing)
	}
}

// TestDocumentedEnvVarCountMatchesTable keeps the "N environment
// variables" sentence honest against the table underneath it. The
// sentence said 13 while the table listed 14 rows and the loader read
// 22 -- a number nobody rechecks by hand.
func TestDocumentedEnvVarCountMatchesTable(t *testing.T) {
	raw, err := os.ReadFile(configDocs)
	if err != nil {
		t.Fatalf("read %s: %v", configDocs, err)
	}
	lines := strings.Split(string(raw), "\n")

	rows := 0
	for _, line := range lines {
		// A variable row is a table row whose first cell is a
		// backtick-quoted env name.
		if strings.HasPrefix(line, "| `") && envName.MatchString(line) {
			rows++
		}
	}
	if rows == 0 {
		t.Fatal("no variable rows found; the table format changed")
	}

	claim := regexp.MustCompile(`^(\d+) environment variables`)
	for i, line := range lines {
		m := claim.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		if m[1] != strconv.Itoa(rows) {
			t.Errorf("%s:%d: claims %s environment variables, table has %d rows",
				configDocs, i+1, m[1], rows)
		}
		return
	}
	t.Error("no \"N environment variables\" sentence found")
}
