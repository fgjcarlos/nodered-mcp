package config

import (
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
