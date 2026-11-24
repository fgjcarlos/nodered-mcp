package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

func TestFunctionSyntaxForceToolsRefuseBeforeWrite(t *testing.T) {
	const invalidFlows = `[{"id":"tab1","type":"tab"},{"id":"fn","type":"function","z":"tab1","x":1,"y":1,"func":"const = ;","wires":[]}]`
	for _, tool := range []string{"set_flows", "restore_backup"} {
		t.Run(tool, func(t *testing.T) {
			dir := t.TempDir()
			args := map[string]any{"force": true}
			if tool == "set_flows" {
				args["flows"] = invalidFlows
			} else {
				args["backup"] = "invalid.json"
				if err := os.WriteFile(filepath.Join(dir, "invalid.json"), []byte(invalidFlows), 0600); err != nil {
					t.Fatal(err)
				}
			}
			writes := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost || r.Method == http.MethodPut {
					writes++
				}
				if r.Method == http.MethodGet && r.URL.Path == "/flows" {
					_, _ = w.Write([]byte(`[{"id":"tab1","type":"tab"}]`))
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()
			client, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: dir, SkipVersionProbe: true})
			if err != nil {
				t.Fatal(err)
			}
			result, err := callHelperTool(context.Background(), New(client, Options{Version: "test"}), tool, args)
			if err != nil {
				t.Fatal(err)
			}
			if result == nil || len(result.Content) == 0 {
				t.Fatalf("%s returned no content", tool)
			}
			body, _ := json.Marshal(result.Content)
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			var artifacts []string
			for _, entry := range entries {
				if entry.Name() != "invalid.json" {
					artifacts = append(artifacts, entry.Name())
				}
			}
			if writes != 0 || len(artifacts) != 0 {
				t.Fatalf("%s refusal side effects: writes=%d backup/artifacts=%v", tool, writes, artifacts)
			}
			if !strings.Contains(string(body), `SyntaxError: node \"fn\" field \"func\" at line 1, column 7`) {
				t.Fatalf("%s result = %s", tool, body)
			}
		})
	}
}

func TestFunctionSyntaxRegisteredToolsRefuseBeforeWrite(t *testing.T) {
	const invalidTab = `{"id":"tab1","label":"Home","nodes":[{"id":"fn","type":"function","z":"tab1","x":1,"y":1,"func":"const = ;","wires":[]}]}`
	const invalidFlows = `[{"id":"tab1","type":"tab"},{"id":"fn","type":"function","z":"tab1","x":1,"y":1,"func":"const = ;","wires":[]}]`
	const invalidSubflow = `{"id":"sf1","type":"subflow","nodes":[{"id":"fn","type":"function","z":"sf1","x":1,"y":1,"func":"const = ;","wires":[]}],"in":[],"out":[]}`
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"validate_flow", map[string]any{"flow": invalidTab}},
		{"create_flow", map[string]any{"flow": invalidTab}},
		{"update_flow", map[string]any{"id": "tab1", "flow": invalidTab}},
		{"set_flows", map[string]any{"flows": invalidFlows}},
		{"create_subflow", map[string]any{"subflow": invalidSubflow}},
		{"update_subflow", map[string]any{"id": "sf1", "subflow": invalidSubflow}},
		{"restore_backup", map[string]any{"backup": "invalid.json"}},
		{"update_node", map[string]any{"flow_id": "tab1", "node_id": "fn", "properties": `{"func":"const = ;"}`}},
		{"add_node", map[string]any{"flow_id": "tab1", "node": `{"id":"newfn","type":"function","x":1,"y":1,"func":"const = ;","wires":[]}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.name == "restore_backup" {
				if err := os.WriteFile(filepath.Join(dir, "invalid.json"), []byte(invalidFlows), 0600); err != nil {
					t.Fatal(err)
				}
			}
			writes := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost || r.Method == http.MethodPut {
					writes++
				}
				switch r.Method + " " + r.URL.Path {
				case "GET /flows":
					_, _ = w.Write([]byte(`{"rev":"r","flows":[{"id":"tab1","type":"tab"},{"id":"fn","type":"function","z":"tab1","x":1,"y":1,"func":"return msg;","wires":[]}]}`))
				case "GET /flow/global":
					global := `{"id":"global","subflows":[],"configs":[]}`
					if tc.name == "update_subflow" {
						global = `{"id":"global","subflows":[{"id":"sf1","type":"subflow","nodes":[],"in":[],"out":[]}],"configs":[]}`
					}
					_, _ = w.Write([]byte(global))
				default:
					w.WriteHeader(http.StatusNoContent)
				}
			}))
			defer srv.Close()
			client, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: dir, SkipVersionProbe: true})
			if err != nil {
				t.Fatal(err)
			}
			s := New(client, Options{Version: "test"})
			res, err := callHelperTool(context.Background(), s, tc.name, tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if res == nil || len(res.Content) == 0 {
				t.Fatalf("%s returned no content", tc.name)
			}
			text, err := json.Marshal(res.Content)
			if err != nil {
				t.Fatal(err)
			}
			if writes != 0 {
				t.Fatalf("%s issued %d write requests", tc.name, writes)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() != "invalid.json" {
					t.Fatalf("%s created backup/artifact %q", tc.name, entry.Name())
				}
			}
			if !strings.Contains(string(text), "SyntaxError") || !strings.Contains(string(text), `field`) {
				t.Fatalf("%s did not report function syntax: %s", tc.name, text)
			}
			if tc.name == "validate_flow" && !strings.Contains(string(text), "function_syntax") {
				t.Fatalf("validate_flow did not report function_syntax: %s", text)
			}
		})
	}
}
