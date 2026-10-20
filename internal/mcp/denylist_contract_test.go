package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// ---------------------------------------------------------------------------
// The contract every refusal owes the caller.
//
// The dedicated tests in this file cover the properties a refusal must
// hold wherever it is produced: it is typed, it carries the canonical
// wording, it reaches the operator before the runtime is touched, and it
// leaves no backup behind.
//
// The shared fixtures live in denylist_test.go.
// ---------------------------------------------------------------------------

func TestIssue329_RefusalContract(t *testing.T) {
	for _, nodeType := range []string{"function", "inject", "subflow:sf1"} {
		t.Run(nodeType, func(t *testing.T) {
			srv, seen := issue329Runtime(t, map[string]string{
				"GET /flow/global": `{"id":"global","subflows":[{"id":"sf1","type":"subflow","name":"sf","in":[],"out":[],"nodes":[]}],"configs":[]}`,
			})
			s := issue329Server(t, srv, nodeType)

			var res *mcp.CallToolResult
			var err error
			switch nodeType {
			case "subflow:sf1":
				// The handler renders the refusal into the tool result.
				res, err = s.handleInstantiateSubflow(context.Background(), mcp.CallToolRequest{
					Params: mcp.CallToolParams{Arguments: map[string]any{
						"flow_id": "tabA", "subflow_id": "sf1",
					}},
				})
				if err != nil {
					t.Fatalf("handleInstantiateSubflow returned err=%v", err)
				}
				if res == nil || !res.IsError {
					t.Fatalf("expected an error result, got %+v", res)
				}
			default:
				// The helper refuses while provisioning, so the typed
				// error surfaces from the provisioning call itself.
				_, _, err = s.ensureSetContextHelper(context.Background())
				var denied *nodered.DeniedNodeError
				if !errors.As(err, &denied) {
					t.Fatalf("expected a typed *nodered.DeniedNodeError, got %T: %v", err, err)
				}
				if denied.NodeType != nodeType {
					t.Errorf("refusal names node type %q, want %q", denied.NodeType, nodeType)
				}
			}

			want := fmt.Sprintf("node type %q is in MCP_NODE_DENYLIST; remove it from the denylist or use a different node type (see SECURITY.md)", nodeType)
			if res != nil {
				if got := resultFirstText(res); got != want {
					t.Errorf("canonical message = %q, want %q", got, want)
				}
			}

			if got := issue329Requests(seen); len(got) != 0 {
				t.Errorf("a refused write must not contact the runtime, got %v", got)
			}
			backups, err := s.nrClient.ListBackups()
			if err != nil {
				t.Fatalf("ListBackups: %v", err)
			}
			if len(backups) != 0 {
				t.Errorf("a refused write must not take a backup, got %v", backups)
			}
		})
	}
}

// TestIssue329_InstanceMembersRefusedBeforeLookup covers the instance
// surface from the member side: the caller's params are merged into the
// instance node, so a forbidden type nested in them travels with the node.
// The refusal has to happen before the subflow lookup, otherwise the
// runtime is contacted for a write that was never going to happen.
func TestIssue329_InstanceMembersRefusedBeforeLookup(t *testing.T) {
	srv, seen := issue329Runtime(t, map[string]string{
		"GET /flow/global": `{"id":"global","subflows":[{"id":"sf1","type":"subflow","name":"sf","in":[],"out":[],"nodes":[]}],"configs":[]}`,
	})
	s := issue329Server(t, srv, "exec")

	res, err := s.handleInstantiateSubflow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"flow_id":    "tabA",
			"subflow_id": "sf1",
			"params":     `{"id":"i1","nodes":[{"type":"exec"}]}`,
		}},
	})
	if err != nil {
		t.Fatalf("handleInstantiateSubflow returned err=%v", err)
	}
	assertIssue329Denied(t, res, "exec")

	if got := issue329Requests(seen); len(got) != 0 {
		t.Errorf("a refused instance must not contact the runtime, got %v", got)
	}
}

// TestIssue329_PatchedNodeMembersAreRefused covers the patched node from the
// member side: a patch can carry a member collection of its own, and the
// node as it will be deployed then holds it. Judging only the node's own
// top-level type would let that through.
func TestIssue329_PatchedNodeMembersAreRefused(t *testing.T) {
	for _, key := range []string{"nodes", "configs", "subflows"} {
		t.Run(key, func(t *testing.T) {
			srv, seen := issue329Runtime(t, map[string]string{
				"GET /flow/tabA": `{"id":"tabA","label":"A","nodes":[{"id":"n1","type":"inject","z":"tabA","x":140,"y":140,"wires":[]}]}`,
			})
			s := issue329Server(t, srv, "exec")

			res, err := s.handleUpdateNode(context.Background(), mcp.CallToolRequest{
				Params: mcp.CallToolParams{Arguments: map[string]any{
					"flow_id":    "tabA",
					"node_id":    "n1",
					"properties": `{"` + key + `":[{"id":"p1","type":"exec"}]}`,
				}},
			})
			if err != nil {
				t.Fatalf("handleUpdateNode returned err=%v", err)
			}
			assertIssue329Denied(t, res, "exec")

			for _, req := range issue329Requests(seen) {
				if !strings.HasPrefix(req, "GET ") {
					t.Errorf("a refused patch must not deploy, got %v", issue329Requests(seen))
				}
			}
		})
	}
}

