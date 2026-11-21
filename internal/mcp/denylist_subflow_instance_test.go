package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// A subflow instance node (type "subflow:<id>") runs the nodes of its
// definition, so the denylist judges it by that definition: a write that
// would deploy an instance of a definition carrying a denied type — directly
// or through nested instances — is refused before any backup or runtime
// write, even when the definition is not part of the submitted document but
// already lives in the runtime.

// Runtime definitions, flat as GET /flows serves them:
//   - sfx carries an exec node directly;
//   - sfa carries an instance of sfb, and sfb carries the exec node;
//   - sfc and sfd instantiate each other, and sfd carries the exec node
//     after its instance of sfc, so only a cycle guard reaches it;
//   - sfok carries nothing denied.
var subflowInstanceRuntimeEntries = []string{
	`{"id":"t1","type":"tab","label":"T1"}`,
	`{"id":"n1","type":"inject","z":"t1","x":100,"y":100,"wires":[[]]}`,
	`{"id":"sfx","type":"subflow","name":"x","in":[],"out":[]}`,
	`{"id":"ex","type":"exec","z":"sfx","x":10,"y":10,"command":"id","wires":[[],[],[]]}`,
	`{"id":"sfa","type":"subflow","name":"a","in":[],"out":[]}`,
	`{"id":"ia","type":"subflow:sfb","z":"sfa","x":10,"y":10,"wires":[]}`,
	`{"id":"sfb","type":"subflow","name":"b","in":[],"out":[]}`,
	`{"id":"eb","type":"exec","z":"sfb","x":10,"y":10,"command":"id","wires":[[],[],[]]}`,
	`{"id":"sfc","type":"subflow","name":"c","in":[],"out":[]}`,
	`{"id":"ic","type":"subflow:sfd","z":"sfc","x":10,"y":10,"wires":[]}`,
	`{"id":"sfd","type":"subflow","name":"d","in":[],"out":[]}`,
	`{"id":"id1","type":"subflow:sfc","z":"sfd","x":10,"y":10,"wires":[]}`,
	`{"id":"ed","type":"exec","z":"sfd","x":10,"y":10,"command":"id","wires":[[],[],[]]}`,
	`{"id":"sfok","type":"subflow","name":"ok","in":[],"out":[]}`,
}

// The same definitions, nested as GET /flow/global serves them.
const subflowInstanceGlobal = `{"id":"global","configs":[],"subflows":[
	{"id":"sfx","type":"subflow","name":"x","in":[],"out":[],"nodes":[{"id":"ex","type":"exec","z":"sfx","x":10,"y":10,"command":"id","wires":[[],[],[]]}]},
	{"id":"sfa","type":"subflow","name":"a","in":[],"out":[],"nodes":[{"id":"ia","type":"subflow:sfb","z":"sfa","x":10,"y":10,"wires":[]}]},
	{"id":"sfb","type":"subflow","name":"b","in":[],"out":[],"nodes":[{"id":"eb","type":"exec","z":"sfb","x":10,"y":10,"command":"id","wires":[[],[],[]]}]},
	{"id":"sfc","type":"subflow","name":"c","in":[],"out":[],"nodes":[{"id":"ic","type":"subflow:sfd","z":"sfc","x":10,"y":10,"wires":[]}]},
	{"id":"sfd","type":"subflow","name":"d","in":[],"out":[],"nodes":[{"id":"id1","type":"subflow:sfc","z":"sfd","x":10,"y":10,"wires":[]},{"id":"ed","type":"exec","z":"sfd","x":10,"y":10,"command":"id","wires":[[],[],[]]}]},
	{"id":"sfok","type":"subflow","name":"ok","in":[],"out":[],"nodes":[]}
]}`

// subflowInstanceServer wires a Server with the default denylist to a
// runtime stand-in seeded with the definitions above. It returns the
// request log and the backup directory, which must both stay free of
// writes when a refusal happens.
func subflowInstanceServer(t *testing.T) (*Server, *[]string, string) {
	t.Helper()
	srv, seen := issue329Runtime(t, map[string]string{
		"GET /flows":       `{"rev":"r","flows":[` + strings.Join(subflowInstanceRuntimeEntries, ",") + `]}`,
		"GET /flow/global": subflowInstanceGlobal,
	})
	backupDir := t.TempDir()
	c, err := nodered.NewClient(nodered.Options{SkipVersionProbe: true, BaseURL: srv.URL, BackupDir: backupDir})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return New(c, Options{Version: "test", NodeDenylist: []string{"exec", "system"}}), seen, backupDir
}

// assertNoWriteAndNoBackup asserts the runtime saw only reads and that no
// backup file was written.
func assertNoWriteAndNoBackup(t *testing.T, seen *[]string, backupDir string) {
	t.Helper()
	for _, req := range issue329Requests(seen) {
		if !strings.HasPrefix(req, "GET ") {
			t.Errorf("a refused write must not reach the runtime, got %s", req)
		}
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading backup dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("a refused write must not take a backup, found %d file(s)", len(entries))
	}
}

func subflowInstanceCall(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}}
}

func subflowInstanceNode(id, subflowID, z string) string {
	return `{"id":"` + id + `","type":"subflow:` + subflowID + `","z":"` + z + `","x":200,"y":200,"wires":[]}`
}

