package mcp

// Issue #316 — runtime enforcement of the version gate.
//
// These tests prove four properties the issue names:
//
//   1. set_context is refused on a known-too-low version BEFORE
//      any helper is provisioned and BEFORE the inject is
//      dispatched (no POST /flow, no POST /flow/:id, no PUT
//      /flow/:id, no POST /inject/:id reaches the fixture).
//
//   2. inject_node with a payload is refused on a known-too-low
//      version BEFORE the body is built and dispatched (no POST
//      /inject/:id reaches the fixture, and the lookup probe
//      GET /flows is also avoided — the guard sits above the
//      disabled/lookup checks on purpose so a refused call does
//      not even ask the runtime whether the node exists).
//
//   3. inject_node without a payload still works on a
//      known-too-low version. The no-payload path is the one
//      every supported NR release actually handles, and breaking
//      it on a runtime whose version is below 5.0 would be a
//      regression of the original behaviour every existing
//      caller depends on.
//
//   4. set_context and inject_node both proceed on an unknown
//      version. The success text gains an explicit
//      "version was not detected" suffix so the caller knows
//      the call is not backed by a capability check; the policy
//      is the conservative one the issue describes — never
//      invent a successful capability check, never refuse on
//      the absence of evidence.
//
// The unit tests at the top cover RefuseForVersion directly; the
// handler tests then only need to assert on observable side
// effects.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// versionedHandler wraps a base handler so GET /settings returns
// the given version JSON, while every other request falls through
// to the original handler. /settings is the path
// NodeRedVersion probes; the rest of the fixture stays in charge
// of mocking the rest of the surface. Pass version == "" to
// leave the cache at Known=false (the base handler runs for
// /settings too — most tests pass a 500-ish handler so the
// probe fails).
func versionedHandler(version string, base http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if version != "" && r.Method == http.MethodGet && r.URL.Path == "/settings" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"version":%q}`, version)))
			return
		}
		base.ServeHTTP(w, r)
	})
}

// versionedFixture builds an httptest.Server whose /settings
// response advertises the given version. The returned Server is
// already wired with the (versioned) handler, so the first
// NodeRedVersion() call populates the cache. baseHandler may be
// nil — the default is a 200 OK with `{}`.
func versionedFixture(t *testing.T, version string, baseHandler http.Handler) (*httptest.Server, *Server) {
	t.Helper()
	if baseHandler == nil {
		baseHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		})
	}
	srv := httptest.NewServer(versionedHandler(version, baseHandler))
	t.Cleanup(srv.Close)
	c, err := nodered.NewClient(nodered.Options{BaseURL: srv.URL, BackupDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return srv, New(c, Options{Version: "test"})
}

// primeVersionCache calls NodeRedVersion() once on s so the
// cache is populated before the handler under test runs. The
// version gate calls NodeRedVersion() itself, so the test only
// needs to make the cache reflect the desired state before the
// handler executes (the handler is the place that will read the
// cache via NodeRedVersion). Returns the cached version for
// assertions.
func primeVersionCache(t *testing.T, s *Server) nodered.Version {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.nrClient.NodeRedVersion(ctx)
}

// --- RefuseForVersion unit tests -----------------------------------------

func TestRefuseForVersion_KnownTooLow_Refuses(t *testing.T) {
	refuse, msg := RefuseForVersion("set_context", nodered.Version{Major: 4, Minor: 0, Patch: 0, Known: true, Raw: "4.0.0"})
	if !refuse {
		t.Fatalf("expected refuse on 4.0.0 for set_context (min 5.0.0)")
	}
	if !strings.Contains(msg, "5.0.0") {
		t.Errorf("msg should name the required minimum 5.0.0, got %q", msg)
	}
	if !strings.Contains(msg, "4.0.0") {
		t.Errorf("msg should name the detected version 4.0.0, got %q", msg)
	}
}

func TestRefuseForVersion_KnownSupported_DoesNotRefuse(t *testing.T) {
	refuse, msg := RefuseForVersion("set_context", nodered.Version{Major: 5, Minor: 0, Patch: 1, Known: true, Raw: "5.0.1"})
	if refuse {
		t.Fatalf("expected no refuse on 5.0.1 for set_context; msg=%q", msg)
	}
	if msg != "" {
		t.Errorf("no refuse should mean empty msg, got %q", msg)
	}
}

func TestRefuseForVersion_UnknownVersion_DoesNotRefuse(t *testing.T) {
	// The cache reports Known=false when /settings was unreachable
	// or the version field was missing. Per the issue, that case
	// is "no evidence of incompatibility" — do not refuse. The
	// caller still has to be told the call is not verified, which
	// is the success-text suffix's job.
	refuse, msg := RefuseForVersion("set_context", nodered.Version{})
	if refuse {
		t.Fatalf("unknown version must not refuse (policy: proceed, surface the gap)")
	}
	if msg != "" {
		t.Errorf("unknown version has no actionable refusal msg, got %q", msg)
	}
}

func TestRefuseForVersion_NoMinimum_DoesNotRefuse(t *testing.T) {
	// list_flows has no entry in the table — it works on every
	// supported NR. The function must therefore not refuse even
	// on a very low detected version, and must not invent a msg
	// that names a minimum the tool does not have.
	refuse, msg := RefuseForVersion("list_flows", nodered.Version{Major: 1, Minor: 0, Patch: 0, Known: true, Raw: "1.0.0"})
	if refuse {
		t.Fatalf("tool with no minimum must never refuse; msg=%q", msg)
	}
	if msg != "" {
		t.Errorf("tool with no minimum has no actionable refusal msg, got %q", msg)
	}
}

// --- set_context enforcement --------------------------------------------

// TestHandleSetContext_RefusedOnTooLowVersion_NoWriteReachesFixture
// is the headline test for the set_context half of #316. The
// fixture fails the test if any write reaches it — the guard
// must sit above the lock and the helper provisioning, so a
// refused call touches no HTTP endpoint at all.
func TestHandleSetContext_RefusedOnTooLowVersion_NoWriteReachesFixture(t *testing.T) {
	var writes int32
	_, s := versionedFixture(t, "4.0.0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Catch-all that fails on any write method. GET
		// probes are not part of set_context, but if the
		// guard ever regresses and one sneaks in, we want
		// the test to know.
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
			atomic.AddInt32(&writes, 1)
			t.Errorf("refused set_context should not hit %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	if v := primeVersionCache(t, s); !v.Known || v.Major != 4 {
		t.Fatalf("seeded cache not 4.0.0, got %+v", v)
	}

	res, err := s.handleSetContext(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"scope": "global",
			"key":   "x",
			"value": `"1"`,
		}},
	})
	if err != nil {
		t.Fatalf("handler returned err=%v", err)
	}
	if res == nil {
		t.Fatal("expected a result, got nil")
	}
	if !res.IsError {
		t.Fatalf("expected an error result, got %+v", res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatal("error result has no content")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "5.0.0") {
		t.Errorf("refusal should name the required minimum 5.0.0, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "4.0.0") {
		t.Errorf("refusal should name the detected version 4.0.0, got %q", tc.Text)
	}
	if got := atomic.LoadInt32(&writes); got != 0 {
		t.Errorf("refused call reached the fixture %d times, want 0", got)
	}
	// And the helper must not have been provisioned — the
	// guard sits above the lock precisely so a refused call
	// leaves the runtime untouched.
	if s.ctxHelper != nil {
		t.Errorf("refused call provisioned the helper: %+v", s.ctxHelper)
	}
}

// TestHandleSetContext_ProceedsOnSupportedVersion asserts the
// gate does not fire when the version is known and high enough.
// A minimum helper fixture handles the provisioning chain
// (CreateFlow + AddNode×2 + ConnectNodes + inject), so the
// success path is exercised end-to-end.
func TestHandleSetContext_ProceedsOnSupportedVersion(t *testing.T) {
	_, s := versionedFixture(t, "5.0.0", setcontextSupportedHandler())
	if v := primeVersionCache(t, s); !v.Known || v.Major != 5 {
		t.Fatalf("seeded cache not 5.0.0, got %+v", v)
	}

	res, err := s.handleSetContext(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"scope": "global",
			"key":   "k",
			"value": `"v"`,
		}},
	})
	if err != nil {
		t.Fatalf("handler returned err=%v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("expected success on NR 5.0, got %+v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "Set context") {
		t.Errorf("expected the success text, got %q", tc.Text)
	}
	// The unknown-version suffix must NOT appear on a
	// known-supported run.
	if strings.Contains(tc.Text, "version was not detected") {
		t.Errorf("known-supported run should not carry the unknown-version suffix, got %q", tc.Text)
	}
}

// TestHandleSetContext_ProceedsOnUnknownVersion_WithNotice is
// the "policy for unknown versions" half of the issue: the call
// goes through, but the success text says the version could not
// be detected so the caller knows the call is not backed by a
// capability check.
func TestHandleSetContext_ProceedsOnUnknownVersion_WithNotice(t *testing.T) {
	// versionedFixture with "" routes /settings through the
	// base handler (which returns 200 with `{}` — no
	// "version" field, so the probe yields the zero
	// Version, Known=false). We deliberately do NOT call
	// primeVersionCache so the cache stays at zero
	// regardless. The version gate then sees Known=false
	// and lets the call through.
	_, s := versionedFixture(t, "", setcontextSupportedHandler())

	res, err := s.handleSetContext(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"scope": "global",
			"key":   "k",
			"value": `"v"`,
		}},
	})
	if err != nil {
		t.Fatalf("handler returned err=%v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("expected success on unknown version, got %+v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "Set context") {
		t.Errorf("expected the success text, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "version was not detected") {
		t.Errorf("success text must tell the caller the version could not be detected, got %q", tc.Text)
	}
}

// setcontextSupportedHandler returns a handler that handles the
// minimum provisioning chain (GET /flows, POST /flow, AddNode
// GET/PUT /flow/:id, ConnectNodes PUT /flow/:id, POST
// /inject/:id). The shape mirrors the live-flow fixture in
// tools_test.go: a single mutable liveFlow tracks the helper
// tab's current nodes; every PUT /flow/:id updates it so the
// next GET /flow/:id (the read half of every editFlow call)
// reflects the just-installed state. This is the smallest
// fixture that lets a known-supported set_context succeed end
// to end; the assertion in the test is on whether the gate
// fires, not on the exact wire shape.
func setcontextSupportedHandler() http.Handler {
	var (
		mu       sync.Mutex
		liveFlow = map[string]any{
			"id":    "mcp_ctx_helper_tab",
			"label": "__mcp_context_helper__",
			"nodes": []any{},
		}
	)
	refreshSnapshot := func() []byte {
		out, _ := json.Marshal(liveFlow)
		return out
	}
	ingestPUT := func(body []byte) {
		var doc struct {
			ID    string           `json:"id"`
			Label string           `json:"label"`
			Nodes []map[string]any `json:"nodes"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			return
		}
		liveFlow["id"] = doc.ID
		liveFlow["label"] = doc.Label
		liveFlow["nodes"] = doc.Nodes
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(fmt.Sprintf(
				`[{"type":"tab","id":"other","label":"Other","nodes":[]},%s]`,
				refreshSnapshot(),
			)))
		case r.Method == http.MethodPost && r.URL.Path == "/flow":
			// CreateFlow: echo the body so the response
			// carries the requested tab id
			body, _ := io.ReadAll(r.Body)
			_, _ = w.Write(body)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/flow/"):
			// GetFlow (the read half of every editFlow
			// call). Echo the live helper so the
			// read-modify-write cycle sees the
			// just-installed nodes.
			_, _ = w.Write(refreshSnapshot())
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/flow/"):
			// editFlow's write half: ingest the body
			// so the next GET reflects it.
			body, _ := io.ReadAll(r.Body)
			ingestPUT(body)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/inject/"):
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	})
}

