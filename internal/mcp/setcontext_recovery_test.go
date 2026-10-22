package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// helperRuntime models the separate process-owned runtime, including ownership
// checks on replacement, rather than copying the MCP server's helper cache.
type helperRuntime struct {
	mu               sync.Mutex
	items            []json.RawMessage
	creates, injects int
	lastInject       string
	fail             bool
	ambiguous        bool
}

func (rt *helperRuntime) serve(w http.ResponseWriter, r *http.Request) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	switch {
	case r.Method == "GET" && r.URL.Path == "/flows":
		items := rt.items
		if items == nil {
			items = []json.RawMessage{}
		}
		if r.Header.Get("Node-RED-API-Version") == "v2" {
			_ = json.NewEncoder(w).Encode(map[string]any{"rev": "r1", "flows": items})
		} else {
			_ = json.NewEncoder(w).Encode(items)
		}
	case r.Method == "POST" && r.URL.Path == "/flow":
		rt.creates++
		if rt.fail {
			http.Error(w, "deployment failed", 500)
			return
		}
		var tab map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&tab)
		id := fmt.Sprintf("runtime-%d", rt.creates)
		tab["id"], _ = json.Marshal(id)
		var nodes []map[string]json.RawMessage
		_ = json.Unmarshal(tab["nodes"], &nodes)
		delete(tab, "nodes")
		tab["type"] = json.RawMessage(`"tab"`)
		raw, _ := json.Marshal(tab)
		rt.items = append(rt.items, raw)
		for _, node := range nodes {
			node["z"], _ = json.Marshal(id)
			raw, _ = json.Marshal(node)
			rt.items = append(rt.items, raw)
		}
		if rt.ambiguous {
			http.Error(w, "response lost after deployment", 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/flow/"):
		id := strings.TrimPrefix(r.URL.Path, "/flow/")
		var nodes []json.RawMessage
		for _, raw := range rt.items {
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			if m["z"] == id {
				nodes = append(nodes, raw)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "label": setContextHelperFlowLabel, "nodes": nodes})
	case r.Method == "POST" && r.URL.Path == "/flows":
		var body struct {
			Flows []json.RawMessage `json:"flows"`
		}
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 && raw[0] == '[' {
			_ = json.Unmarshal(raw, &body.Flows)
		} else {
			_ = json.Unmarshal(raw, &body)
		}
		owners := map[string]string{}
		for _, raw := range rt.items {
			var m map[string]string
			_ = json.Unmarshal(raw, &m)
			owners[m["id"]] = m["z"]
		}
		for _, raw := range body.Flows {
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			if owner, ok := owners[m["id"].(string)]; ok && owner != "" && owner != m["z"] {
				http.Error(w, "flow entry belongs outside the updated flow", 400)
				return
			}
		}
		rt.items = body.Flows
		w.WriteHeader(204)
	case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/flow/"):
		id := strings.TrimPrefix(r.URL.Path, "/flow/")
		kept := rt.items[:0]
		for _, raw := range rt.items {
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			if m["id"] != id && m["z"] != id {
				kept = append(kept, raw)
			}
		}
		rt.items = kept
		w.WriteHeader(204)
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/inject/"):
		id := strings.TrimPrefix(r.URL.Path, "/inject/")
		for _, raw := range rt.items {
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			if m["id"] == id && m["type"] == "inject" {
				rt.injects++
				rt.lastInject = id
				w.WriteHeader(200)
				return
			}
		}
		http.NotFound(w, r)
	default:
		http.Error(w, "unexpected request "+r.Method+" "+r.URL.Path, 500)
	}
}

