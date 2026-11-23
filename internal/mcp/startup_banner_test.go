package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
	nr2 := nodered.ParseVersion("2.2.2")
	if got := degradedTools(nr2, tools); got != 4 {
		t.Errorf("NR 2.2.2 with the same tools: degraded = %d, want 4", got)
	}
	nr3 := nodered.ParseVersion("3.0.0")
	if got := degradedTools(nr3, tools); got != 3 {
		t.Errorf("NR 3.0 with get_flows_state supported: degraded = %d, want 3", got)
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
	s, _ := serverWithMockVersionProbe(t, func(w http.ResponseWriter, r *http.Request) {
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

// TestStartupBanner_NR3FlagsThree — NR 3.0 supports get_flows_state
// but is below the minimum for get_diagnostics, set_context, and inject_node.
func TestStartupBanner_NR3FlagsThree(t *testing.T) {
	s, _ := serverWithMockVersionProbe(t, func(w http.ResponseWriter, r *http.Request) {
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
	if degraded := degradedTools(v, s.tools); degraded != 3 {
		t.Errorf("NR 3.0 with full tool set: degraded = %d, want 3", degraded)
	}
}

// TestLoopbackURLIsNotAFixture pins the issue #340 behaviour: a
// loopback URL is not a test fixture. The shipped default
// (http://localhost:1880) is a real local Node-RED and must be
// probed; only the explicit nodered.Options.SkipVersionProbe switch
// marks a client as a fixture.
//
// The old heuristic keyed off the URL, so a real local install was
// never probed and the runtime version gate never engaged there.
func TestLoopbackURLIsNotAFixture(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/settings" {
			atomic.AddInt32(&hits, 1)
			_, _ = w.Write([]byte(`{"version":"5.0.8"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	c, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	v := c.NodeRedVersion(context.Background())
	if !v.Known || v.String() != "5.0.8" {
		t.Fatalf("loopback runtime must be probed, got %+v", v)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Errorf("expected the probe to hit /settings")
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
