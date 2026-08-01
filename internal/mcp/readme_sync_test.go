package mcp

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// TestReadmeSyncWithRegistry is the consistency check that closes issue
// #74: the README capabilities table and the live MCP registry must list
// the same set of tools, resources, and prompts. Drift fails the test.
//
// The parser is limited to the explicit capability section headers —
// Flows / Palette / Runtime and recovery / Resources / Prompts — so
// environment-variable tables, CLI usage tables, and prose do not
// pollute the comparison. If a future restructure changes the table
// format, the parser will surface the expectation and the README can
// be updated alongside — but the registry and the docs cannot silently
// diverge again.
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
	for _, rel := range []string{"README.md", "README.es.md"} {
		readmeTools, readmeResources, readmePrompts, err := parseReadmeTables(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		compareRegistry(t, rel+" tools", registryTools, readmeTools)
		compareRegistry(t, rel+" resources", registryResources, readmeResources)
		compareRegistry(t, rel+" prompts", registryPrompts, readmePrompts)
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
	return trimmed == "### Resources" || trimmed == "### Recursos"
}

func isPromptsSection(trimmed string) bool {
	return trimmed == "### Prompts" || trimmed == "### Prompts MCP" || trimmed == "### Prompts (ES)"
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
