package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
)

// TestHandleSetFlows_TabRuleIsIndependentOfTheJSONForm pins issue #343
// at the tool: the "at least one tab" rule is decided by decoding the
// "type" of every entry, so the same document behaves the same whether
// it arrives as a compact string, as an indented string or as an array
// of objects. The string form keeps the bytes it was given and the
// object form re-encodes them; the rule must not notice the difference.
func TestHandleSetFlows_TabRuleIsIndependentOfTheJSONForm(t *testing.T) {
	withTab := []struct {
		name  string
		flows any
	}{
		{"compact string", `[{"id":"tab1","type":"tab","nodes":[]}]`},
		{"indented string", "[\n  {\n    \"id\": \"tab1\",\n    \"type\": \"tab\",\n    \"nodes\": []\n  }\n]"},
		{"array of objects", []any{map[string]any{"id": "tab1", "type": "tab", "nodes": []any{}}}},
	}
	withoutTab := []struct {
		name  string
		flows any
	}{
		{"compact string", `[{"id":"n1","type":"inject","z":"tab1","wires":[]}]`},
		{"indented string", "[\n  {\n    \"id\": \"n1\",\n    \"type\": \"inject\",\n    \"z\": \"tab1\",\n    \"wires\": []\n  }\n]"},
		{"array of objects", []any{map[string]any{"id": "n1", "type": "inject", "z": "tab1", "wires": []any{}}}},
	}

	// The same document with a tab is deployed in every form, and what
	// reaches the runtime is the same document.
	var posted []any
	for _, f := range withTab {
		t.Run("with a tab/"+f.name, func(t *testing.T) {
			s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/flows":
					_, _ = w.Write([]byte(`[{"id":"old","type":"tab"}]`))
				case r.Method == http.MethodPost && r.URL.Path == "/flows":
					body, _ := io.ReadAll(r.Body)
					var decoded any
					if err := json.Unmarshal(body, &decoded); err != nil {
						t.Errorf("posted body is not JSON: %v (%s)", err, body)
					}
					posted = append(posted, decoded)
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				}
			})
			res, err := call(t, s.handleSetFlows, map[string]any{"flows": f.flows})
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if res.IsError {
				t.Fatalf("unexpected error: %v", res.Content)
			}
		})
	}
	if len(posted) != len(withTab) {
		t.Fatalf("expected %d deployments, got %d", len(withTab), len(posted))
	}
	for i, body := range posted {
		if !reflect.DeepEqual(body, posted[0]) {
			t.Errorf("%s deployed a different document than %s: %v vs %v",
				withTab[i].name, withTab[0].name, body, posted[0])
		}
	}

	// The same document without a tab is refused in every form, and the
	// runtime is never contacted.
	for _, f := range withoutTab {
		t.Run("without a tab/"+f.name, func(t *testing.T) {
			s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("runtime should not be contacted for a no-tab flows array: %s %s", r.Method, r.URL.Path)
			})
			res, err := call(t, s.handleSetFlows, map[string]any{"flows": f.flows})
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if !res.IsError {
				t.Fatal("a flows array without a tab should be refused")
			}
		})
	}
}
