package mcp

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// TestReadmeSyncWithRegistry is the consistency check that closes issue
// #74: the docs capability table and the live MCP registry must list
// the same set of tools, resources, and prompts. Drift fails the test.
//
// After #174 the canonical table moved from README.md to docs/tools.md
// (the README is now a short index). The test scans every docs file
// in the registry's responsibility and unions the names it finds.
// The READMEs are still scanned — they may mention tool names in
// prose without listing every one, which is fine; missing entries
// from docs/tools.md is the drift we care about.
func TestReadmeSyncWithRegistry(t *testing.T) {
	c, err := nodered.NewClient(nodered.Options{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	// debugStream=true exercises the WebSocket tail construction path;
	// the registry set is identical either way.
	srv := New(c, Options{Version: "test", DebugStream: true})

	registryTools := make([]string, 0, len(srv.tools))
	for _, tool := range srv.tools {
		registryTools = append(registryTools, tool.Name)
	}
	registryResources := make([]string, 0, len(srv.resources))
	for _, res := range srv.resources {
		registryResources = append(registryResources, res.URI)
	}
	registryPrompts := make([]string, 0, len(srv.prompts))
	for _, prompt := range srv.prompts {
		registryPrompts = append(registryPrompts, prompt.Name)
	}
	sort.Strings(registryTools)
	sort.Strings(registryResources)
	sort.Strings(registryPrompts)

	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	for _, rel := range []string{"README.md", "README.es.md", "docs/tools.md"} {
		docTools, docResources, docPrompts, err := parseReadmeTables(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		// docs/tools.md is the canonical table — every name must be there.
		// The READMEs may mention names in prose without listing every one.
		// We surface missing-from-docs/tools.md as the real drift signal.
		if rel == "docs/tools.md" {
			compareRegistry(t, rel+" tools", registryTools, docTools)
			compareRegistry(t, rel+" resources", registryResources, docResources)
			compareRegistry(t, rel+" prompts", registryPrompts, docPrompts)
		}
	}
}

// readCountClaim matches a claim about the --read-only subset, in
// either language. It is tried first and its matches are removed from
// the line, so the general countClaim below never sees them. Matching
// the phrase rather than looking for "read" anywhere on the line
// matters: "Catalog of the 44 MCP tools (read / write / action)" names
// the risk classification, not the subset.
var readCountClaim = regexp.MustCompile(
	"(?i)(\\d+)\\s+(?:read\\s+tools|tools\\s+marked\\s+`?read`?|tools\\s+de\\s+lectura)")

// countClaim matches the headline totals the docs quote in prose --
// "44 tools", "the 44 MCP tools". The name-level check above cannot
// catch these: docs/tools.md can list every tool correctly and still
// open with a stale total, which is exactly the drift that survived
// from v0.5 (43) into v0.6 (44).
var countClaim = regexp.MustCompile(`(\d+)\s+(?:MCP\s+)?tools`)

// countClaimDocs are the files that describe the *current* build.
// docs/roadmap.md is deliberately excluded: its per-version rows are
// history and must keep quoting the count each release actually had.
var countClaimDocs = []string{
	"README.md",
	"README.es.md",
	"docs/README.md",
	"docs/tools.md",
	"docs/clients.md",
	"docs/configuration.md",
}

// TestDocsToolCountsMatchRegistry pins every "N tools" claim in the
// user-facing docs to the live registry, so the totals cannot drift
// away from the catalogue the way they did between v0.5 and v0.6.
func TestDocsToolCountsMatchRegistry(t *testing.T) {
	c, err := nodered.NewClient(nodered.Options{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	wantAll := len(New(c, Options{Version: "test"}).tools)
	wantRead := len(New(c, Options{Version: "test", ReadOnly: true}).tools)

	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	for _, rel := range countClaimDocs {
		raw, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			check := func(got string, want int, label string) {
				if got != strconv.Itoa(want) {
					t.Errorf("%s:%d: claims %s %s, registry has %d\n\t%s",
						rel, i+1, got, label, want, strings.TrimSpace(line))
				}
			}
			// Consume the read-only claims first so the general
			// matcher below cannot double-report them.
			rest := readCountClaim.ReplaceAllStringFunc(line, func(hit string) string {
				check(readCountClaim.FindStringSubmatch(hit)[1], wantRead, "read tools")
				return ""
			})
			for _, m := range countClaim.FindAllStringSubmatch(rest, -1) {
				check(m[1], wantAll, "tools")
			}
		}
	}
}

// compareRegistry fails the test if the two slices differ in either
// direction. Order is ignored because the registry sorts; the README
// parses in document order.
func compareRegistry(t *testing.T, label string, registry, readme []string) {
	t.Helper()
	regSet := set(registry)
	readmeSet := set(readme)
	if len(regSet) != len(readme) {
		t.Errorf("%s: README lists %d unique names, registry has %d", label, len(readme), len(regSet))
	}
	if len(regSet) != len(registry) {
		t.Errorf("%s: registry has %d entries but only %d unique names", label, len(registry), len(regSet))
	}
	var missing, extra []string
	for n := range regSet {
		if _, ok := readmeSet[n]; !ok {
			missing = append(missing, n)
		}
	}
	for n := range readmeSet {
		if _, ok := regSet[n]; !ok {
			extra = append(extra, n)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%s: in registry but missing from README: %v", label, missing)
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		t.Errorf("%s: in README but not registered: %v", label, extra)
	}
}

func set(in []string) map[string]struct{} {
	m := make(map[string]struct{}, len(in))
	for _, s := range in {
		m[s] = struct{}{}
	}
	return m
}

// sectionMatchers: the headers that delimit a capability table. The
// Resources section lives in any language (we only test English here);
// Spanish uses "Recursos" but the test names the explicit header.
var toolSections = []string{
	"### Flows",
	"### Palette",
	"### Runtime and recovery",
	"### Runtime y recuperación",
	"## Flows",
	"## Palette",
	"## Runtime, diagnostics, recovery",
}

// parseReadmeTables walks the file linearly. While inside a recognised
// tool section, every backtick-quoted first cell of a Markdown table
// row is collected as a tool name. The Resources section is treated
// separately (its names are `nodered://...` URIs and live in any
// language). The Prompts section is also language-specific.
func parseReadmeTables(path string) (tools, resources, prompts []string, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil, err
	}
	rowRE := regexp.MustCompile("^\\|\\s+`([^`]+)`\\s+\\|")
	lines := strings.Split(string(raw), "\n")

	type mode int
	const (
		none mode = iota
		toolsMode
		resourcesMode
		promptsMode
	)
	current := none
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Section delimiters are tight: a line that starts with
		// `### ` (in the supported chapter set) switches mode.
		// Anything else (## headers, prose, code blocks) leaves
		// the current mode untouched.
		switch {
		case isToolSection(trimmed):
			current = toolsMode
		case isResourcesSection(trimmed):
			current = resourcesMode
		case isPromptsSection(trimmed):
			current = promptsMode
		case strings.HasPrefix(trimmed, "### "):
			// Another subsection header (e.g. "Working with
			// large instances") ends the current
			// capability subtable.
			if current != none {
				current = none
			}
		}
		if current == none {
			continue
		}
		m := rowRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		first := m[1]
		switch current {
		case toolsMode:
			tools = append(tools, first)
		case resourcesMode:
			if strings.HasPrefix(first, "nodered://") {
				resources = append(resources, first)
			}
		case promptsMode:
			prompts = append(prompts, first)
		}
	}
	return tools, resources, prompts, nil
}

func isToolSection(trimmed string) bool {
	for _, h := range toolSections {
		if trimmed == h {
			return true
		}
	}
	return false
}

func isResourcesSection(trimmed string) bool {
	return trimmed == "### Resources" || trimmed == "### Recursos" ||
		strings.HasPrefix(trimmed, "## Resources")
}

func isPromptsSection(trimmed string) bool {
	return trimmed == "### Prompts" || trimmed == "### Prompts MCP" || trimmed == "### Prompts (ES)" ||
		strings.HasPrefix(trimmed, "## Prompts")
}

// findRepoRoot locates the directory containing go.mod by walking up
// from the test's working directory.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
	return "", os.ErrNotExist
}
