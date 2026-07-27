package nodered

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestGetDiagnostics(t *testing.T) {
	// Trimmed from an actual Node-RED 5.0.0 response.
	const body = `{"report":"diagnostics","scope":"basic","nodejs":{"version":"v24.16.0","arch":"x64"},"os":{"containerised":true}}`

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/diagnostics" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(body))
	})

	got, err := c.GetDiagnostics(context.Background())
	if err != nil {
		t.Fatalf("GetDiagnostics: %v", err)
	}
	// Diagnostics are passed through opaquely: Node-RED owns this shape and it
	// grows between versions. Anything we parsed we would eventually drop.
	if !strings.Contains(string(got), `"v24.16.0"`) {
		t.Errorf("diagnostics were not passed through verbatim: %s", got)
	}
}

func TestListPlugins(t *testing.T) {
	const body = `[{"id":"node-red-plugin-example/example","type":"plugin","module":"node-red-plugin-example"}]`

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/plugins" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		// Without this header Node-RED serves the plugin editor HTML instead.
		if got := r.Header.Get("Accept"); !strings.Contains(got, "application/json") {
			t.Errorf("expected a JSON Accept header, got %q", got)
		}
		_, _ = w.Write([]byte(body))
	})

	got, err := c.ListPlugins(context.Background())
	if err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	if !strings.Contains(string(got), "node-red-plugin-example") {
		t.Errorf("unexpected plugins payload: %s", got)
	}
}

func TestGetContextPaths(t *testing.T) {
	tests := []struct {
		name     string
		scope    string
		id       string
		key      string
		wantPath string
	}{
		{"global store", "global", "", "", "/context/global"},
		{"global single key", "global", "", "temperature", "/context/global/temperature"},
		{"flow store", "flow", "tabA", "", "/context/flow/tabA"},
		{"flow single key", "flow", "tabA", "counter", "/context/flow/tabA/counter"},
		{"node store", "node", "fn1", "", "/context/node/fn1"},
		{"node single key", "node", "fn1", "state", "/context/node/fn1/state"},
		// A global scope must ignore a stray id rather than build
		// /context/global/tabA, which would silently read the wrong thing.
		{"global ignores an id", "global", "tabA", "", "/context/global"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				_, _ = w.Write([]byte(`{"memory":{}}`))
			})
			if _, err := c.GetContext(context.Background(), tc.scope, tc.id, tc.key); err != nil {
				t.Fatalf("GetContext: %v", err)
			}
			if gotPath != tc.wantPath {
				t.Errorf("requested %q, want %q", gotPath, tc.wantPath)
			}
		})
	}
}

// Node-RED answers 404 for a bad scope or a missing id, which surfaces to the
// model as an opaque HTTP error. Rejecting locally gives an actionable message
// and saves a pointless round trip.
func TestGetContextRejectsBadInput(t *testing.T) {
	tests := []struct {
		name, scope, id string
	}{
		{"unknown scope", "bogus", ""},
		{"empty scope", "", ""},
		{"flow without an id", "flow", ""},
		{"node without an id", "node", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				called = true
				_, _ = w.Write([]byte(`{}`))
			})
			if _, err := c.GetContext(context.Background(), tc.scope, tc.id, ""); err == nil {
				t.Fatal("expected an error, got nil")
			}
			if called {
				t.Error("invalid input still reached Node-RED")
			}
		})
	}
}

func TestGetContextPassesValuesThrough(t *testing.T) {
	// The real shape: values are store-keyed, and each carries msg + format.
	const body = `{"memory":{"temperature":{"msg":"21.5","format":"number"},"lastSeen":{"msg":"{\"room\":\"kitchen\"}","format":"Object"}}}`

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	got, err := c.GetContext(context.Background(), "global", "", "")
	if err != nil {
		t.Fatalf("GetContext: %v", err)
	}
	for _, want := range []string{"temperature", "21.5", "kitchen", "memory"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("context response lost %q: %s", want, got)
		}
	}
}

// A key containing a slash would otherwise extend the URL path and read a
// different key than the caller asked for.
func TestGetContextRejectsPathInjectionInKey(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("request should not have been sent, path %q", r.URL.Path)
	})
	if _, err := c.GetContext(context.Background(), "global", "", "../../flows"); err == nil {
		t.Fatal("expected a traversal in the key to be rejected")
	}
}
