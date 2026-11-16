package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// Issue #341: the capability matrix must classify the /comms-backed
// tools from what the MCP observed, not from a constant. The
// MCP_DEBUG_STREAM opt-in is configuration — an enabled flag says the
// operator asked for the stream, not that the stream works.

// The four situations the issue names, for both stream tools.
func TestCapabilityMatrix_StreamToolsFollowObservation(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		debug   StreamObservation
		status  StreamObservation
		want    Capability
	}{
		{
			name:    "flag off is the operator's own gate",
			enabled: false,
			debug:   StreamObservation{State: StreamStateConnected},
			status:  StreamObservation{State: StreamStateConnected},
			want:    CapStreamDisabled,
		},
		{
			name:    "connected stream is available",
			enabled: true,
			debug:   StreamObservation{State: StreamStateConnected},
			status:  StreamObservation{State: StreamStateConnected},
			want:    CapOK,
		},
		{
			name:    "observed failure is disconnected",
			enabled: true,
			debug:   StreamObservation{State: StreamStateDisconnected, LastError: "connection refused"},
			status:  StreamObservation{State: StreamStateDisconnected, LastError: "connection refused"},
			want:    CapStreamDisconnected,
		},
		{
			name:    "no observation yet is unknown",
			enabled: true,
			debug:   StreamObservation{State: StreamStateUnknown},
			status:  StreamObservation{State: StreamStateUnknown},
			want:    CapUnavailableUnknown,
		},
		{
			name:    "no tail at all is unavailable",
			enabled: true,
			debug:   StreamObservation{State: StreamStateUnavailable},
			status:  StreamObservation{State: StreamStateUnavailable},
			want:    CapStreamUnavailable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := RuntimeProbe{
				NodeRedVersion:      v(5, 0, 1),
				RuntimeStateEnabled: true,
				DebugStreamEnabled:  tc.enabled,
				DebugStream:         tc.debug,
				StatusStream:        tc.status,
			}
			m := noderedCapabilityMatrix(p)
			for _, tool := range []string{"get_debug_messages", "get_node_status"} {
				if got := m[tool]; got != tc.want {
					t.Errorf("%s = %q, want %q", tool, got, tc.want)
				}
			}
		})
	}
}

// The issue's headline: an enabled flag alone must never produce ok.
// Only an observed connection does.
func TestCapabilityMatrix_EnabledFlagAloneIsNotOK(t *testing.T) {
	p := RuntimeProbe{
		NodeRedVersion:      v(5, 0, 1),
		RuntimeStateEnabled: true,
		DebugStreamEnabled:  true,
		DebugStream:         StreamObservation{State: StreamStateUnknown},
		StatusStream:        StreamObservation{State: StreamStateUnknown},
	}
	m := noderedCapabilityMatrix(p)
	for _, tool := range []string{"get_debug_messages", "get_node_status"} {
		if got := m[tool]; got == CapOK {
			t.Errorf("%s = ok on an enabled-but-unobserved stream: the flag is configuration, not evidence", tool)
		}
	}
}

