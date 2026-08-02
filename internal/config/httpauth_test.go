package config

import "testing"

func TestIsLoopbackAddr(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8090", true},
		{"localhost:8090", true},
		{"[::1]:8090", true},
		{"127.0.0.5:8090", true},
		// A bare port binds every interface, which is the case people get
		// wrong: it looks local and is reachable from the network.
		{":8090", false},
		{"0.0.0.0:8090", false},
		{"[::]:8090", false},
		{"192.168.1.10:8090", false},
		{"10.0.0.4:8090", false},
		{"nodered.example.com:8090", false},
		// Unparseable input is treated as exposed: guessing "local" here would
		// disable the guard exactly when it is least understood.
		{"garbage", false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			if got := IsLoopbackAddr(tc.addr); got != tc.want {
				t.Errorf("IsLoopbackAddr(%q) = %v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}

// Binding a network interface without a token would publish full write access
// to the Node-RED instance. Refusing to start is the whole point.
func TestLoadRejectsExposedHTTPWithoutAToken(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "http")
	t.Setenv("MCP_HTTP_ADDR", ":8090")
	t.Setenv("MCP_HTTP_TOKEN", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected an unauthenticated non-loopback bind to be refused")
	}
}

func TestLoadAllowsExposedHTTPWithAToken(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "http")
	t.Setenv("MCP_HTTP_ADDR", ":8090")
	t.Setenv("MCP_HTTP_TOKEN", "s3cret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MCPHTTPToken != "s3cret" {
		t.Errorf("token not loaded, got %q", cfg.MCPHTTPToken)
	}
}

// Local development must stay frictionless: nothing is reachable from off-host,
// so requiring a token there would be ceremony without a threat.
func TestLoadAllowsLoopbackHTTPWithoutAToken(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8090", "localhost:8090", "[::1]:8090"} {
		t.Run(addr, func(t *testing.T) {
			t.Setenv("MCP_TRANSPORT", "http")
			t.Setenv("MCP_HTTP_ADDR", addr)
			t.Setenv("MCP_HTTP_TOKEN", "")

			if _, err := Load(); err != nil {
				t.Errorf("loopback bind should not require a token: %v", err)
			}
		})
	}
}

// stdio never opens a port, so the rule must not fire there.
func TestLoadIgnoresTheTokenRuleOnStdio(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "stdio")
	t.Setenv("MCP_HTTP_ADDR", ":8090")
	t.Setenv("MCP_HTTP_TOKEN", "")

	if _, err := Load(); err != nil {
		t.Errorf("stdio must not be affected by the http token rule: %v", err)
	}
}

// OAuth is the alternative auth mode for the http transport: it must
// satisfy the same "no token on an exposed bind" rule, and it must
// require both issuer and audience to be configured together.
func TestLoadAllowsExposedHTTPWithOAuth(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "http")
	t.Setenv("MCP_HTTP_ADDR", ":8090")
	t.Setenv("MCP_HTTP_TOKEN", "")
	t.Setenv("MCP_OAUTH_ISSUER", "https://issuer.example/")
	t.Setenv("MCP_OAUTH_AUDIENCE", "nodered-mcp")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OAuthIssuer != "https://issuer.example/" {
		t.Errorf("oauth issuer not loaded, got %q", cfg.OAuthIssuer)
	}
	if cfg.OAuthAudience != "nodered-mcp" {
		t.Errorf("oauth audience not loaded, got %q", cfg.OAuthAudience)
	}
}

func TestLoadRejectsOAuthHalfConfigured(t *testing.T) {
	for _, tc := range []struct {
		name             string
		issuer, audience string
	}{
		{"issuer only", "https://issuer.example/", ""},
		{"audience only", "", "nodered-mcp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MCP_TRANSPORT", "http")
			t.Setenv("MCP_HTTP_ADDR", "127.0.0.1:8090")
			t.Setenv("MCP_HTTP_TOKEN", "")
			t.Setenv("MCP_OAUTH_ISSUER", tc.issuer)
			t.Setenv("MCP_OAUTH_AUDIENCE", tc.audience)

			if _, err := Load(); err == nil {
				t.Fatal("half-configured OAuth must be rejected")
			}
		})
	}
}

func TestLoadRejectsBearerAndOAuthTogether(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "http")
	t.Setenv("MCP_HTTP_ADDR", "127.0.0.1:8090")
	t.Setenv("MCP_HTTP_TOKEN", "shared-secret")
	t.Setenv("MCP_OAUTH_ISSUER", "https://issuer.example/")
	t.Setenv("MCP_OAUTH_AUDIENCE", "nodered-mcp")

	if _, err := Load(); err == nil {
		t.Fatal("bearer token and OAuth together must be rejected")
	}
}

// OAuth on a loopback bind must be accepted without a token, since the
// "must not come up unauthenticated" rule is about exposure, not
// presence of an auth mechanism.
func TestLoadAllowsLoopbackHTTPWithOAuthAndNoToken(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "http")
	t.Setenv("MCP_HTTP_ADDR", "127.0.0.1:8090")
	t.Setenv("MCP_HTTP_TOKEN", "")
	t.Setenv("MCP_OAUTH_ISSUER", "https://issuer.example/")
	t.Setenv("MCP_OAUTH_AUDIENCE", "nodered-mcp")

	if _, err := Load(); err != nil {
		t.Errorf("loopback bind with OAuth should not require a token: %v", err)
	}
}

// MCP_ALLOW_INSECURE_LOOPBACK (issue #89) is the operator's
// acknowledgement that the loopback-without-token configuration is
// intentional. It does not affect auth — it only controls the startup
// warning — so the test asserts the flag is loaded and that a bare
// loopback bind remains accepted (the warning is the server runtime's
// responsibility, exercised separately in the mcp package).
func TestLoadReadsAllowInsecureLoopback(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "http")
	t.Setenv("MCP_HTTP_ADDR", "127.0.0.1:8090")
	t.Setenv("MCP_HTTP_TOKEN", "")
	t.Setenv("MCP_ALLOW_INSECURE_LOOPBACK", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.MCPAllowInsecureLoopback {
		t.Errorf("expected MCPAllowInsecureLoopback=true, got false")
	}
}

// The default value of MCP_ALLOW_INSECURE_LOOPBACK is false: every
// deployment that lands on the loopback-without-token configuration
// should see the warning by default, so an operator has to opt in to
// silence it.
func TestLoadAllowInsecureLoopbackDefaultsFalse(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "http")
	t.Setenv("MCP_HTTP_ADDR", "127.0.0.1:8090")
	t.Setenv("MCP_HTTP_TOKEN", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MCPAllowInsecureLoopback {
		t.Errorf("expected MCPAllowInsecureLoopback=false by default, got true")
	}
}

func TestLoadRejectsMalformedAllowInsecureLoopback(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "http")
	t.Setenv("MCP_HTTP_ADDR", "127.0.0.1:8090")
	t.Setenv("MCP_ALLOW_INSECURE_LOOPBACK", "yes-please")

	if _, err := Load(); err == nil {
		t.Fatal("expected a parse error for non-boolean MCP_ALLOW_INSECURE_LOOPBACK")
	}
}
