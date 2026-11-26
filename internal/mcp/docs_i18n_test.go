package mcp

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Every page in docs/ has a Spanish twin in docs/es/ with the same file name,
// and the reverse (CONTRIBUTING.md, Translation policy). docs/audits/ is
// historical and English-only. Each page also carries the language switcher.
func TestDocsHaveSpanishTwins(t *testing.T) {
	root := filepath.Join("..", "..", "docs")
	en := markdownNames(t, root)
	es := markdownNames(t, filepath.Join(root, "es"))

	for name := range en {
		if !es[name] {
			t.Errorf("docs/%s has no Spanish twin docs/es/%s", name, name)
		}
	}
	for name := range es {
		if !en[name] {
			t.Errorf("docs/es/%s has no English source docs/%s", name, name)
		}
	}

	for _, page := range []struct{ path, switcher string }{
		{filepath.Join(root, "%s"), "**English** | [Español](es/%s)"},
		{filepath.Join(root, "es", "%s"), "[English](../%s) | **Español**"},
	} {
		for _, name := range sortedNames(en) {
			body, err := os.ReadFile(strings.Replace(page.path, "%s", name, 1))
			if err != nil {
				continue // reported above as a missing twin
			}
			want := strings.Replace(page.switcher, "%s", name, 1)
			if !strings.Contains(string(body), want) {
				t.Errorf("%s: missing language switcher %q", strings.Replace(page.path, "%s", name, 1), want)
			}
		}
	}
}

func markdownNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names[e.Name()] = true
		}
	}
	return names
}

func sortedNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
