package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// TestIssue329_UpdateNodeTypeChangeIsRefused covers the first audited path:
// update_node applies an arbitrary property patch, so a patch that sets
// "type" to a forbidden value used to reach the deployment untouched.
//
// The refusal must happen before the runtime is contacted at all: the
// patch names the type, so nothing has to be read to know it is forbidden.
func TestIssue329_UpdateNodeTypeChangeIsRefused(t *testing.T) {
	srv, seen := issue329Runtime(t, nil)
	s := issue329Server(t, srv, "exec")

	res, err := s.handleUpdateNode(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"flow_id":    "tabA",
			"node_id":    "n1",
			"properties": `{"type":"exec"}`,
		}},
	})
	if err != nil {
		t.Fatalf("handleUpdateNode returned err=%v", err)
	}
	assertIssue329Denied(t, res, "exec")

	if got := issue329Requests(seen); len(got) != 0 {
		t.Errorf("a refused update_node must not contact the runtime, got %v", got)
	}
}

// TestIssue329_UpdateNodeFinalTypeIsJudged covers the same surface from the
// other side: the patch does not name a type, but the node it modifies
// already is a forbidden one, so the node as it would be deployed is
// forbidden too.
//
// The runtime is read (the final node cannot be known without reading the
// tab) but nothing is deployed.
func TestIssue329_UpdateNodeFinalTypeIsJudged(t *testing.T) {
	srv, seen := issue329Runtime(t, map[string]string{
		"GET /flow/tabA": `{"id":"tabA","label":"A","nodes":[` + issue329ExecNode + `]}`,
	})
	s := issue329Server(t, srv, "exec")

	res, err := s.handleUpdateNode(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"flow_id":    "tabA",
			"node_id":    "e1",
			"properties": `{"name":"renamed"}`,
		}},
	})
	if err != nil {
		t.Fatalf("handleUpdateNode returned err=%v", err)
	}
	assertIssue329Denied(t, res, "exec")

	for _, req := range issue329Requests(seen) {
		if !strings.HasPrefix(req, "GET ") {
			t.Errorf("a refused update_node must not deploy, got %v", issue329Requests(seen))
		}
	}
}

// TestIssue329_ImportFlowNestedTabIsRefused covers the second audited path:
// import_flow normalizes a clipboard tab and creates it, and the nested tab
// carried an exec node nobody looked at.
func TestIssue329_ImportFlowNestedTabIsRefused(t *testing.T) {
	srv, seen := issue329Runtime(t, nil)
	s := issue329Server(t, srv, "exec")

	res, err := s.handleImportFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"clipboard": `[{"id":"tabA","label":"pwn","nodes":[` + issue329ExecNode + `]}]`,
		}},
	})
	if err != nil {
		t.Fatalf("handleImportFlow returned err=%v", err)
	}
	assertIssue329Denied(t, res, "exec")

	if got := issue329Requests(seen); len(got) != 0 {
		t.Errorf("a refused import_flow must not contact the runtime, got %v", got)
	}
}

// TestIssue329_CreateSubflowWithDeniedNodeIsRefused covers the third audited
// path: a subflow definition carries its own nodes, and those nodes carry
// node types.
func TestIssue329_CreateSubflowWithDeniedNodeIsRefused(t *testing.T) {
	srv, seen := issue329Runtime(t, nil)
	s := issue329Server(t, srv, "exec")

	res, err := s.handleCreateSubflow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"subflow": map[string]any{
				"id":   "sf1",
				"type": "subflow",
				"name": "pwn",
				"in":   []any{},
				"out":  []any{},
				"nodes": []any{map[string]any{
					"id": "e1", "type": "exec", "z": "sf1", "x": 140, "y": 140, "command": "id", "wires": []any{},
				}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("handleCreateSubflow returned err=%v", err)
	}
	assertIssue329Denied(t, res, "exec")

	if got := issue329Requests(seen); len(got) != 0 {
		t.Errorf("a refused create_subflow must not contact the runtime, got %v", got)
	}
}

// TestIssue329_UpdateSubflowWithDeniedNodeIsRefused covers the sibling of
// the previous case: update_subflow replaces a definition wholesale, so it
// carries the same nested node types create_subflow does.
func TestIssue329_UpdateSubflowWithDeniedNodeIsRefused(t *testing.T) {
	srv, seen := issue329Runtime(t, nil)
	s := issue329Server(t, srv, "exec")

	res, err := s.handleUpdateSubflow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"id": "sf1",
			"subflow": map[string]any{
				"id":   "sf1",
				"type": "subflow",
				"name": "pwn",
				"in":   []any{},
				"out":  []any{},
				"nodes": []any{map[string]any{
					"id": "e1", "type": "exec", "z": "sf1", "x": 140, "y": 140, "command": "id", "wires": []any{},
				}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("handleUpdateSubflow returned err=%v", err)
	}
	assertIssue329Denied(t, res, "exec")

	if got := issue329Requests(seen); len(got) != 0 {
		t.Errorf("a refused update_subflow must not contact the runtime, got %v", got)
	}
}

// TestIssue329_FlowDocumentWithDeniedSubflowIsRefused covers the fourth
// audited path: a flow document can carry "subflows" definitions, and the
// deployment flattens them into the configuration. The denylist walk used
// to stop at the immediate "nodes" and "configs" keys, so a forbidden type
// nested in a subflow definition passed through.
func TestIssue329_FlowDocumentWithDeniedSubflowIsRefused(t *testing.T) {
	srv, seen := issue329Runtime(t, nil)
	s := issue329Server(t, srv, "exec")

	flow := `{"id":"tabA","label":"A","nodes":[],` +
		`"subflows":[{"id":"sf1","type":"subflow","name":"pwn","in":[],"out":[],` +
		`"nodes":[` + issue329ExecNode + `]}]}`

	for name, call := range map[string]func() (*mcp.CallToolResult, error){
		"create_flow": func() (*mcp.CallToolResult, error) {
			return s.handleCreateFlow(context.Background(), mcp.CallToolRequest{
				Params: mcp.CallToolParams{Arguments: map[string]any{"flow": flow}},
			})
		},
		"update_flow": func() (*mcp.CallToolResult, error) {
			return s.handleUpdateFlow(context.Background(), mcp.CallToolRequest{
				Params: mcp.CallToolParams{Arguments: map[string]any{"id": "tabA", "flow": flow}},
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := call()
			if err != nil {
				t.Fatalf("%s returned err=%v", name, err)
			}
			assertIssue329Denied(t, res, "exec")
		})
	}

	if got := issue329Requests(seen); len(got) != 0 {
		t.Errorf("a refused flow write must not contact the runtime, got %v", got)
	}
}
