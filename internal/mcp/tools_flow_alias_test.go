package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func newFlowAliasMCPClient(t *testing.T, s *Server) *client.Client {
	t.Helper()
	c, err := client.NewInProcessClient(s.mcpServer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.Initialize(context.Background(), mcp.InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFlowIDAliasDiscovery(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unexpected request", http.StatusBadRequest)
	})
	c := newFlowAliasMCPClient(t, s)
	listing, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	schemas := make(map[string]mcp.ToolInputSchema, len(listing.Tools))
	for _, tool := range listing.Tools {
		schemas[tool.Name] = tool.InputSchema
	}
	for _, tc := range []struct {
		tools     []string
		wantID    bool
		wantAlias bool
	}{
		{[]string{"get_flow", "export_flow", "update_flow", "delete_flow", "disable_flow", "enable_flow"}, true, true},
		{[]string{"get_context", "set_context"}, true, false},
		{[]string{"add_node", "update_node", "delete_node", "connect_nodes", "instantiate_subflow", "get_node_status"}, false, true},
	} {
		for _, name := range tc.tools {
			t.Run(name, func(t *testing.T) {
				schema, ok := schemas[name]
				if !ok {
					t.Fatalf("tools/list did not return %s", name)
				}
				_, hasID := schema.Properties["id"]
				_, hasAlias := schema.Properties["flow_id"]
				if hasID != tc.wantID || hasAlias != tc.wantAlias {
					t.Errorf("id/flow_id present = %v/%v, want %v/%v", hasID, hasAlias, tc.wantID, tc.wantAlias)
				}
				if tc.wantID && tc.wantAlias && (slices.Contains(schema.Required, "id") || slices.Contains(schema.Required, "flow_id")) {
					t.Errorf("required = %v; neither alias can be individually required", schema.Required)
				}
				if name == "update_flow" && !slices.Contains(schema.Required, "flow") {
					t.Errorf("required = %v, want flow to remain required", schema.Required)
				}
			})
		}
	}
}

func TestDisableEnableFlowRejectInvalidPathIDsBeforeHTTP(t *testing.T) {
	for _, tool := range []string{"disable_flow", "enable_flow"} {
		for _, id := range []string{"..", "a/b"} {
			t.Run(tool+"/"+id, func(t *testing.T) {
				var calls atomic.Int32
				s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.URL.Path == "/flows" {
						_, _ = w.Write([]byte(`{"rev":"r","flows":[{"id":"tab","type":"tab"}]}`))
						return
					}
					http.NotFound(w, r)
				})
				c := newFlowAliasMCPClient(t, s)
				result, err := c.CallTool(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{
					Name: tool, Arguments: map[string]any{"id": id},
				}})
				if err != nil {
					t.Fatal(err)
				}
				want := "calling Node-RED: flow id must be a single path segment (must not contain /, \\, %, or be . or ..), got \"" + id + "\""
				if result == nil || !result.IsError || len(result.Content) == 0 || result.Content[0].(mcp.TextContent).Text != want {
					t.Errorf("result = %#v, want exact error %q", result, want)
				}
				if got := calls.Load(); got != 0 {
					t.Errorf("HTTP calls = %d, want 0", got)
				}
			})
		}
	}
}

func TestFlowIDAliasesValidateBeforeHTTP(t *testing.T) {
	const tab = `{"id":"tab","type":"tab","disabled":false,"nodes":[]}`
	const flows = `{"rev":"r","flows":[{"id":"tab","type":"tab","disabled":false}]}`
	tools := []string{"get_flow", "export_flow", "update_flow", "delete_flow", "disable_flow", "enable_flow"}
	cases := []struct {
		name      string
		args      map[string]any
		wantError string
	}{
		{"id", map[string]any{"id": "tab"}, ""},
		{"alias", map[string]any{"flow_id": "tab"}, ""},
		{"both equal", map[string]any{"id": "tab", "flow_id": "tab"}, ""},
		{"conflict", map[string]any{"id": "one", "flow_id": "two"}, "id and flow_id must match"},
		{"missing", map[string]any{}, "id or flow_id is required"},
	}
	for _, tool := range tools {
		for _, tc := range cases {
			t.Run(tool+"/"+tc.name, func(t *testing.T) {
				var calls atomic.Int32
				fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					switch r.URL.Path {
					case "/flows":
						_, _ = w.Write([]byte(flows))
					case "/flow/tab":
						_, _ = w.Write([]byte(tab))
					default:
						http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
					}
				}))
				defer fixture.Close()
				nr, err := nodered.NewClient(nodered.Options{SkipVersionProbe: true, BaseURL: fixture.URL, BackupDir: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				c := newFlowAliasMCPClient(t, New(nr, Options{Version: "test"}))
				ctx := context.Background()
				args := map[string]any{}
				for k, v := range tc.args {
					args[k] = v
				}
				if tool == "update_flow" {
					args["flow"] = map[string]any{"id": "tab", "type": "tab", "nodes": []any{}}
				}
				result, err := c.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: tool, Arguments: args}})
				if err != nil {
					t.Fatal(err)
				}
				if tc.wantError != "" {
					if result == nil || !result.IsError {
						t.Errorf("result = %#v, want validation error", result)
					} else if len(result.Content) == 0 || !strings.Contains(result.Content[0].(mcp.TextContent).Text, tc.wantError) {
						t.Errorf("result = %#v, want error containing %q", result, tc.wantError)
					}
					if got := calls.Load(); got != 0 {
						t.Errorf("HTTP calls = %d, want 0", got)
					}
				} else {
					if result == nil || result.IsError {
						t.Errorf("result = %#v, want non-error", result)
					}
					if got := calls.Load(); got == 0 {
						t.Errorf("HTTP calls = %d, want positive", got)
					}
				}
			})
		}
	}
}
