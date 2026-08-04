package mcp

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// TestDegradedTools_CountsVersionGatedBelowFloor proves the
// counter only considers tools whose minimum is higher than the
// running NR version, and ignores tools with no minimum at all.
func TestDegradedTools_CountsVersionGatedBelowFloor(t *testing.T) {
	tools := []mcp.Tool{
		{Name: "get_diagnostics"},
		{Name: "inject_node"},
		{Name: "set_context"},
		{Name: "list_flows"}, // no minimum
		{Name: "get_flows_state"},
	}
	nr3 := nodered.ParseVersion("3.0.0")
	if got := degradedTools(nr3, tools); got != 3 {
		t.Errorf("NR 3.0 with 3 gated tools + 2 ungated: degraded = %d, want 3", got)
	}
	nr5 := nodered.ParseVersion("5.0.1")
	if got := degradedTools(nr5, tools); got != 0 {
		t.Errorf("NR 5.0.1 with same set: degraded = %d, want 0", got)
	}
}

// TestDegradedTools_UnknownVersionIsZero — the audit says the
// banner should not show a degraded count when the probe failed;
// the operator sees the "could not detect" warning instead.
func TestDegradedTools_UnknownVersionIsZero(t *testing.T) {
	tools := []mcp.Tool{{Name: "get_diagnostics"}}
	if got := degradedTools(nodered.Version{}, tools); got != 0 {
		t.Errorf("unknown NR version: degraded = %d, want 0", got)
	}
}

// TestStartupBanner_NR5NoDegraded — a fresh NR 5.0.1 means no
// degraded tools; the banner reports zero. Verifies the count
// component end-to-end via the same code path the banner uses.
func TestStartupBanner_NR5NoDegraded(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/settings" {
			_, _ = w.Write([]byte(`{"version":"5.0.1"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	v := s.nrClient.NodeRedVersion(context.Background())
	if v.Major != 5 || v.Minor != 0 || v.Patch != 1 {
		t.Fatalf("got %+v, want 5.0.1", v)
	}
	if degraded := degradedTools(v, s.tools); degraded != 0 {
		t.Errorf("NR 5.0.1 with full tool set: degraded = %d, want 0", degraded)
	}
}

// TestStartupBanner_NR3FlagsThree — a NR 3.0 mock should report
// 3 degraded tools (the three in nodered_min_version_for).
func TestStartupBanner_NR3FlagsThree(t *testing.T) {
	s, _ := serverWithMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/settings" {
			_, _ = w.Write([]byte(`{"version":"3.0.0"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	v := s.nrClient.NodeRedVersion(context.Background())
	if !v.Known {
		t.Fatalf("probe should succeed for the mock")
	}
	want := len(nodered_min_version_for)
	if degraded := degradedTools(v, s.tools); degraded != want {
		t.Errorf("NR 3.0 with full tool set: degraded = %d, want %d (every gated tool)", degraded, want)
	}
}

// TestIsLoopbackTestFixture pins the heuristic so a future
// httptest port change does not silently flip production
// behaviour. Production deployments use real hostnames; the
// helper must return false for them.
func TestIsLoopbackTestFixture(t *testing.T) {
	// httptest binds these prefixes by default.
	for _, url := range []string{
		"http://127.0.0.1:54321",
		"http://localhost:8080",
		"http://[::1]:3000",
	} {
		c, _ := nodered.NewClient(nodered.Options{BaseURL: url})
		if !isLoopbackTestFixture(c) {
			t.Errorf("isLoopbackTestFixture(%q) = false, want true", url)
		}
	}
	// Production-style URLs are not loopback.
	for _, url := range []string{
		"https://nodered.example.com",
		"http://10.0.0.5:1880",
	} {
		c, _ := nodered.NewClient(nodered.Options{BaseURL: url})
		if isLoopbackTestFixture(c) {
			t.Errorf("isLoopbackTestFixture(%q) = true, want false", url)
		}
	}
}

// TestMinVersionAnnotation_AppearsOnGatedTools — sanity: the
// min-version annotation the banner counts must actually be
// applied to the tools in the gate table, otherwise degraded is
// counting the wrong set.
func TestMinVersionAnnotation_AppearsOnGatedTools(t *testing.T) {
	s := newTestServer(t, false)
	for _, t1 := range s.tools {
		min, ok := MinVersionForKnown(t1.Name)
		if !ok {
			continue
		}
		if !strings.Contains(t1.Description, min.String()) {
			t.Errorf("%s should carry annotation containing %q, description: %q",
				t1.Name, min.String(), t1.Description)
		}
	}
}
