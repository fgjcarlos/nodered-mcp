package nodered

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// ---------------------------------------------------------------------------
// Issue #329: the node-type policy engine.
//
// The engine decides whether a write may carry a given node type. Two
// properties matter more than the rest:
//
//   - it walks the admin API's own shapes recursively, so a forbidden type
//     nested inside a subflow definition is found too (the walk that used to
//     stop at the immediate "nodes"/"configs" keys is what the issue is
//     about); and
//   - it never guesses. A payload it cannot read carries no node type it can
//     name, so it produces no error rather than a false accusation.
//
// The comparison is exact and case-sensitive, because Node-RED node types
// are case-sensitive.
// ---------------------------------------------------------------------------

// issue329Policy is the default operator posture: "exec" and "system" are
// forbidden, everything else is allowed.
func issue329Policy(denied ...string) NodePolicy {
	set := map[string]struct{}{}
	for _, t := range denied {
		set[t] = struct{}{}
	}
	return func(nodeType string) bool {
		_, forbidden := set[nodeType]
		return !forbidden
	}
}

// issue329Denied builds a client carrying the given policy.
func issue329Denied(t *testing.T, policy NodePolicy) *Client {
	t.Helper()
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the runtime was contacted for a write the policy should have refused: %s %s", r.Method, r.URL.Path)
		_, _ = w.Write([]byte(`[]`))
	})
	c.SetNodePolicy(policy)
	return c
}

// assertIssue329DeniedError asserts the error is a policy refusal naming the
// expected node type.
func assertIssue329DeniedError(t *testing.T, err error, nodeType string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a policy refusal, got nil")
	}
	var denied *DeniedNodeError
	if !errors.As(err, &denied) {
		t.Fatalf("expected a *DeniedNodeError, got %T: %v", err, err)
	}
	if denied.NodeType != nodeType {
		t.Errorf("refusal names node type %q, want %q", denied.NodeType, nodeType)
	}
}

// TestIssue329_DeniedNodeErrorMessage pins the exact wording of the refusal
// the MCP layer renders: it names the offending type, and nothing else.
func TestIssue329_DeniedNodeErrorMessage(t *testing.T) {
	err := &DeniedNodeError{NodeType: "exec"}
	const want = `node type "exec" is denied by the configured node policy`
	if got := err.Error(); got != want {
		t.Errorf("DeniedNodeError.Error() = %q, want %q", got, want)
	}
}

// TestIssue329_DeniedNodeTypeInDocument covers the shapes the admin API
// uses, and the case the issue is about: a forbidden type nested inside a
// subflow definition of a flow document.
func TestIssue329_DeniedNodeTypeInDocument(t *testing.T) {
	cases := map[string]struct {
		doc      string
		wantType string
	}{
		"node in the immediate nodes array": {
			doc:      `{"id":"tabA","label":"A","nodes":[{"id":"e1","type":"exec","z":"tabA","wires":[]}]}`,
			wantType: "exec",
		},
		"node nested inside a subflow definition": {
			doc: `{"id":"tabA","label":"A","nodes":[],` +
				`"subflows":[{"id":"sf1","type":"subflow","name":"pwn","in":[],"out":[],` +
				`"nodes":[{"id":"e1","type":"exec","z":"sf1","wires":[]}]}]}`,
			wantType: "exec",
		},
		"node nested two levels deep": {
			doc: `{"id":"tabA","label":"A","nodes":[],` +
				`"subflows":[{"id":"sf1","type":"subflow","name":"outer","in":[],"out":[],` +
				`"nodes":[],"subflows":[{"id":"sf2","type":"subflow","name":"inner","in":[],"out":[],` +
				`"nodes":[{"id":"e1","type":"exec","z":"sf2","wires":[]}]}]}]}`,
			wantType: "exec",
		},
		"node in the configs array": {
			doc:      `{"id":"tabA","label":"A","nodes":[],"configs":[{"id":"c1","type":"system","name":"pwn"}]}`,
			wantType: "system",
		},
		"no denied type anywhere": {
			doc:      `{"id":"tabA","label":"A","nodes":[{"id":"i1","type":"inject","z":"tabA","wires":[]}]}`,
			wantType: "",
		},
		"empty document": {
			doc:      `{}`,
			wantType: "",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := issue329Denied(t, issue329Policy("exec", "system"))
			err := c.deniedNodeType(json.RawMessage(tc.doc))
			if tc.wantType == "" {
				if err != nil {
					t.Fatalf("expected no refusal, got %v", err)
				}
				return
			}
			assertIssue329DeniedError(t, err, tc.wantType)
		})
	}
}

