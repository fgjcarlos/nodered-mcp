package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// TestIssue329_MutatingSurfacesRefuseDeniedType is the matrix the issue asks
// for: every mutating surface that can carry a node type must refuse it,
// with the default policy and with a custom one.
//
// The two policies differ so the test cannot pass by accident on a
// hard-coded "exec" check: the custom policy names a type the default
// allows, and the default policy names one the custom allows.
func TestIssue329_MutatingSurfacesRefuseDeniedType(t *testing.T) {
	policies := []struct {
		name      string
		denylist  []string
		nodeType  string
		nodeJSON  string
		forbidden string
	}{
		{
			name:      "default policy",
			denylist:  []string{"exec", "system"},
			nodeType:  "exec",
			nodeJSON:  issue329ExecNode,
			forbidden: "exec",
		},
		{
			name:      "custom policy",
			denylist:  []string{"mqtt in"},
			nodeType:  "mqtt in",
			nodeJSON:  `{"id":"m1","type":"mqtt in","z":"tabA","x":140,"y":140,"wires":[]}`,
			forbidden: "mqtt in",
		},
	}

	for _, policy := range policies {
		t.Run(policy.name, func(t *testing.T) {
			srv, seen := issue329Runtime(t, nil)
			s := issue329Server(t, srv, policy.denylist...)

			nestedFlow := `{"id":"tabA","label":"A","nodes":[` + policy.nodeJSON + `]}`
			flatFlows := `[{"type":"tab","id":"tabA","label":"A"},` + policy.nodeJSON + `]`

			surfaces := map[string]func() (*mcp.CallToolResult, error){
				"create_flow": func() (*mcp.CallToolResult, error) {
					return s.handleCreateFlow(context.Background(), mcp.CallToolRequest{
						Params: mcp.CallToolParams{Arguments: map[string]any{"flow": nestedFlow}},
					})
				},
				"update_flow": func() (*mcp.CallToolResult, error) {
					return s.handleUpdateFlow(context.Background(), mcp.CallToolRequest{
						Params: mcp.CallToolParams{Arguments: map[string]any{"id": "tabA", "flow": nestedFlow}},
					})
				},
				"add_node": func() (*mcp.CallToolResult, error) {
					return s.handleAddNode(context.Background(), mcp.CallToolRequest{
						Params: mcp.CallToolParams{Arguments: map[string]any{
							"flow_id": "tabA",
							"node":    issue329NodeObject(policy.nodeJSON),
						}},
					})
				},
				"import_flow": func() (*mcp.CallToolResult, error) {
					return s.handleImportFlow(context.Background(), mcp.CallToolRequest{
						Params: mcp.CallToolParams{Arguments: map[string]any{
							"clipboard": `[{"id":"tabA","label":"A","nodes":[` + policy.nodeJSON + `]}]`,
						}},
					})
				},
				"set_flows": func() (*mcp.CallToolResult, error) {
					return s.handleSetFlows(context.Background(), mcp.CallToolRequest{
						Params: mcp.CallToolParams{Arguments: map[string]any{"flows": flatFlows}},
					})
				},
				"create_subflow": func() (*mcp.CallToolResult, error) {
					return s.handleCreateSubflow(context.Background(), mcp.CallToolRequest{
						Params: mcp.CallToolParams{Arguments: map[string]any{
							"subflow": map[string]any{
								"id": "sf1", "type": "subflow", "name": "pwn", "in": []any{}, "out": []any{},
								"nodes": []any{json.RawMessage(policy.nodeJSON)},
							},
						}},
					})
				},
				"update_subflow": func() (*mcp.CallToolResult, error) {
					return s.handleUpdateSubflow(context.Background(), mcp.CallToolRequest{
						Params: mcp.CallToolParams{Arguments: map[string]any{
							"id": "sf1",
							"subflow": map[string]any{
								"id": "sf1", "type": "subflow", "name": "pwn", "in": []any{}, "out": []any{},
								"nodes": []any{json.RawMessage(policy.nodeJSON)},
							},
						}},
					})
				},
			}

			for name, call := range surfaces {
				t.Run(name, func(t *testing.T) {
					res, err := call()
					if err != nil {
						t.Fatalf("%s returned err=%v", name, err)
					}
					assertIssue329Denied(t, res, policy.forbidden)
				})
			}

			if got := issue329Requests(seen); len(got) != 0 {
				t.Errorf("refused writes must not contact the runtime, got %v", got)
			}
		})
	}
}

// TestIssue329_RestoreBackupRefusesDeniedType covers the restore surface
// end to end: the backup file is read from disk and deployed wholesale, so
// a backup written before the policy changed can carry a forbidden type.
func TestIssue329_RestoreBackupRefusesDeniedType(t *testing.T) {
	backupDir := t.TempDir()
	doc := `[{"type":"tab","id":"tabA","label":"A"},` + issue329ExecNode + `]`
	name := "flows-20260101-000000.000000000-000001.json"
	if err := os.WriteFile(filepath.Join(backupDir, name), []byte(doc), 0o600); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	srv, seen := issue329Runtime(t, nil)
	c, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: backupDir})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	s := New(c, Options{Version: "test", NodeDenylist: []string{"exec"}})

	res, err := s.handleRestoreBackup(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"backup": name}},
	})
	if err != nil {
		t.Fatalf("handleRestoreBackup returned err=%v", err)
	}
	assertIssue329Denied(t, res, "exec")

	if got := issue329Requests(seen); len(got) != 0 {
		t.Errorf("a refused restore must not contact the runtime, got %v", got)
	}
}

// TestIssue329_SetContextHelperRespectsCustomPolicy covers the helper the
// server provisions for itself: it installs a function and an inject node
// straight through the client, so a policy naming either type has to stop
// the provisioning before anything is deployed.
func TestIssue329_SetContextHelperRespectsCustomPolicy(t *testing.T) {
	srv, seen := issue329Runtime(t, nil)
	s := issue329Server(t, srv, "function")

	res, err := s.handleSetContext(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"scope": "global",
			"key":   "k",
			"value": `"v"`,
		}},
	})
	if err != nil {
		t.Fatalf("handleSetContext returned err=%v", err)
	}
	assertIssue329Denied(t, res, "function")

	if got := issue329Requests(seen); len(got) != 0 {
		t.Errorf("a refused helper provisioning must not contact the runtime, got %v", got)
	}
}

// TestIssue329_RefusalContract pins the contract every refusal owes the
// caller, on the three paths where the refusal can be produced in more than
// one place:
//
//   - the refusal is typed (*nodered.DeniedNodeError), so the MCP layer can
//     recognise it and render one canonical message;
//   - the message is that canonical wording, naming MCP_NODE_DENYLIST and the
//     offending type; and
//   - nothing reaches the runtime and no backup is written.
//
// The instance case matters because the subflow lookup used to happen
// before the policy was consulted: the refusal was correct but the runtime
// had already been contacted.
