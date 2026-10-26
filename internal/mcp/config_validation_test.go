package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// Issue #334 at the tool boundary: the guards have to say that nothing
// was deployed, and the recovery path for a restore has to be explicit.

// A full deployment whose references do not resolve is refused, and the
// runtime is never contacted.
func TestHandleSetFlows_RefusesConfigurationWithDanglingWire(t *testing.T) {
	var seen []string
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		http.Error(w, "the runtime must not be contacted", http.StatusBadRequest)
	})
	res, err := call(t, s.handleSetFlows, map[string]any{
		"flows": `[
		  {"id":"t1","type":"tab","label":"Home"},
		  {"id":"n1","type":"inject","z":"t1","x":140,"y":140,"wires":[["ghost"]]}
		]`,
	})
	if err != nil {
		t.Fatalf("handleSetFlows returned err=%v", err)
	}
	if !res.IsError {
		t.Fatalf("expected an error result, got %+v", res.Content)
	}
	text := resultFirstText(res)
	for _, want := range []string{"not deployed", "dangling_wire", "ghost"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected the error to mention %q: %s", want, text)
		}
	}
	if len(seen) != 0 {
		t.Errorf("a refused deployment must not contact the runtime, got %v", seen)
	}
}

// A restore of a backup whose references do not resolve is refused, and
// the error says how to install it on purpose.
func TestHandleRestoreBackup_RefusesBackupThatDoesNotValidate(t *testing.T) {
	backupDir := t.TempDir()
	name := "flows-20260101-000000.000000000-000001.json"
	doc := `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"n1","type":"inject","z":"t1","x":140,"y":140,"wires":[["ghost"]]}
	]`
	if err := os.WriteFile(filepath.Join(backupDir, name), []byte(doc), 0o600); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	var seen []string
	s := serverWithBackupDir(t, backupDir, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		http.Error(w, "the runtime must not be contacted", http.StatusBadRequest)
	})

	res, err := call(t, s.handleRestoreBackup, map[string]any{"backup": name})
	if err != nil {
		t.Fatalf("handleRestoreBackup returned err=%v", err)
	}
	if !res.IsError {
		t.Fatalf("expected an error result, got %+v", res.Content)
	}
	text := resultFirstText(res)
	if !strings.Contains(text, "force=true") {
		t.Errorf("expected the error to name the recovery path: %s", text)
	}
	if len(seen) != 0 {
		t.Errorf("a refused restore must not contact the runtime, got %v", seen)
	}
}

// The recovery path: force=true installs a backup that no longer
// validates, and the result says that it was installed unvalidated.
func TestHandleRestoreBackup_ForceInstallsABackupThatDoesNotValidate(t *testing.T) {
	backupDir := t.TempDir()
	name := "flows-20260101-000000.000000000-000001.json"
	doc := `[
	  {"id":"t1","type":"tab","label":"Home"},
	  {"id":"n1","type":"inject","z":"t1","x":140,"y":140,"wires":[["ghost"]]}
	]`
	if err := os.WriteFile(filepath.Join(backupDir, name), []byte(doc), 0o600); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	var posted bool
	s := serverWithBackupDir(t, backupDir, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/flows":
			_, _ = io.WriteString(w, `[{"id":"cur","type":"tab","label":"Current"}]`)
		case r.Method == http.MethodPost && r.URL.Path == "/flows":
			posted = true
			_, _ = io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})

	res, err := call(t, s.handleRestoreBackup, map[string]any{"backup": name, "force": true})
	if err != nil {
		t.Fatalf("handleRestoreBackup returned err=%v", err)
	}
	if res.IsError {
		t.Fatalf("expected a successful result, got %+v", res.Content)
	}
	if !posted {
		t.Error("the backup was not deployed")
	}
	text := resultFirstText(res)
	if !strings.Contains(text, "WITHOUT validating") {
		t.Errorf("expected the result to say the backup was installed unvalidated: %s", text)
	}
}

