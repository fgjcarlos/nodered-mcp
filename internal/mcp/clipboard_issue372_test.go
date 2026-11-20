package mcp

import (
	"regexp"
	"testing"
)

// Issue #372: the legacy nested single-tab clipboard earlier releases of
// export_flow produced, and the placement refusal for a stray z.

// TestIssue372_LegacyNestedTabRefusals: every malformed nested tab is
// refused with its own message before any POST /flow.
func TestIssue372_LegacyNestedTabRefusals(t *testing.T) {
	cases := []struct{ name, clipboard, want string }{
		{"nodes not an array", `[{"id":"t1","label":"T","nodes":{}}]`,
			`clipboard tab: "nodes" is not an array`},
		{"configs not an array", `[{"id":"t1","label":"T","configs":"c1"}]`,
			`clipboard tab: "configs" is not an array`},
		{"member not an object", `[{"id":"t1","label":"T","nodes":[5]}]`,
			`clipboard tab member is not a JSON object: 5`},
		{"null member", `[{"id":"t1","label":"T","configs":[null]}]`,
			`clipboard tab member is not a JSON object: null`},
		{"z neither the tab nor a subflow", `[
		  {"id":"t1","type":"tab","label":"T"},
		  {"id":"n1","type":"comment","z":"elsewhere","x":1,"y":1,"wires":[]}
		]`, `clipboard element "n1" has z "elsewhere", which is neither the tab nor a subflow in the clipboard`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, s := newFakeRuntime(t, `[]`)
			res := importClipboard(t, s, tc.clipboard)
			if !res.IsError || resultFirstText(res) != tc.want {
				t.Fatalf("got %q (error=%v), want %q", resultFirstText(res), res.IsError, tc.want)
			}
			if len(rt.posted) != 0 {
				t.Error("a refused clipboard reached POST /flow")
			}
		})
	}
}

// TestIssue372_LegacyNestedTabWithoutID: a nested tab with no id gets a
// minted Node-RED style id, and its members are placed on it.
func TestIssue372_LegacyNestedTabWithoutID(t *testing.T) {
	rt, s := newFakeRuntime(t, `[]`)
	res := importClipboard(t, s, `[{"label":"T","nodes":[{"id":"n1","type":"comment","x":1,"y":1,"wires":[]}]}]`)
	if res.IsError {
		t.Fatalf("import refused: %s", resultFirstText(res))
	}
	doc := rt.lastPost(t)
	id, _ := doc["id"].(string)
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(id) {
		t.Errorf("tab id = %v, want a minted 16-hex id", doc["id"])
	}
	nodes, _ := doc["nodes"].([]any)
	if len(nodes) != 1 || nodes[0].(map[string]any)["id"] != "n1" {
		t.Errorf("nodes = %v, want n1", doc["nodes"])
	}
}

// TestIssue372_LegacyNestedTabConfigsOnly: a nested tab with configs and
// no nodes imports the configs as tab-scoped config nodes.
func TestIssue372_LegacyNestedTabConfigsOnly(t *testing.T) {
	rt, s := newFakeRuntime(t, `[]`)
	res := importClipboard(t, s, `[{"id":"t1","label":"T","configs":[{"id":"c1","type":"ui-base","name":"cfg"}]}]`)
	if res.IsError {
		t.Fatalf("import refused: %s", resultFirstText(res))
	}
	doc := rt.lastPost(t)
	if nodes, _ := doc["nodes"].([]any); len(nodes) != 0 {
		t.Errorf("nodes = %v, want none", doc["nodes"])
	}
	cfgs, _ := doc["configs"].([]any)
	if len(cfgs) != 1 || cfgs[0].(map[string]any)["id"] != "c1" {
		t.Errorf("configs = %v, want c1", doc["configs"])
	}
}
