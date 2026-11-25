package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func assertStructuralRecoveryNotice(t *testing.T, text string) {
	t.Helper()
	for _, want := range []string{"structural", "credentials", "encrypted credential store", "credentialSecret", "Before", "external", "re-enter"} {
		if !strings.Contains(text, want) {
			t.Errorf("recovery notice missing %q: %s", want, text)
		}
	}
}

func TestBackupRecoveryLimitsAdvertisedBeforeDeletion(t *testing.T) {
	for _, mode := range []struct {
		name     string
		readOnly bool
	}{{"read_write", false}, {"read_only", true}} {
		t.Run(mode.name, func(t *testing.T) {
			ctx := context.Background()
			s := newTestServer(t, mode.readOnly)
			c, err := client.NewInProcessClient(s.mcpServer)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = c.Close() })
			init, err := c.Initialize(ctx, mcp.InitializeRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if mode.readOnly {
				if init.Instructions != "" {
					t.Errorf("read-only server unexpectedly advertises instructions: %q", init.Instructions)
				}
				return
			}
			if got := strings.Count(init.Instructions, backupRecoveryNotice); got != 1 {
				t.Errorf("initialize advertises the full backup recovery notice %d times, want 1", got)
			}
			listing, err := c.ListTools(ctx, mcp.ListToolsRequest{})
			if err != nil {
				t.Fatal(err)
			}
			descriptions := make(map[string]string, len(listing.Tools))
			for _, tool := range listing.Tools {
				if strings.Contains(tool.Description, backupRecoveryNotice) {
					t.Errorf("%s repeats the full recovery notice in tools/list", tool.Name)
				}
				descriptions[tool.Name] = tool.Description
			}
			total := 0
			for _, name := range []string{
				"create_flow", "import_flow", "update_flow", "add_node", "update_node", "delete_node",
				"connect_nodes", "delete_flow", "disable_flow", "enable_flow", "create_subflow", "update_subflow",
				"delete_subflow", "instantiate_subflow", "restore_backup", "set_flows_state", "set_flows",
			} {
				desc := descriptions[name]
				total += len(desc)
				if !strings.Contains(desc, "do not include credentials") || !strings.Contains(desc, "can permanently remove") {
					t.Errorf("%s description is missing the short credential warning", name)
				}
				if strings.Contains(desc, "restore can itself be undone") {
					t.Errorf("%s description still promises complete reversibility", name)
				}
			}
			t.Logf("backup-related tools/list descriptions: %d bytes", total)
			if total > 14_000 {
				t.Errorf("backup-related descriptions total %d bytes, want at most 14000", total)
			}
		})
	}
}

// Model the Admin API's separate credential store: deleting the tab clears it,
// while deploying the /flows snapshot cannot recreate it. No real secrets used.
func TestBackupRecoveryLimitsDeleteRestoreCredentials(t *testing.T) {
	const original = `[{"id":"tab1","type":"tab"},{"id":"broker","type":"mqtt-broker","z":"tab1"}]`
	flows := original
	credentials := `{"user":"audit-user","has_password":true}`
	var requests []string
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/flows":
			_, _ = io.WriteString(w, flows)
		case r.Method == http.MethodDelete && r.URL.Path == "/flow/tab1":
			flows, credentials = `[]`, `{}`
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/flows":
			body, _ := io.ReadAll(r.Body)
			flows = string(body)
			if r.Header.Get("Node-RED-Deployment-Type") != "full" {
				t.Error("restore must remain a full deployment")
			}
			_, _ = io.WriteString(w, `{}`)
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	})
	deleted, err := call(t, s.handleDeleteFlow, map[string]any{"id": "tab1"})
	if err != nil || deleted.IsError {
		t.Fatalf("delete: %+v, %v", deleted, err)
	}
	assertStructuralRecoveryNotice(t, deleted.Content[0].(mcp.TextContent).Text)
	if flows != `[]` || credentials != `{}` {
		t.Fatal("delete did not clear tab and credentials")
	}
	restored, err := call(t, s.handleRestoreBackup, map[string]any{"backup": "latest"})
	if err != nil || restored.IsError {
		t.Fatalf("restore: %+v, %v", restored, err)
	}
	assertStructuralRecoveryNotice(t, restored.Content[0].(mcp.TextContent).Text)
	var got, want any
	if err := json.Unmarshal([]byte(flows), &got); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(original), &want)
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) || credentials != `{}` {
		t.Fatalf("restore must recover structure only: flows=%s credentials=%s", flows, credentials)
	}
	if got := strings.Join(requests, ","); got != "GET /flows,DELETE /flow/tab1,GET /flows,POST /flows" {
		t.Fatalf("unexpected backup/delete/restore sequence: %s", got)
	}
}
