package nodered

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestNodePathEscaping(t *testing.T) {
	tests := []struct {
		name   string
		module string
		set    string
		want   string
	}{
		{name: "module", module: "node-red-dashboard", want: "/nodes/node-red-dashboard"},
		{name: "module and set", module: "node-red-dashboard", set: "set-name", want: "/nodes/node-red-dashboard/set-name"},
		{name: "scoped module", module: "@flowfuse/node-red-dashboard", want: "/nodes/%40flowfuse%2Fnode-red-dashboard"},
		{name: "path traversal", module: "../flows", want: "/nodes/..%2Fflows"},
		{name: "space", module: "a b", want: "/nodes/a%20b"},
		{name: "quotes", module: "name\"with\"quotes", want: "/nodes/name%22with%22quotes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nodeInfoPath(tt.module, tt.set); got != tt.want {
				t.Errorf("nodeInfoPath(%q, %q) = %q, want %q", tt.module, tt.set, got, tt.want)
			}
		})
	}
}

func TestGetNodeInfo_EscapesModuleName(t *testing.T) {
	var gotRequestURI string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotRequestURI = r.RequestURI
		_, _ = w.Write([]byte(`{"name":"@flowfuse/node-red-dashboard"}`))
	})

	if _, err := c.GetNodeInfo(context.Background(), "@flowfuse/node-red-dashboard"); err != nil {
		t.Fatalf("GetNodeInfo: %v", err)
	}
	if gotRequestURI != "/nodes/%40flowfuse%2Fnode-red-dashboard" {
		t.Errorf("expected escaped request URI, got %s", gotRequestURI)
	}
}

func TestInstallNode(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"name":"node-red-dashboard","version":"3.6.0"}`))
	})

	info, err := c.InstallNode(context.Background(), "node-red-dashboard", "3.6.0")
	if err != nil {
		t.Fatalf("InstallNode: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/nodes" {
		t.Errorf("expected POST /nodes, got %s %s", gotMethod, gotPath)
	}
	if gotBody["module"] != "node-red-dashboard" || gotBody["version"] != "3.6.0" {
		t.Errorf("unexpected body: %v", gotBody)
	}
	if info.Version != "3.6.0" {
		t.Errorf("expected version 3.6.0, got %q", info.Version)
	}
}

func TestInstallNode_OmitsEmptyVersion(t *testing.T) {
	var gotBody map[string]string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"name":"node-red-dashboard"}`))
	})

	if _, err := c.InstallNode(context.Background(), "node-red-dashboard", ""); err != nil {
		t.Fatalf("InstallNode: %v", err)
	}
	if _, present := gotBody["version"]; present {
		t.Errorf("expected no version key when empty, got %v", gotBody)
	}
}

func TestInstallNode_EmptyModule(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be called for empty module")
	})
	if _, err := c.InstallNode(context.Background(), "", ""); err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

func TestUninstallNode(t *testing.T) {
	var gotPath, gotMethod string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusNoContent)
	})

	if err := c.UninstallNode(context.Background(), "node-red-dashboard"); err != nil {
		t.Fatalf("UninstallNode: %v", err)
	}
	if gotMethod != "DELETE" || gotPath != "/nodes/node-red-dashboard" {
		t.Errorf("expected DELETE /nodes/node-red-dashboard, got %s %s", gotMethod, gotPath)
	}
}

func TestUninstallNode_InUseSurfacesError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"code":"type_in_use"}`, http.StatusBadRequest)
	})
	err := c.UninstallNode(context.Background(), "node-red-dashboard")
	if err == nil {
		t.Fatal("expected error for in-use module, got nil")
	}
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.StatusCode != 400 {
		t.Fatalf("expected 400 APIError, got %v", err)
	}
}

func TestSetNodeEnabled(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]bool
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"name":"node-red-dashboard"}`))
	})

	if _, err := c.SetNodeEnabled(context.Background(), "node-red-dashboard", "", false); err != nil {
		t.Fatalf("SetNodeEnabled: %v", err)
	}
	if gotMethod != "PUT" || gotPath != "/nodes/node-red-dashboard" {
		t.Errorf("expected PUT /nodes/node-red-dashboard, got %s %s", gotMethod, gotPath)
	}
	if gotBody["enabled"] != false {
		t.Errorf("expected enabled=false in body, got %v", gotBody)
	}
}

func TestSetNodeEnabled_WithSet(t *testing.T) {
	var gotPath string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"name":"node-red-dashboard"}`))
	})

	if _, err := c.SetNodeEnabled(context.Background(), "node-red-dashboard", "ui_button", true); err != nil {
		t.Fatalf("SetNodeEnabled: %v", err)
	}
	if gotPath != "/nodes/node-red-dashboard/ui_button" {
		t.Errorf("expected set in path, got %s", gotPath)
	}
}

func TestListNodes_IncludesUserField(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[
			{"id":"node-red-node-mqtt","name":"node-red-node-mqtt","version":"1.0.0","local":false,"user":true,"types":["mqtt in","mqtt out"],"loaded":true,"enabled":true,"module":"node-red-node-mqtt"}
		]`)
	})

	nodes, err := c.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if !nodes[0].User {
		t.Errorf("expected User=true, got %v", nodes[0].User)
	}
	if nodes[0].Local {
		t.Errorf("expected Local=false, got %v", nodes[0].Local)
	}
}

func TestGetNodeInfo_RoundTripAllFields(t *testing.T) {
	fixture := `{
		"name":"node-red-node-mqtt",
		"version":"1.0.0",
		"local":false,
		"user":true,
		"path":"/home/composedof2/.node-red/node_modules/node-red-node-mqtt",
		"plugins":[1,2,3],
		"nodes":[
			{"id":"mqtt","name":"mqtt","version":"1.0.0","local":false,"user":true,"types":["mqtt in","mqtt out"],"loaded":true,"enabled":true,"module":"node-red-node-mqtt"}
		]
	}`
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, fixture)
	})

	info, err := c.GetNodeInfo(context.Background(), "node-red-node-mqtt")
	if err != nil {
		t.Fatalf("GetNodeInfo: %v", err)
	}
	if info.Name != "node-red-node-mqtt" {
		t.Errorf("name: got %q", info.Name)
	}
	if info.Version != "1.0.0" {
		t.Errorf("version: got %q", info.Version)
	}
	if info.Local != false {
		t.Errorf("local: got %v", info.Local)
	}
	if info.User != true {
		t.Errorf("user: got %v", info.User)
	}
	if info.Path != "/home/composedof2/.node-red/node_modules/node-red-node-mqtt" {
		t.Errorf("path: got %q", info.Path)
	}
	if len(info.Plugins) == 0 || string(info.Plugins) != "[1,2,3]" {
		t.Errorf("plugins: got %s", string(info.Plugins))
	}
	if len(info.Nodes) != 1 {
		t.Fatalf("nodes: got %d entries", len(info.Nodes))
	}
	if info.Nodes[0].ID != "mqtt" {
		t.Errorf("nodes[0].id: got %q", info.Nodes[0].ID)
	}
}
