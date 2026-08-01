package config

import (
	"os"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("NODERED_URL", "")
	t.Setenv("MCP_LOG_LEVEL", "")
	t.Setenv("MCP_TRANSPORT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.NodeRedURL != "http://localhost:1880" {
		t.Errorf("expected default URL, got %q", cfg.NodeRedURL)
	}
	if cfg.MCPLogLevel != "info" {
		t.Errorf("expected default log level info, got %q", cfg.MCPLogLevel)
	}
	if cfg.MCPTransport != "stdio" {
		t.Errorf("expected default transport stdio, got %q", cfg.MCPTransport)
	}
}

func TestLoad_RejectsInvalidTransport(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "carrier-pigeon")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid transport")
	}
}

func TestLoad_HTTPTransport(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "http")
	// The default address binds every interface, which now requires a token.
	// See httpauth_test.go for that rule on its own.
	t.Setenv("MCP_HTTP_TOKEN", "test-token")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MCPTransport != "http" {
		t.Errorf("expected transport http, got %q", cfg.MCPTransport)
	}
	if cfg.MCPHTTPAddr != ":8090" {
		t.Errorf("expected default http addr :8090, got %q", cfg.MCPHTTPAddr)
	}
}

func TestLoad_RejectsInvalidLogLevel(t *testing.T) {
	t.Setenv("MCP_LOG_LEVEL", "shout")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid log level")
	}
}

func TestLoad_ReadOnlyDefaultsOff(t *testing.T) {
	t.Setenv("MCP_READ_ONLY", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MCPReadOnly {
		t.Error("expected MCPReadOnly=false by default")
	}
}

func TestLoad_ParsesReadOnly(t *testing.T) {
	t.Setenv("MCP_READ_ONLY", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.MCPReadOnly {
		t.Error("expected MCPReadOnly=true")
	}
}

// A misspelled value must abort startup, not quietly fall back to false: that
// fallback would hand write tools to someone who asked for read-only.
func TestLoad_RejectsUnparseableReadOnly(t *testing.T) {
	t.Setenv("MCP_READ_ONLY", "yes")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for unparseable MCP_READ_ONLY, got nil")
	}
}

func TestLoad_ParsesInsecure(t *testing.T) {
	t.Setenv("NODERED_INSECURE", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.NodeRedInsecure {
		t.Error("expected NodeRedInsecure=true")
	}
}

// TestLoad_NodeDenylist_Default covers issue #81: with no env var set,
// the server must default to blocking exec/system so a fresh deployment
// is not vulnerable to RCE out of the box. (Setting the var to "" is the
// explicit opt-out, not the default — see TestLoad_NodeDenylist_EmptyOptOut.)
func TestLoad_NodeDenylist_Default(t *testing.T) {
	// t.Unsetenv was added in Go 1.17 but we still target Go versions
	// where it is absent; do the unset+restore dance by hand.
	prev, hadPrev := os.LookupEnv("MCP_NODE_DENYLIST")
	os.Unsetenv("MCP_NODE_DENYLIST")
	t.Cleanup(func() {
		if hadPrev {
			os.Setenv("MCP_NODE_DENYLIST", prev)
		} else {
			os.Unsetenv("MCP_NODE_DENYLIST")
		}
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.HasDeniedNodeType("exec") {
		t.Error("default denylist must block \"exec\" (issue #81)")
	}
	if !cfg.HasDeniedNodeType("system") {
		t.Error("default denylist must block \"system\" (issue #81)")
	}
	if cfg.HasDeniedNodeType("inject") {
		t.Error("default denylist must NOT block \"inject\"")
	}
}

// TestLoad_NodeDenylist_FromEnv covers the parse path: a comma-separated
// list comes back as a slice, with whitespace tolerated.
func TestLoad_NodeDenylist_FromEnv(t *testing.T) {
	t.Setenv("MCP_NODE_DENYLIST", "foo, bar ,baz")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.HasDeniedNodeType("foo") || !cfg.HasDeniedNodeType("bar") || !cfg.HasDeniedNodeType("baz") {
		t.Errorf("expected foo/bar/baz in denylist, got %v", cfg.NodeDenylist)
	}
	if cfg.HasDeniedNodeType("exec") {
		t.Errorf("non-default value must replace the defaults, got %v", cfg.NodeDenylist)
	}
}

// TestLoad_NodeDenylist_EmptyOptOut is the explicit "I know what I'm
// doing" path: an operator who needs exec/system sets the variable to
// an empty (or whitespace-only) string and the denylist is empty —
// no defaults applied.
func TestLoad_NodeDenylist_EmptyOptOut(t *testing.T) {
	for _, raw := range []string{"", " ", "  ,  "} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("MCP_NODE_DENYLIST", raw)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(cfg.NodeDenylist) != 0 {
				t.Errorf("MCP_NODE_DENYLIST=%q must opt out (nil/empty), got %v", raw, cfg.NodeDenylist)
			}
			if cfg.HasDeniedNodeType("exec") {
				t.Errorf("explicit empty value must NOT carry the default denylist (raw=%q)", raw)
			}
		})
	}
}
