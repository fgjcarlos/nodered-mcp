package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
	"github.com/mark3labs/mcp-go/server"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func registeredToolCall(t *testing.T, s *Server, name string, args map[string]any) json.RawMessage {
	t.Helper()
	request, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := json.Marshal(s.mcpServer.HandleMessage(context.Background(), request))
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestInjectNodeAmbiguousTransportDoesNotRetry(t *testing.T) {
	var posts atomic.Int32
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/inject/i":
			posts.Add(1)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		case r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[{"id":"i","type":"inject","z":"f"},{"id":"f","type":"tab"}]`))
		default:
			_, _ = w.Write([]byte(`{"version":"5.0.0"}`))
		}
	})
	response := registeredToolCall(t, s, "inject_node", map[string]any{"id": "i"})
	if got := posts.Load(); got != 1 {
		t.Errorf("POST /inject/i count = %d, want 1; response: %s", got, response)
	}
	if backups, err := s.nrClient.ListBackups(); err != nil || len(backups) != 0 {
		t.Fatalf("inject action must not add configuration backups: %v, %v", backups, err)
	}
	var envelope struct {
		Result mcp.CallToolResult `json:"result"`
		Error  any                `json:"error"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error != nil || !envelope.Result.IsError {
		t.Fatalf("want MCP tool error result, got %s", response)
	}
	text := strings.ToLower(string(response))
	for _, want := range []string{"indeterminate", "runtime", "before repeating"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q guidance: %s", want, response)
		}
	}
}

func TestReadTransportRetriesRemain(t *testing.T) {
	for _, tool := range []string{"list_flows", "search_nodes"} {
		for _, truncated := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/truncated=%v", tool, truncated), func(t *testing.T) {
				var gets atomic.Int32
				fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" {
						t.Errorf("unexpected method %s", r.Method)
					}
					if gets.Add(1) < 3 {
						if truncated {
							w.Header().Set("Content-Length", "100")
							_, _ = w.Write([]byte(`{`))
							return
						}
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
						return
					}
					if tool == "search_nodes" {
						_, _ = w.Write([]byte(`{"objects":[]}`))
					} else {
						_, _ = w.Write([]byte(`[]`))
					}
				}))
				defer fixture.Close()
				dir := t.TempDir()
				c, err := nodered.NewClient(nodered.Options{BaseURL: fixture.URL, SearchBaseURL: fixture.URL, BackupDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				response := registeredToolCall(t, New(c, Options{}), tool, map[string]any{"query": "test"})
				var envelope struct {
					Result mcp.CallToolResult
					Error  any
				}
				if err := json.Unmarshal(response, &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Error != nil || envelope.Result.IsError || gets.Load() != 3 {
					t.Fatalf("GET count=%d, response=%s", gets.Load(), response)
				}
				if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
					t.Fatalf("read wrote backups: %v, %v", entries, err)
				}
			})
		}
	}
}

func TestTransportMarkersDoNotControlRetry(t *testing.T) {
	for _, tool := range []string{"list_flows", "inject_node"} {
		t.Run(tool, func(t *testing.T) {
			var gets, posts atomic.Int32
			s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					posts.Add(1)
				} else {
					gets.Add(1)
				}
				if tool == "inject_node" && r.URL.Path == "/flows" {
					_, _ = w.Write([]byte(`[{"id":"i","type":"inject","z":"f"},{"id":"f","type":"tab"}]`))
					return
				}
				http.Error(w, "EOF connection reset context deadline exceeded", 400)
			})
			response := registeredToolCall(t, s, tool, map[string]any{"id": "i"})
			var envelope struct {
				Result mcp.CallToolResult
				Error  any
			}
			if err := json.Unmarshal(response, &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error != nil || !envelope.Result.IsError || strings.Contains(string(response), "indeterminate") {
				t.Fatalf("unexpected API error result: %s", response)
			}
			if gets.Load() != map[string]int32{"list_flows": 1, "inject_node": 2}[tool] || posts.Load() != map[string]int32{"list_flows": 0, "inject_node": 1}[tool] {
				t.Fatalf("requests: GET=%d POST=%d", gets.Load(), posts.Load())
			}
		})
	}
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) { t.Errorf("invalid input reached runtime") })
	response := registeredToolCall(t, s, "set_flows_state", map[string]any{"state": "EOF"})
	if !strings.Contains(string(response), `state must be`) || strings.Contains(string(response), "indeterminate") {
		t.Fatalf("validation result: %s", response)
	}
}

func TestWriteTransportDeadlineIsSingleAttempt(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, context.Canceled} {
		t.Run(cause.Error(), func(t *testing.T) {
			var calls int
			s := &Server{mcpServer: server.NewMCPServer("test", "test")}
			s.addWriteTool(mcp.NewTool("action"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				calls++
				if _, ok := ctx.Deadline(); !ok {
					t.Error("missing per-attempt deadline")
				}
				return nil, fmt.Errorf("dispatch: %w", cause)
			})
			response := registeredToolCall(t, s, "action", nil)
			if calls != 1 || !strings.Contains(string(response), "indeterminate") || !strings.Contains(string(response), "before repeating") {
				t.Fatalf("calls=%d response=%s", calls, response)
			}
		})
	}
}

func TestRetryDoesNotClassifyUntypedErrors(t *testing.T) {
	calls := 0
	cause := errors.New("validation EOF")
	res, err := (&Server{}).withTimeoutRetry("read", func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) { calls++; return nil, cause })(context.Background(), mcp.CallToolRequest{})
	if calls != 1 || res != nil || err != cause {
		t.Fatalf("calls=%d result=%v err=%v", calls, res, err)
	}
}

func TestIssue328PaletteWriteResponses(t *testing.T) {
	for _, mode := range []string{"closed", "truncated", "api-marker"} {
		t.Run(mode, func(t *testing.T) {
			var posts atomic.Int32
			s, fixture := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/nodes" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected request", 500)
					return
				}
				posts.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != `{"module":"node-red-node-test"}` {
					t.Errorf("body=%q err=%v", body, err)
				}
				switch mode {
				case "closed":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
				case "truncated":
					w.Header().Set("Content-Length", "100")
					_, _ = io.WriteString(w, "{")
				case "api-marker":
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, "EOF")
				}
			})
			raw := registeredToolCall(t, s, "install_node",
				map[string]any{"module": "node-red-node-test"})
			var envelope struct {
				Result mcp.CallToolResult `json:"result"`
				Error  json.RawMessage    `json:"error"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatal(err)
			}
			want := "calling Node-RED: nodered POST /nodes: HTTP 400: EOF"
			if mode != "api-marker" {
				cause := fmt.Sprintf(
					"calling Node-RED: cannot reach Node-RED at %s/nodes: Post %q: EOF",
					fixture.URL, fixture.URL+"/nodes")
				if mode == "truncated" {
					cause = "calling Node-RED: reading response body: unexpected EOF"
				}
				want = `tool "install_node" outcome is indeterminate: ` + cause +
					". The action may already have executed; inspect the Node-RED runtime before repeating the operation."
			}
			if posts.Load() != 1 || len(envelope.Error) != 0 ||
				!envelope.Result.IsError || resultFirstText(&envelope.Result) != want {
				t.Fatalf("posts=%d response=%s want=%q", posts.Load(), raw, want)
			}
		})
	}
}