// --- inject_node enforcement --------------------------------------------

// TestHandleInjectNode_PayloadRefusedOnTooLowVersion_NoWrite
// mirrors the set_context refusal test: a fixture that fails
// the test if any write reaches it, and a known-too-low version
// advertised via /settings. The guard is at the payload
// dispatch, not at function entry, so the no-payload path is
// unaffected (covered by
// TestHandleInjectNode_NoPayloadStillFiresOnTooLowVersion).
func TestHandleInjectNode_PayloadRefusedOnTooLowVersion_NoWrite(t *testing.T) {
	var writes int32
	_, s := versionedFixture(t, "4.0.0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
			atomic.AddInt32(&writes, 1)
			t.Errorf("refused inject_node(payload) should not hit %s %s", r.Method, r.URL.Path)
		}
		// GET /flows is the disabled/lookup probe. The
		// guard is *above* the lookup on purpose, so a
		// refused call does not even ask the runtime. Fail
		// loudly if that property ever regresses.
		if r.Method == http.MethodGet && r.URL.Path == "/flows" {
			t.Errorf("refused inject_node(payload) should not probe /flows, but handler did")
		}
		w.WriteHeader(http.StatusOK)
	}))
	if v := primeVersionCache(t, s); !v.Known || v.Major != 4 {
		t.Fatalf("seeded cache not 4.0.0, got %+v", v)
	}

	res, err := s.handleInjectNode(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"id":      "n1",
			"payload": map[string]any{"foo": 1},
		}},
	})
	if err != nil {
		t.Fatalf("handler returned err=%v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result on a too-low version, got %+v", res)
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "5.0.0") {
		t.Errorf("refusal should name the required minimum 5.0.0, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "4.0.0") {
		t.Errorf("refusal should name the detected version 4.0.0, got %q", tc.Text)
	}
	if got := atomic.LoadInt32(&writes); got != 0 {
		t.Errorf("refused call reached the fixture %d times, want 0", got)
	}
}

