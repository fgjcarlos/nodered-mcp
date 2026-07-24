package nodered

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestGetSettings(t *testing.T) {
	const body = `{"httpNodeRoot":"/","port":1880,"version":"5.0.1"}`
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/settings" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(body))
	})

	got, err := c.GetSettings(context.Background())
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if !json.Valid(got) {
		t.Fatalf("invalid JSON: %s", got)
	}
	if !strings.Contains(string(got), `"version":"5.0.1"`) {
		t.Errorf("settings missing version field: %s", got)
	}
}

func TestGetFlowsState(t *testing.T) {
	const body = `{"started":true,"flows":["tab1"],"rev":"abc"}`
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/flows/state" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(body))
	})

	got, err := c.GetFlowsState(context.Background())
	if err != nil {
		t.Fatalf("GetFlowsState: %v", err)
	}
	if !strings.Contains(string(got), `"started":true`) {
		t.Errorf("flow state missing started flag: %s", got)
	}
}

func TestSetFlowsState_RejectsInvalid(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be called for an invalid state")
	})
	if err := c.SetFlowsState(context.Background(), "pause"); err == nil {
		t.Fatal("expected validation error for invalid state")
	}
	if err := c.SetFlowsState(context.Background(), ""); err == nil {
		t.Fatal("expected validation error for empty state")
	}
}

func TestSetFlowsState_WritesBackupFirst(t *testing.T) {
	dir := t.TempDir()
	var deployedType string
	var deployedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			// pre-write snapshot
			_, _ = w.Write([]byte(`[{"id":"cur","type":"tab","label":"Current"}]`))
		case r.Method == "POST" && r.URL.Path == "/flows/state":
			deployedType = r.Method
			deployedBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(Options{BaseURL: srv.URL, Token: "t", BackupDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetFlowsState(context.Background(), "stop"); err != nil {
		t.Fatalf("SetFlowsState: %v", err)
	}
	if deployedType != "POST" {
		t.Errorf("expected POST, got %s", deployedType)
	}
	if !containsJSON(deployedBody, `"state":"stop"`) {
		t.Errorf("missing state:stop in body: %s", deployedBody)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("expected 1 pre-write backup, got %d", len(entries))
	}
}

func TestSetFlowsState_AbortsWhenBackupFails(t *testing.T) {
	var reached bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/flows" {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		reached = true
	}))
	t.Cleanup(srv.Close)

	c, _ := NewClient(Options{BaseURL: srv.URL, Token: "t", BackupDir: t.TempDir()})
	if err := c.SetFlowsState(context.Background(), "start"); err == nil {
		t.Fatal("expected SetFlowsState to fail when backup fails")
	}
	if reached {
		t.Error("state change reached Node-RED despite backup failure (not fail-closed)")
	}
}

func TestSetFlows_RequiresAtLeastOneFlow(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be called for empty flow list")
	})
	if err := c.SetFlows(context.Background(), nil); err == nil {
		t.Fatal("expected error for empty flows slice")
	}
}

func TestSetFlows_FullDeployHeader(t *testing.T) {
	dir := t.TempDir()
	var deployType string
	var postBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[{"id":"old","type":"tab","label":"Old"}]`))
		case r.Method == "POST" && r.URL.Path == "/flows":
			deployType = r.Header.Get("Node-RED-Deployment-Type")
			postBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c, _ := NewClient(Options{BaseURL: srv.URL, Token: "t", BackupDir: dir})
	flows := []json.RawMessage{json.RawMessage(`{"id":"new","type":"tab","label":"New"}`)}
	if err := c.SetFlows(context.Background(), flows); err != nil {
		t.Fatalf("SetFlows: %v", err)
	}
	if deployType != "full" {
		t.Errorf("expected full deployment header, got %q", deployType)
	}
	if !containsJSON(postBody, `"label":"New"`) {
		t.Errorf("posted flows missing the new tab: %s", postBody)
	}
}

func TestSetFlows_AbortsWhenBackupFails(t *testing.T) {
	var reached bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/flows" && r.Method == "GET" {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		reached = true
	}))
	t.Cleanup(srv.Close)

	c, _ := NewClient(Options{BaseURL: srv.URL, Token: "t", BackupDir: t.TempDir()})
	flows := []json.RawMessage{json.RawMessage(`{"id":"x","type":"tab","label":"X"}`)}
	if err := c.SetFlows(context.Background(), flows); err == nil {
		t.Fatal("expected SetFlows to fail when backup fails")
	}
	if reached {
		t.Error("full deploy reached Node-RED despite backup failure (not fail-closed)")
	}
}

func TestSearchNodes_RejectsEmptyQuery(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be called for empty query")
	})
	if _, err := c.SearchNodes(context.Background(), "", 10); err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestSearchNodes_ParsesRegistryResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/-/v1/search") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if !strings.Contains(r.URL.RawQuery, "keywords:node-red") {
			t.Errorf("query should be scoped to the node-red keyword: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{
			"objects":[
				{"package":{
					"name":"node-red-dashboard",
					"description":"Dashboard widgets",
					"version":"3.6.0",
					"date":"2026-01-15T12:00:00.000Z",
					"links":{"npm":"https://www.npmjs.com/package/node-red-dashboard"},
					"publisher":{"username":"dceejay"}
				}},
				{"package":{
					"name":"unrelated-pkg",
					"description":"not a node-red package",
					"version":"1.0.0"
				}}
			]
		}`))
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(Options{BaseURL: "http://x", SearchBaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.SearchNodes(context.Background(), "dashboard", 5)
	if err != nil {
		t.Fatalf("SearchNodes: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 hit (unrelated-pkg filtered out), got %d: %+v", len(got), got)
	}
	h := got[0]
	if h.Name != "node-red-dashboard" {
		t.Errorf("unexpected name: %q", h.Name)
	}
	if h.Version != "3.6.0" {
		t.Errorf("unexpected version: %q", h.Version)
	}
	if h.Link == "" {
		t.Error("expected non-empty link")
	}
	if h.Publisher != "dceejay" {
		t.Errorf("unexpected publisher: %q", h.Publisher)
	}
}

func TestSearchNodes_ClampsLimitToDefault(t *testing.T) {
	var gotSize string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSize = r.URL.Query().Get("size")
		_, _ = w.Write([]byte(`{"objects":[]}`))
	}))
	t.Cleanup(srv.Close)

	c, _ := NewClient(Options{BaseURL: "http://x", SearchBaseURL: srv.URL})
	// limit=0 should fall back to the default (10), not 0.
	if _, err := c.SearchNodes(context.Background(), "anything", 0); err != nil {
		t.Fatalf("SearchNodes: %v", err)
	}
	if gotSize != "10" {
		t.Errorf("expected default size 10, got %q", gotSize)
	}
}