func TestSubflowInstanceDenylist_RefusesInstanceOfDeniedDefinition(t *testing.T) {
	ctx := context.Background()
	surfaces := map[string]func(s *Server) (*mcp.CallToolResult, error){
		"instantiate_subflow": func(s *Server) (*mcp.CallToolResult, error) {
			return s.handleInstantiateSubflow(ctx, subflowInstanceCall(map[string]any{"flow_id": "t1", "subflow_id": "sfx"}))
		},
		"instantiate_subflow nested": func(s *Server) (*mcp.CallToolResult, error) {
			return s.handleInstantiateSubflow(ctx, subflowInstanceCall(map[string]any{"flow_id": "t1", "subflow_id": "sfa"}))
		},
		"instantiate_subflow cycle": func(s *Server) (*mcp.CallToolResult, error) {
			return s.handleInstantiateSubflow(ctx, subflowInstanceCall(map[string]any{"flow_id": "t1", "subflow_id": "sfc"}))
		},
		"add_node": func(s *Server) (*mcp.CallToolResult, error) {
			return s.handleAddNode(ctx, subflowInstanceCall(map[string]any{
				"flow_id": "t1", "node": issue329NodeObject(subflowInstanceNode("i1", "sfx", "t1")),
			}))
		},
		"add_node nested": func(s *Server) (*mcp.CallToolResult, error) {
			return s.handleAddNode(ctx, subflowInstanceCall(map[string]any{
				"flow_id": "t1", "node": issue329NodeObject(subflowInstanceNode("i1", "sfa", "t1")),
			}))
		},
		"add_node cycle": func(s *Server) (*mcp.CallToolResult, error) {
			return s.handleAddNode(ctx, subflowInstanceCall(map[string]any{
				"flow_id": "t1", "node": issue329NodeObject(subflowInstanceNode("i1", "sfd", "t1")),
			}))
		},
		"update_node": func(s *Server) (*mcp.CallToolResult, error) {
			return s.handleUpdateNode(ctx, subflowInstanceCall(map[string]any{
				"flow_id": "t1", "node_id": "n1", "properties": `{"type":"subflow:sfx"}`,
			}))
		},
		"update_flow": func(s *Server) (*mcp.CallToolResult, error) {
			return s.handleUpdateFlow(ctx, subflowInstanceCall(map[string]any{
				"id": "t1", "flow": `{"id":"t1","label":"T1","nodes":[` + subflowInstanceNode("i1", "sfx", "t1") + `]}`,
			}))
		},
		"create_flow": func(s *Server) (*mcp.CallToolResult, error) {
			return s.handleCreateFlow(ctx, subflowInstanceCall(map[string]any{
				"flow": `{"id":"t2","label":"T2","nodes":[` + subflowInstanceNode("i1", "sfx", "t2") + `]}`,
			}))
		},
		"import_flow": func(s *Server) (*mcp.CallToolResult, error) {
			return s.handleImportFlow(ctx, subflowInstanceCall(map[string]any{
				"clipboard": `[{"id":"tz","type":"tab","label":"Z"},` + subflowInstanceNode("i1", "sfx", "tz") + `]`,
			}))
		},
		"set_flows": func(s *Server) (*mcp.CallToolResult, error) {
			return s.handleSetFlows(ctx, subflowInstanceCall(map[string]any{
				"flows": `[` + strings.Join(subflowInstanceRuntimeEntries[:4], ",") + `,` + subflowInstanceNode("i1", "sfx", "t1") + `]`,
			}))
		},
		"create_subflow": func(s *Server) (*mcp.CallToolResult, error) {
			return s.handleCreateSubflow(ctx, subflowInstanceCall(map[string]any{
				"subflow": `{"id":"sfn","type":"subflow","name":"n","in":[],"out":[],"nodes":[` + subflowInstanceNode("in1", "sfx", "sfn") + `]}`,
			}))
		},
		"update_subflow": func(s *Server) (*mcp.CallToolResult, error) {
			return s.handleUpdateSubflow(ctx, subflowInstanceCall(map[string]any{
				"id":      "sfok",
				"subflow": `{"id":"sfok","type":"subflow","name":"ok","in":[],"out":[],"nodes":[` + subflowInstanceNode("in1", "sfa", "sfok") + `]}`,
			}))
		},
	}
	for name, call := range surfaces {
		t.Run(name, func(t *testing.T) {
			s, seen, backupDir := subflowInstanceServer(t)
			res, err := call(s)
			if err != nil {
				t.Fatalf("%s returned err=%v", name, err)
			}
			assertIssue329Denied(t, res, "exec")
			assertNoWriteAndNoBackup(t, seen, backupDir)
		})
	}
}

// restore_backup deploys a whole document, so the definition an instance
// runs is part of that document and is judged with it.
func TestSubflowInstanceDenylist_RestoreBackupRefusesInstanceOfDeniedDefinition(t *testing.T) {
	backupDir := t.TempDir()
	doc := `[` + strings.Join(subflowInstanceRuntimeEntries[:4], ",") + `,` + subflowInstanceNode("i1", "sfx", "t1") + `]`
	name := "flows-20260101-000000.000000000-000001.json"
	if err := os.WriteFile(filepath.Join(backupDir, name), []byte(doc), 0o600); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	srv, seen := issue329Runtime(t, nil)
	c, err := nodered.NewClient(nodered.Options{SkipVersionProbe: true, BaseURL: srv.URL, BackupDir: backupDir})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	s := New(c, Options{Version: "test", NodeDenylist: []string{"exec"}})
	res, err := s.handleRestoreBackup(context.Background(), subflowInstanceCall(map[string]any{"backup": name}))
	if err != nil {
		t.Fatalf("handleRestoreBackup returned err=%v", err)
	}
	assertIssue329Denied(t, res, "exec")
	if got := issue329Requests(seen); len(got) != 0 {
		t.Errorf("a refused restore must not contact the runtime, got %v", got)
	}
}