// TestHandleInjectNode_NoPayloadStillFiresOnTooLowVersion pins
// down the issue's explicit "preserve no-payload injection on
// versions where it actually works" requirement. The payload
// path is 5.0+; the bare POST has been there since the original
// inject_node. A guard at function entry would refuse this
// legitimate call; the issue is explicit that the guard belongs
// at the payload dispatch.
func TestHandleInjectNode_NoPayloadStillFiresOnTooLowVersion(t *testing.T) {
	var posts []capture
	_, s := versionedFixture(t, "4.0.0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := capture{method: r.Method, path: r.URL.Path}
		if r.Body != nil {
			c.body, _ = io.ReadAll(r.Body)
		}
		posts = append(posts, c)
		if r.Method == http.MethodGet && r.URL.Path == "/flows" {
			// Flat array: LookupInjectTarget walks items at
			// the top level, so the inject must be a
			// sibling of the tab, not nested under it.
			_, _ = w.Write([]byte(`[{"id":"t","type":"tab","label":"Home"},{"id":"n1","type":"inject","z":"t","name":"tick","wires":[],"x":1,"y":1}]`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	if v := primeVersionCache(t, s); !v.Known || v.Major != 4 {
		t.Fatalf("seeded cache not 4.0.0, got %+v", v)
	}

	res, err := s.handleInjectNode(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"id": "n1"}},
	})
	if err != nil {
		t.Fatalf("handler returned err=%v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("no-payload inject must still work on 4.0, got error: %+v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "fired") || strings.Contains(tc.Text, "version was not detected") {
		t.Errorf("expected plain success text, got %q", tc.Text)
	}
	var saw bool
	for _, p := range posts {
		if p.method == http.MethodPost && p.path == "/inject/n1" {
			saw = true
			if len(p.body) != 0 {
				t.Errorf("no-payload path should send an empty body, got %q", string(p.body))
			}
		}
	}
	if !saw {
		t.Errorf("expected a POST /inject/n1, got requests: %+v", posts)
	}
}

// TestHandleInjectNode_PayloadProceedsOnSupportedVersion: the
// gate must not fire when the version is high enough.
func TestHandleInjectNode_PayloadProceedsOnSupportedVersion(t *testing.T) {
	_, s := versionedFixture(t, "5.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/flows" {
			_, _ = w.Write([]byte(`[{"id":"t","type":"tab"},{"id":"n1","type":"inject","z":"t","wires":[],"x":1,"y":1}]`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	if v := primeVersionCache(t, s); !v.Known || v.Major != 5 {
		t.Fatalf("seeded cache not 5.x, got %+v", v)
	}

	res, err := s.handleInjectNode(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"id":      "n1",
			"payload": map[string]any{"foo": 2},
		}},
	})
	if err != nil {
		t.Fatalf("handler returned err=%v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("expected success on NR 5.0.1, got %+v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "payload") {
		t.Errorf("expected the payload success text, got %q", tc.Text)
	}
	if strings.Contains(tc.Text, "version was not detected") {
		t.Errorf("known-supported run should not carry the unknown-version suffix, got %q", tc.Text)
	}
}

// TestHandleInjectNode_PayloadProceedsOnUnknownVersion_WithNotice
// pins down the "policy for unknown versions" half of #316: the
// call is allowed through, but the success text carries the
// "version was not detected" suffix so the caller knows the
// call is not backed by a capability check.
func TestHandleInjectNode_PayloadProceedsOnUnknownVersion_WithNotice(t *testing.T) {
	// versionedFixture with "" routes /settings through the
	// base handler (which returns 200 with `{}` — no
	// "version" field, so the probe yields zero Version,
	// Known=false). The version gate then sees Known=false
	// and lets the call through.
	_, s := versionedFixture(t, "", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/flows" {
			_, _ = w.Write([]byte(`[{"id":"t","type":"tab"},{"id":"n1","type":"inject","z":"t","wires":[],"x":1,"y":1}]`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	res, err := s.handleInjectNode(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"id":      "n1",
			"payload": map[string]any{"foo": 3},
		}},
	})
	if err != nil {
		t.Fatalf("handler returned err=%v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("expected success on unknown version, got %+v", res.Content)
	}
	tc := res.Content[0].(mcp.TextContent)
	if !strings.Contains(tc.Text, "payload") {
		t.Errorf("expected the payload success text, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "version was not detected") {
		t.Errorf("unknown-version payload inject must carry the notice suffix, got %q", tc.Text)
	}
}

// TestHandleInjectNode_NoPayloadBodyShape is a sanity check on
// the wire body for the no-payload path on a known-supported
// runtime: the body must be empty so the original behaviour
// (and the no-version-gate promise of Decision 1) is
// observable end-to-end.
func TestHandleInjectNode_NoPayloadBodyShape(t *testing.T) {
	var posts []capture
	_, s := versionedFixture(t, "5.0.0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		posts = append(posts, capture{method: r.Method, path: r.URL.Path, body: body})
		if r.Method == http.MethodGet && r.URL.Path == "/flows" {
			_, _ = w.Write([]byte(`[{"id":"t","type":"tab"},{"id":"n1","type":"inject","z":"t","wires":[],"x":1,"y":1}]`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	if v := primeVersionCache(t, s); !v.Known || v.Major != 5 {
		t.Fatalf("seeded cache not 5.0.0, got %+v", v)
	}

	if _, err := s.handleInjectNode(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{"id": "n1"}},
	}); err != nil {
		t.Fatalf("handleInjectNode: %v", err)
	}
	var post capture
	for _, p := range posts {
		if p.method == http.MethodPost && p.path == "/inject/n1" {
			post = p
		}
	}
	if post.method == "" {
		t.Fatalf("expected POST /inject/n1, got: %+v", posts)
	}
	if !bytes.Equal(post.body, nil) && len(post.body) != 0 {
		// The Client sends []byte{} for the no-body case;
		// both nil and empty are acceptable.
		t.Errorf("expected no body, got %q", string(post.body))
	}
	_ = json.RawMessage{} // keep the encoding/json import in case the test grows
}