func helperCall(t *testing.T, s *Server) *mcp.CallToolResult {
	t.Helper()
	result, err := callHelperTool(context.Background(), s, "set_context", map[string]any{"scope": "global", "key": "audit", "value": "42"})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSetContextRecoveryRestart(t *testing.T) {
	rt := &helperRuntime{}
	srv := httptest.NewServer(http.HandlerFunc(rt.serve))
	defer srv.Close()
	for i := 0; i < 2; i++ {
		s := newTestServerAt(t, srv.URL, t.TempDir())
		if result := helperCall(t, s); result.IsError {
			t.Fatalf("call %d: %+v", i, result)
		}
	}
	if rt.creates != 1 || rt.injects != 2 || len(rt.items) != 3 {
		t.Fatalf("creates=%d injects=%d entries=%d; want 1,2,3", rt.creates, rt.injects, len(rt.items))
	}
}

func TestSetContextRecoveryFailedDeployment(t *testing.T) {
	rt := &helperRuntime{fail: true}
	srv := httptest.NewServer(http.HandlerFunc(rt.serve))
	defer srv.Close()
	s := newTestServerAt(t, srv.URL, t.TempDir())
	if result := helperCall(t, s); !result.IsError {
		t.Fatal("expected deployment failure")
	}
	if len(rt.items) != 0 || rt.injects != 0 {
		t.Fatal("failed deployment left state or dispatched")
	}
	rt.fail = false
	if result := helperCall(t, s); result.IsError {
		t.Fatalf("retry: %+v", result)
	}
	if rt.creates != 2 || rt.injects != 1 || len(rt.items) != 3 {
		t.Fatalf("retry left residues: %+v", rt)
	}
}

func TestSetContextRecoveryLostResponse(t *testing.T) {
	rt := &helperRuntime{ambiguous: true}
	srv := httptest.NewServer(http.HandlerFunc(rt.serve))
	defer srv.Close()
	s := newTestServerAt(t, srv.URL, t.TempDir())
	if result := helperCall(t, s); !result.IsError {
		t.Fatal("expected ambiguous deployment error")
	}
	if rt.creates != 1 || rt.injects != 0 {
		t.Fatal("ambiguous deployment replayed or injected")
	}
	rt.ambiguous = false
	if result := helperCall(t, s); result.IsError {
		t.Fatalf("recovery: %+v", result)
	}
	if rt.creates != 1 || rt.injects != 1 || len(rt.items) != 3 {
		t.Fatalf("recovery left residues: %+v", rt)
	}
}

func TestSetContextRecoveryCollisionAndReplacement(t *testing.T) {
	rt := &helperRuntime{items: []json.RawMessage{json.RawMessage(`{"id":"unrelated","type":"tab","label":"User"}`), json.RawMessage(`{"id":"mcp_ctx_helper_fn","type":"function","z":"unrelated","func":"return msg;"}`)}}
	original := string(rt.items[1])
	srv := httptest.NewServer(http.HandlerFunc(rt.serve))
	defer srv.Close()
	s := newTestServerAt(t, srv.URL, t.TempDir())
	if result := helperCall(t, s); result.IsError {
		t.Fatalf("collision: %+v", result)
	}
	if string(rt.items[1]) != original {
		t.Fatal("unrelated node changed")
	}
	// Simulate replacement by another client: a modified helper must not receive
	// the privileged payload, even though its IDs and names are unchanged.
	for i, raw := range rt.items {
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		if m["type"] == "function" && m["z"] != "unrelated" {
			m["func"] = "throw new Error('edited');"
			rt.items[i], _ = json.Marshal(m)
		}
	}
	if result := helperCall(t, s); result.IsError {
		t.Fatalf("edited helper recovery: %+v", result)
	}
	if rt.creates != 2 || rt.injects != 2 {
		t.Fatalf("edited helper not replaced safely: %+v", rt)
	}
	// Simulate set_flows deleting all helpers. A cached pointer must not be used.
	rt.items = rt.items[:2]
	if result := helperCall(t, s); result.IsError {
		t.Fatalf("deleted helper recovery: %+v", result)
	}
	if rt.creates != 3 || rt.injects != 3 || len(rt.items) != 5 {
		t.Fatalf("deleted helper not recovered: %+v", rt)
	}
}

// mockCreatedContextTab mirrors Node-RED assigning a tab ID and rewriting z.
func mockCreatedContextTab(t *testing.T, r *http.Request, id string) []byte {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("read created tab: %v", err)
	}
	var tab map[string]any
	if err := json.Unmarshal(body, &tab); err != nil {
		t.Errorf("decode created tab: %v", err)
	}
	tab["id"] = id
	if nodes, ok := tab["nodes"].([]any); ok {
		for _, node := range nodes {
			node.(map[string]any)["z"] = id
		}
	}
	body, _ = json.Marshal(tab)
	return body
}