// Each non-ok stream state owes the operator a reason and, where a
// concrete next step exists, a remedy that names it.
func TestCapabilityGuidance_StreamStatesAreCoherent(t *testing.T) {
	cases := []struct {
		name       string
		cap        Capability
		probe      RuntimeProbe
		wantReason []string
		wantRemedy []string
	}{
		{
			name:       "disabled names the MCP-side opt-in",
			cap:        CapStreamDisabled,
			probe:      RuntimeProbe{DebugStreamEnabled: false},
			wantReason: []string{"MCP_DEBUG_STREAM"},
			wantRemedy: []string{"MCP_DEBUG_STREAM"},
		},
		{
			name: "disconnected names the observed error",
			cap:  CapStreamDisconnected,
			probe: RuntimeProbe{
				DebugStreamEnabled: true,
				DebugStream:        StreamObservation{State: StreamStateDisconnected, LastError: "connection refused"},
				StatusStream:       StreamObservation{State: StreamStateDisconnected, LastError: "connection refused"},
			},
			wantReason: []string{"connection refused"},
			wantRemedy: []string{"MCP_LOG_LEVEL=debug"},
		},
		{
			name: "unavailable names the configured URL",
			cap:  CapStreamUnavailable,
			probe: RuntimeProbe{
				DebugStreamEnabled: true,
				DebugStream:        StreamObservation{State: StreamStateUnavailable},
				StatusStream:       StreamObservation{State: StreamStateUnavailable},
			},
			wantReason: []string{"/comms"},
			wantRemedy: []string{"URL"},
		},
		{
			name: "unknown does not claim a fact about availability",
			cap:  CapUnavailableUnknown,
			probe: RuntimeProbe{
				DebugStreamEnabled: true,
				DebugStream:        StreamObservation{State: StreamStateUnknown},
				StatusStream:       StreamObservation{State: StreamStateUnknown},
			},
			wantReason: []string{"/comms"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, tool := range []string{"get_debug_messages", "get_node_status"} {
				r, m := capabilityGuidance(tool, tc.cap, tc.probe)
				if r == "" {
					t.Fatalf("%s: reason must be non-empty", tool)
				}
				for _, want := range tc.wantReason {
					if !strings.Contains(r, want) {
						t.Errorf("%s: reason should mention %q, got %q", tool, want, r)
					}
				}
				if len(tc.wantRemedy) == 0 {
					continue
				}
				if m == "" {
					t.Fatalf("%s: remedy must be non-empty", tool)
				}
				for _, want := range tc.wantRemedy {
					if !strings.Contains(m, want) {
						t.Errorf("%s: remedy should mention %q, got %q", tool, want, m)
					}
				}
			}
		})
	}
}

// The unknown reason for a stream tool must not claim the tool is
// available or unavailable: the MCP has not seen a connection yet, and
// that is all it may say.
func TestCapabilityGuidance_StreamUnknownIsNotAVerdict(t *testing.T) {
	p := RuntimeProbe{
		DebugStreamEnabled: true,
		DebugStream:        StreamObservation{State: StreamStateUnknown},
		StatusStream:       StreamObservation{State: StreamStateUnknown},
	}
	for _, tool := range []string{"get_debug_messages", "get_node_status"} {
		r, _ := capabilityGuidance(tool, CapUnavailableUnknown, p)
		lower := strings.ToLower(r)
		for _, claim := range []string{"is available", "not available", "unavailable", "is ok"} {
			if strings.Contains(lower, claim) {
				t.Errorf("%s: unknown reason asserts %q: %q", tool, claim, r)
			}
		}
	}
}

// The probe must report what the MCP observed of the tails, not what
// the flag says. A server whose tails could not even be built (the
// /comms URL could not be derived) reports the stream as unavailable.
func TestRuntimeProbe_ReportsObservedStreamState(t *testing.T) {
	c, err := nodered.NewClient(nodered.Options{SkipVersionProbe: true, BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	s := &Server{nrClient: c, debugStream: true}

	p := s.runtimeProbe(context.Background())
	if p.DebugStream.State != StreamStateUnavailable {
		t.Errorf("debug stream state = %q, want unavailable (no tail was built)", p.DebugStream.State)
	}
	if p.StatusStream.State != StreamStateUnavailable {
		t.Errorf("status stream state = %q, want unavailable (no tail was built)", p.StatusStream.State)
	}
	if !p.DebugStreamEnabled {
		t.Error("the probe must keep reporting the opt-in as configuration")
	}
}

// End to end over a real /comms WebSocket: once the tail is
// connected, the matrix says ok. This is the case the issue says the
// old code could not express.
func TestRuntimeProbe_ConnectedStreamIsAvailable(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		select {
		case <-done:
		case <-r.Context().Done():
		}
	}))
	defer func() {
		close(done)
		srv.Close()
	}()

	c, err := nodered.NewClient(nodered.Options{SkipVersionProbe: true, BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	tail, err := nodered.NewStatusTail(c)
	if err != nil {
		t.Fatalf("NewStatusTail: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tail.Run(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for {
		if connected, _ := tail.State(); connected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the tail did not connect to the fake /comms endpoint")
		}
		time.Sleep(10 * time.Millisecond)
	}

	s := &Server{nrClient: c, debugStream: true, statusTail: tail}
	p := s.runtimeProbe(context.Background())
	if p.StatusStream.State != StreamStateConnected {
		t.Errorf("status stream state = %q, want connected", p.StatusStream.State)
	}
	m := noderedCapabilityMatrix(p)
	if got := m["get_node_status"]; got != CapOK {
		t.Errorf("get_node_status with a connected stream = %q, want ok", got)
	}
}
