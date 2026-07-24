package nodered

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

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
