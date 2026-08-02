package config

import (
	"os"
	"strings"
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

// TestLoad_NodeRedURL_AcceptsHTTPScheme covers issue #84: a normal
// http:// URL must keep loading. The SSRF guard is added on top of the
// existing empty-string check, so the happy path must still work.
func TestLoad_NodeRedURL_AcceptsHTTPScheme(t *testing.T) {
	t.Setenv("NODERED_URL", "http://localhost:1880")
	if _, err := Load(); err != nil {
		t.Fatalf("expected http URL to be accepted, got: %v", err)
	}
}

// TestLoad_NodeRedURL_AcceptsHTTPSScheme is the https counterpart of
// AcceptsHTTPScheme — TLS to the Node-RED host must be a first-class
// option, not a path that hits the new SSRF guard.
func TestLoad_NodeRedURL_AcceptsHTTPSScheme(t *testing.T) {
	t.Setenv("NODERED_URL", "https://nodered.example.com")
	if _, err := Load(); err != nil {
		t.Fatalf("expected https URL to be accepted, got: %v", err)
	}
}

// TestLoad_NodeRedURL_RejectsEmpty preserves the pre-existing
// defence-in-depth check: a Config with NodeRedURL == "" is rejected
// at validate() time. getEnv's default fallback means Load() never
// produces an empty value in practice, but the check stays so a
// future caller or refactor cannot silently start the server with
// an empty URL.
func TestLoad_NodeRedURL_RejectsEmpty(t *testing.T) {
	cfg := &Config{NodeRedURL: ""}
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for empty NodeRedURL, got nil")
	}
}

// TestLoad_NodeRedURL_RejectsNonHTTPSchemes is the SSRF guard test for
// issue #84. Node-RED's admin API only listens on http/https, so any
// other scheme — file://, ftp://, gopher://, javascript:, or a value
// without a scheme at all — must be refused at config-load time. A
// misconfigured or maliciously-injected NODERED_URL would otherwise
// turn the MCP into a proxy for reading local files or hitting
// non-HTTP services on the network.
func TestLoad_NodeRedURL_RejectsNonHTTPSchemes(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"file", "file:///etc/passwd"},
		{"ftp", "ftp://x"},
		{"gopher", "gopher://x"},
		{"javascript", "javascript:alert(1)"},
		{"javascript-leading-space", " javascript:alert(1)"},
		{"missing-scheme", "localhost:1880"},
		{"empty-scheme", "://no-scheme"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NODERED_URL", tc.value)
			_, err := Load()
			if err == nil {
				t.Fatalf("expected error for NODERED_URL=%q, got nil", tc.value)
			}
			// The error must be informative enough for an operator
			// to fix the value without reading the source. The scheme
			// guard says "scheme"/"http"; the url.Parse path says
			// "valid URL". Either is acceptable — what matters is
			// that the rejection is not silent.
			msg := err.Error()
			if !strings.Contains(msg, "scheme") &&
				!strings.Contains(msg, "http") &&
				!strings.Contains(msg, "valid URL") {
				t.Errorf("error for NODERED_URL=%q should mention scheme, http, or \"valid URL\", got: %v", tc.value, err)
			}
		})
	}
}

// TestLoad_HTTPMaxBody_Default covers issue #86: with no env var set,
// the HTTP body cap must default to 32 MiB — generous enough for the
// largest legitimate MCP request, tight enough to make a memory
// exhaustion attack expensive.
func TestLoad_HTTPMaxBody_Default(t *testing.T) {
	prev, hadPrev := os.LookupEnv("MCP_HTTP_MAX_BODY")
	os.Unsetenv("MCP_HTTP_MAX_BODY")
	t.Cleanup(func() {
		if hadPrev {
			os.Setenv("MCP_HTTP_MAX_BODY", prev)
		} else {
			os.Unsetenv("MCP_HTTP_MAX_BODY")
		}
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MCPHTTPMaxBody != 32<<20 {
		t.Errorf("expected default HTTPMaxBody=32 MiB, got %d", cfg.MCPHTTPMaxBody)
	}
}

// TestLoad_HTTPMaxBody_FromEnv covers the parse path: an operator who
// wants a different ceiling sets MCP_HTTP_MAX_BODY in bytes.
func TestLoad_HTTPMaxBody_FromEnv(t *testing.T) {
	t.Setenv("MCP_HTTP_MAX_BODY", "1048576")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MCPHTTPMaxBody != 1048576 {
		t.Errorf("expected HTTPMaxBody=1048576, got %d", cfg.MCPHTTPMaxBody)
	}
}

// TestLoad_HTTPMaxBody_RejectsBadValue locks in the validation rule:
// zero, negative, or unparseable values must abort startup rather than
// silently fall back to the default — the same "no silent fallback" rule
// the read-only test enforces.
func TestLoad_HTTPMaxBody_RejectsBadValue(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"zero", "0"},
		{"negative", "-1"},
		{"non-numeric", "fifty-megabytes"},
		{"empty-after-spaces", "   "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MCP_HTTP_MAX_BODY", tc.value)
			_, err := Load()
			if err == nil {
				t.Fatalf("expected error for MCP_HTTP_MAX_BODY=%q, got nil", tc.value)
			}
			if !strings.Contains(err.Error(), "MCP_HTTP_MAX_BODY") {
				t.Errorf("error should mention MCP_HTTP_MAX_BODY, got: %v", err)
			}
		})
	}
}