// TestIssue329_DeniedNodeTypeIgnoresUnreadablePayloads pins the
// never-guess rule: a payload the policy cannot read carries no node type
// it can name, so it produces no refusal. Guessing here would reject
// perfectly good writes for a reason the operator cannot act on.
func TestIssue329_DeniedNodeTypeIgnoresUnreadablePayloads(t *testing.T) {
	cases := map[string]string{
		"malformed JSON":          `{"nodes":[{"type":"exec"`,
		"not an object":           `["a","b"]`,
		"non-string type":         `{"nodes":[{"id":"e1","type":42,"z":"tabA"}]}`,
		"absent type key":         `{"nodes":[{"id":"e1","z":"tabA"}]}`,
		"non-object member":       `{"nodes":["e1","e2"]}`,
		"null nodes member":       `{"nodes":null}`,
		"empty payload":           ``,
		"unreadable subflows key": `{"nodes":[],"subflows":"not-an-array"}`,
	}

	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			c := issue329Denied(t, issue329Policy("exec", "system"))
			if err := c.deniedNodeType(json.RawMessage(doc)); err != nil {
				t.Errorf("expected no refusal for an unreadable payload, got %v", err)
			}
		})
	}
}

// TestIssue329_PolicyComparisonIsExactAndCaseSensitive pins the comparison
// rules: the policy is a predicate over the exact type string, so "Exec" and
// "exec " are different types and neither is denied by a policy that denies
// "exec".
func TestIssue329_PolicyComparisonIsExactAndCaseSensitive(t *testing.T) {
	c := issue329Denied(t, issue329Policy("exec"))

	for _, nodeType := range []string{"Exec", "EXEC", "exec ", " exec", "executable", "system"} {
		t.Run(nodeType, func(t *testing.T) {
			if err := c.deniedNodeTypeForType(nodeType); err != nil {
				t.Errorf("type %q must not be denied by a policy that denies \"exec\", got %v", nodeType, err)
			}
		})
	}

	if err := c.deniedNodeTypeForType("exec"); err == nil {
		t.Error("type \"exec\" must be denied by a policy that denies \"exec\"")
	}
}

// TestIssue329_NilPolicyAllowsEveryType pins the operator opt-out: with no
// policy installed the client behaves as if no restriction existed.
func TestIssue329_NilPolicyAllowsEveryType(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected runtime request: %s %s", r.Method, r.URL.Path)
		_, _ = w.Write([]byte(`[]`))
	})

	if !c.NodeTypeAllowed("exec") {
		t.Error("a client without a policy must allow every node type")
	}
	if err := c.deniedNodeTypeForType("exec"); err != nil {
		t.Errorf("a client without a policy must not refuse anything, got %v", err)
	}
	doc := json.RawMessage(`{"id":"tabA","nodes":[{"id":"e1","type":"exec","z":"tabA","wires":[]}]}`)
	if err := c.deniedNodeType(doc); err != nil {
		t.Errorf("a client without a policy must not refuse a document, got %v", err)
	}
}