// A subflow definition with a wire that points nowhere is refused, and
// the error says that the runtime was never contacted.
func TestHandleCreateSubflow_RefusesDefinitionWithDanglingWire(t *testing.T) {
	var seen []string
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		http.Error(w, "the runtime must not be contacted", http.StatusBadRequest)
	})
	res, err := call(t, s.handleCreateSubflow, map[string]any{
		"subflow": `{
		  "id":"sf_a","type":"subflow","name":"A","in":[],"out":[],
		  "nodes":[{"id":"inner","type":"inject","z":"sf_a","x":140,"y":140,"wires":[["ghost"]]}]
		}`,
	})
	if err != nil {
		t.Fatalf("handleCreateSubflow returned err=%v", err)
	}
	if !res.IsError {
		t.Fatalf("expected an error result, got %+v", res.Content)
	}
	text := resultFirstText(res)
	if !strings.Contains(text, "before the runtime was contacted") {
		t.Errorf("expected the error to say the runtime was not contacted: %s", text)
	}
	if !strings.Contains(text, "ghost") {
		t.Errorf("expected the error to name the dangling target: %s", text)
	}
	if len(seen) != 0 {
		t.Errorf("a refused write must not contact the runtime, got %v", seen)
	}
}

// The tool descriptions have to advertise the guard and the recovery
// path, so a caller knows both before it tries.
func TestConfigValidationGuardIsAdvertised(t *testing.T) {
	s := newTestServer(t, false)
	byName := map[string]string{}
	for _, tool := range s.tools {
		byName[tool.Name] = tool.Description
	}
	setFlows := byName["set_flows"]
	for _, want := range []string{"validated", "refused"} {
		if !strings.Contains(setFlows, want) {
			t.Errorf("set_flows description missing %q: %s", want, setFlows)
		}
	}
	restore := byName["restore_backup"]
	for _, want := range []string{"force=true", "validated"} {
		if !strings.Contains(restore, want) {
			t.Errorf("restore_backup description missing %q: %s", want, restore)
		}
	}
	for _, name := range []string{"create_subflow", "update_subflow"} {
		desc := byName[name]
		if !strings.Contains(desc, "same rules a tab is") {
			t.Errorf("%s description does not advertise the shared guard: %s", name, desc)
		}
	}
}

// serverWithBackupDir builds a server whose client reads its backups
// from the given directory, so a restore test can write a real backup
// file.
func serverWithBackupDir(t *testing.T, backupDir string, h http.HandlerFunc) *Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: backupDir})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return New(c, Options{Version: "test"})
}

// The escape hatch of set_flows: a configuration the guard refuses can
// be deployed on purpose.
func TestHandleSetFlows_ForceDeploysAConfigurationThatDoesNotValidate(t *testing.T) {
	var posted bool
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/flows":
			_, _ = io.WriteString(w, `[{"id":"cur","type":"tab","label":"Current"}]`)
		case r.Method == http.MethodPost && r.URL.Path == "/flows":
			posted = true
			_, _ = io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	res, err := call(t, s.handleSetFlows, map[string]any{
		"flows": `[
		  {"id":"t1","type":"tab","label":"Home"},
		  {"id":"n1","type":"inject","z":"t1","x":140,"y":140,"wires":[["ghost"]]}
		]`,
		"force": true,
	})
	if err != nil {
		t.Fatalf("handleSetFlows returned err=%v", err)
	}
	if res.IsError {
		t.Fatalf("expected a successful result, got %+v", res.Content)
	}
	if !posted {
		t.Error("the configuration was not deployed")
	}
	if text := resultFirstText(res); !strings.Contains(text, "WITHOUT validating") {
		t.Errorf("expected the result to say the configuration was deployed unvalidated: %s", text)
	}
}

// A restore of a backup with no tab at all would leave the runtime with
// no tabs, so it is refused the same way a deployment is.
func TestHandleRestoreBackup_RefusesABackupWithNoTab(t *testing.T) {
	backupDir := t.TempDir()
	name := "flows-20260101-000000.000000000-000001.json"
	doc := `[{"id":"n1","type":"inject","z":"t1","x":140,"y":140,"wires":[]}]`
	if err := os.WriteFile(filepath.Join(backupDir, name), []byte(doc), 0o600); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	var seen []string
	s := serverWithBackupDir(t, backupDir, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		http.Error(w, "the runtime must not be contacted", http.StatusBadRequest)
	})
	res, err := call(t, s.handleRestoreBackup, map[string]any{"backup": name})
	if err != nil {
		t.Fatalf("handleRestoreBackup returned err=%v", err)
	}
	if !res.IsError {
		t.Fatalf("expected an error result, got %+v", res.Content)
	}
	if !strings.Contains(resultFirstText(res), "tab") {
		t.Errorf("expected the error to name the missing tab: %s", resultFirstText(res))
	}
	if len(seen) != 0 {
		t.Errorf("a refused restore must not contact the runtime, got %v", seen)
	}
}
