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
			if got := isLoopbackAddr(tc.addr); got != tc.want {
				t.Errorf("isLoopbackAddr(%q) = %v, want %v", tc.addr, got, tc.want)
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