// TestIssue329_WiringTouchesForbiddenNodeIsRefused covers the wiring
// surface: a wire into or out of a forbidden node is what puts it in a
// position to run, so wiring one is refused the way patching one is.
//
// The tab has to be read to know what the wire touches, so the refusal
// follows that read — but nothing is deployed and no backup is taken.
func TestIssue329_WiringTouchesForbiddenNodeIsRefused(t *testing.T) {
	exec := `{"id":"n1","type":"exec","z":"tabA","x":140,"y":140,"command":"id","wires":[]}`
	other := `{"id":"n2","type":"debug","z":"tabA","x":240,"y":140,"wires":[]}`

	srv, seen := issue329Runtime(t, map[string]string{
		"GET /flow/tabA": `{"id":"tabA","label":"A","nodes":[` + exec + `,` + other + `]}`,
	})
	s := issue329Server(t, srv, "exec")

	res, err := s.handleConnectNodes(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"flow_id": "tabA", "from_id": "n1", "port": 0, "to_id": "n2",
		}},
	})
	if err != nil {
		t.Fatalf("handleConnectNodes returned err=%v", err)
	}
	assertIssue329Denied(t, res, "exec")

	for _, req := range issue329Requests(seen) {
		if !strings.HasPrefix(req, "GET ") {
			t.Errorf("a refused wiring must not deploy, got %v", issue329Requests(seen))
		}
	}
	backups, err := s.nrClient.ListBackups()
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(backups) != 0 {
		t.Errorf("a refused wiring must not take a backup, got %v", backups)
	}
}

// TestIssue329_InstanceParamsErrorPrecedence pins a deliberate consequence
// of refusing before the runtime is contacted: a malformed instance payload
// is now reported as such, instead of the subflow lookup error that used
// to come first. Both are errors for an invalid call; the new one does not
// contact the runtime.
func TestIssue329_InstanceParamsErrorPrecedence(t *testing.T) {
	srv, seen := issue329Runtime(t, nil)
	s := issue329Server(t, srv, "exec")

	res, err := s.handleInstantiateSubflow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"flow_id": "tabA", "subflow_id": "missing", "params": `[]`,
		}},
	})
	if err != nil {
		t.Fatalf("handleInstantiateSubflow returned err=%v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result, got %+v", res)
	}
	if got := resultFirstText(res); !strings.Contains(got, "instance is not a JSON object") {
		t.Errorf("error must name the malformed instance payload, got %q", got)
	}
	if got := issue329Requests(seen); len(got) != 0 {
		t.Errorf("a refused instance must not contact the runtime, got %v", got)
	}
}

// TestIssue329_AllowedTypesStillDeploy guards the other direction: the
// control must not turn into a blanket refusal. A node type the policy
// allows still reaches the runtime.
func TestIssue329_AllowedTypesStillDeploy(t *testing.T) {
	var deployed []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/flow":
			deployed, _ = io.ReadAll(r.Body)
			_, _ = w.Write(deployed)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	s := issue329Server(t, srv, "exec")

	res, err := s.handleCreateFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"flow": `{"label":"safe","nodes":[{"id":"i1","type":"inject","z":"safe","x":140,"y":140,"wires":[]}]}`,
		}},
	})
	if err != nil {
		t.Fatalf("handleCreateFlow returned err=%v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("an allowed node type must be accepted, got %+v", res)
	}
	if !strings.Contains(string(deployed), `"type":"inject"`) {
		t.Errorf("the allowed node must reach the runtime, got %s", deployed)
	}
}

// TestIssue329_ReadOnlyDoesNotReplaceTheControl states the issue's
// acceptance rule: read-only mode is not a substitute for the denylist.
// With writes enabled the denylist still holds.
func TestIssue329_ReadOnlyDoesNotReplaceTheControl(t *testing.T) {
	srv, seen := issue329Runtime(t, nil)
	s := issue329Server(t, srv, "exec")

	res, err := s.handleCreateFlow(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"flow": `{"label":"pwn","nodes":[` + issue329ExecNode + `]}`,
		}},
	})
	if err != nil {
		t.Fatalf("handleCreateFlow returned err=%v", err)
	}
	assertIssue329Denied(t, res, "exec")

	if got := issue329Requests(seen); len(got) != 0 {
		t.Errorf("a refused write must not contact the runtime, got %v", got)
	}
}