// TestIssue329_NodeTypeAllowedReportsThePolicy pins the accessor the MCP
// layer uses to pre-check the node types it provisions for itself.
func TestIssue329_NodeTypeAllowedReportsThePolicy(t *testing.T) {
	c := issue329Denied(t, issue329Policy("function"))

	if c.NodeTypeAllowed("function") {
		t.Error("\"function\" must not be allowed by a policy that denies \"function\"")
	}
	if !c.NodeTypeAllowed("inject") {
		t.Error("\"inject\" must be allowed by a policy that denies only \"function\"")
	}
	if !c.NodeTypeAllowed("exec") {
		t.Error("\"exec\" must be allowed by a policy that denies only \"function\"")
	}
}

// TestIssue329_DeniedNodeTypeOfNodeFindsTheNodeByID pins the lookup the
// update path uses to judge the node as it will be deployed: the type is
// read from the node the patch touched, in either collection.
func TestIssue329_DeniedNodeTypeOfNodeFindsTheNodeByID(t *testing.T) {
	c := issue329Denied(t, issue329Policy("exec", "system"))

	doc := RawFlow(`{"id":"tabA","label":"A",` +
		`"nodes":[{"id":"i1","type":"inject","z":"tabA","wires":[]},{"id":"e1","type":"exec","z":"tabA","wires":[]}],` +
		`"configs":[{"id":"c1","type":"system","name":"pwn"}]}`)

	assertIssue329DeniedError(t, c.deniedNodeTypeOfNode(doc, "e1"), "exec")
	assertIssue329DeniedError(t, c.deniedNodeTypeOfNode(doc, "c1"), "system")

	if err := c.deniedNodeTypeOfNode(doc, "i1"); err != nil {
		t.Errorf("an allowed node must not be refused, got %v", err)
	}
	if err := c.deniedNodeTypeOfNode(doc, "missing"); err != nil {
		t.Errorf("an unknown node id must not be refused, got %v", err)
	}
	if err := c.deniedNodeTypeOfNode(doc, ""); err != nil {
		t.Errorf("an empty node id must not be refused, got %v", err)
	}
}

// TestIssue329_DeniedNodeTypeInFlowsArray covers the flat /flows shape the
// full-deployment tools use: one denied entry rejects the whole array,
// because the array replaces the entire configuration.
func TestIssue329_DeniedNodeTypeInFlowsArray(t *testing.T) {
	c := issue329Denied(t, issue329Policy("exec", "system"))

	clean := []json.RawMessage{
		json.RawMessage(`{"type":"tab","id":"tabA","label":"A"}`),
		json.RawMessage(`{"id":"i1","type":"inject","z":"tabA","wires":[]}`),
	}
	if err := c.deniedNodeTypeInFlows(clean); err != nil {
		t.Errorf("an array without denied types must be accepted, got %v", err)
	}

	dirty := []json.RawMessage{
		json.RawMessage(`{"type":"tab","id":"tabA","label":"A"}`),
		json.RawMessage(`{"id":"e1","type":"exec","z":"tabA","wires":[]}`),
	}
	assertIssue329DeniedError(t, c.deniedNodeTypeInFlows(dirty), "exec")

	// A subflow definition in the flat shape carries its nodes inline, so
	// the denied type is found there too.
	nested := []json.RawMessage{
		json.RawMessage(`{"type":"tab","id":"tabA","label":"A"}`),
		json.RawMessage(`{"type":"subflow","id":"sf1","name":"pwn","in":[],"out":[],` +
			`"nodes":[{"id":"e1","type":"exec","z":"sf1","wires":[]}]}`),
	}
	assertIssue329DeniedError(t, c.deniedNodeTypeInFlows(nested), "exec")
}

// TestIssue329_WritePathRefusesBeforeContactingTheRuntime is the client-side
// half of the guarantee: a write carrying a forbidden node type is refused
// without the runtime ever being asked to deploy it.
func TestIssue329_WritePathRefusesBeforeContactingTheRuntime(t *testing.T) {
	c := issue329Denied(t, issue329Policy("exec"))

	node := json.RawMessage(`{"id":"e1","type":"exec","z":"tabA","x":140,"y":140,"command":"id","wires":[]}`)
	err := c.AddNode(context.Background(), "tabA", node)
	assertIssue329DeniedError(t, err, "exec")
}
