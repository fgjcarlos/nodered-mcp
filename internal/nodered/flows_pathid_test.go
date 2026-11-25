package nodered

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
)

// A flow or inject id is substituted into the admin API path, and url.JoinPath
// cleans "..". Unchecked, delete_flow {"id":"../nodes/<module>"} reached
// DELETE /nodes/<module> and uninstalled a palette package while reporting a
// deleted flow. Every id-in-path call must refuse such ids before any request
// that could reach a different endpoint.
func TestIDPathTraversalIsRefused(t *testing.T) {
	bad := []string{"../nodes/node-red-node-random", "..", ".", "a/b", `a\b`, "..%2Fnodes"}
	calls := map[string]func(c *Client, id string) error{
		"GetFlow":    func(c *Client, id string) error { _, err := c.GetFlow(context.Background(), id); return err },
		"DeleteFlow": func(c *Client, id string) error { return c.DeleteFlow(context.Background(), id) },
		"InjectNode": func(c *Client, id string) error { return c.InjectNode(context.Background(), id) },
		"InjectNodeWithBody": func(c *Client, id string) error {
			return c.InjectNodeWithBody(context.Background(), id, json.RawMessage(`{}`))
		},
	}
	for name, call := range calls {
		for _, id := range bad {
			var escaped atomic.Int32
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/flows" {
					escaped.Add(1)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[]`))
			})
			if err := call(c, id); err == nil {
				t.Errorf("%s(%q): expected an error, got nil", name, id)
			}
			if n := escaped.Load(); n != 0 {
				t.Errorf("%s(%q): %d request(s) reached a non-/flows endpoint", name, id, n)
			}
		}
	}
}