func mockContextSnapshot(r *http.Request, id string) []byte {
	tab := strings.ReplaceAll(string(contextHelperDocument(setContextHelperFunctionID, setContextHelperInjectID)), setContextHelperFlowID, id)
	return mockFlowSnapshot(r, []byte(tab))
}

func callHelperTool(ctx context.Context, s *Server, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	request, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	raw, err := json.Marshal(s.mcpServer.HandleMessage(ctx, request))
	if err != nil {
		return nil, err
	}
	var response struct {
		Error  any                `json:"error"`
		Result mcp.CallToolResult `json:"result"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	if response.Error != nil {
		return nil, fmt.Errorf("%s: %s", tool, raw)
	}
	return &response.Result, nil
}

func TestSetContextRecoveryAfterConfigurationWrite(t *testing.T) {
	for _, name := range []string{"delete_flow", "set_flows"} {
		t.Run(name, func(t *testing.T) {
			rt := &helperRuntime{}
			srv := httptest.NewServer(http.HandlerFunc(rt.serve))
			defer srv.Close()
			s := newTestServerAt(t, srv.URL, t.TempDir())
			if result := helperCall(t, s); result.IsError {
				t.Fatalf("initial call: %+v", result)
			}
			args := map[string]any{"id": "runtime-1"}
			wantEntries := 3
			if name == "set_flows" {
				args = map[string]any{"flows": `[{"id":"replacement","type":"tab","label":"Replacement"}]`}
				wantEntries = 4
			}
			result, err := callHelperTool(context.Background(), s, name, args)
			if err != nil || result.IsError {
				t.Fatalf("write: %+v %v", result, err)
			}
			if result := helperCall(t, s); result.IsError {
				t.Fatalf("recovery: %+v", result)
			}
			if rt.creates != 2 || rt.injects != 2 || len(rt.items) != wantEntries {
				t.Fatalf("creates=%d injects=%d entries=%d", rt.creates, rt.injects, len(rt.items))
			}
		})
	}
}

func TestSetContextRecoveryRejectsModifiedExecutable(t *testing.T) {
	cases := []struct {
		name, kind, key string
		value           any
	}{
		{"code", "function", "func", "return msg;"},
		{"startup", "function", "initialize", "global.set('audit','unexpected');"},
		{"shutdown", "function", "finalize", "global.set('audit','unexpected');"},
		{"library", "function", "libs", []any{map[string]any{"var": "fs", "module": "fs"}}},
		{"outgoing", "function", "wires", []any{[]any{"unrelated"}}},
		{"inject-wire", "inject", "wires", []any{[]any{"unrelated"}}},
		{"schedule", "inject", "repeat", "1"},
		{"disabled", "inject", "d", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := &helperRuntime{}
			srv := httptest.NewServer(http.HandlerFunc(rt.serve))
			defer srv.Close()
			s := newTestServerAt(t, srv.URL, t.TempDir())
			if result := helperCall(t, s); result.IsError {
				t.Fatalf("initial: %+v", result)
			}
			rt.mu.Lock()
			oldInject := rt.lastInject
			var edited json.RawMessage
			var editedID string
			for i, raw := range rt.items {
				var node map[string]any
				_ = json.Unmarshal(raw, &node)
				if node["type"] == tc.kind {
					node[tc.key] = tc.value
					rt.items[i], _ = json.Marshal(node)
					edited = append([]byte(nil), rt.items[i]...)
					editedID = node["id"].(string)
				}
			}
			rt.mu.Unlock()
			// A fresh server must enforce the same checks as the cached path.
			if result := helperCall(t, newTestServerAt(t, srv.URL, t.TempDir())); result.IsError {
				t.Fatalf("recovery: %+v", result)
			}
			rt.mu.Lock()
			defer rt.mu.Unlock()
			if rt.creates != 2 || rt.injects != 2 || rt.lastInject == oldInject || len(rt.items) != 6 {
				t.Fatalf("unverified helper used or state lost: creates=%d injects=%d entries=%d", rt.creates, rt.injects, len(rt.items))
			}
			for _, raw := range rt.items {
				var node map[string]any
				_ = json.Unmarshal(raw, &node)
				if node["id"] == editedID && string(raw) != string(edited) {
					t.Fatal("edited node overwritten")
				}
			}
		})
	}
}
