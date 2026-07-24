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
