package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

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

// Tool descriptions are advertised before a caller can choose to delete.
func TestBackupRecoveryLimitsAdvertisedBeforeDeletion(t *testing.T) {
	s := newTestServer(t, false)
	for _, tool := range s.tools {
		if strings.Contains(tool.Description, "restore_backup") || tool.Name == "restore_backup" {
			t.Run(tool.Name, func(t *testing.T) {
				assertStructuralRecoveryNotice(t, tool.Description)
				if strings.Contains(tool.Description, "restore can itself be undone") {
					t.Fatal("description still promises complete reversibility")
				}
			})
		}
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
