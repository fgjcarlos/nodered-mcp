package mcp

// Issue #310 — Context helper synchronization regressions.
//
// The Server.ctxHelper pointer and the metadata it carries are shared
// across every goroutine that calls set_context, plus restore_backup
// (which must invalidate the pointer on a successful restore). The
// regression set below exercises the production seam directly: the
// httptest Node-RED fixture is a controlled barrier, not a mock
// implementation, so the test asserts on the real call graph
// (ensureSetContextHelper → provisionSetContextHelper → nrClient
// methods, and handleSetContext → injectWithProvisioningRetry, and
// handleRestoreBackup → nrClient.RestoreFlows).
//
// What each test pins down:
//
//   1. TestSetContextHelper_ConcurrentFirstCallersProvisionOnce
//      At most one provisioning for simultaneous first-callers.
//      Before the fix, the second-and-later goroutines race past the
//      `if provisioned()` check and each create their own
//      &setContextHelper{} and run the full provision flow (CreateFlow
//      + AddNode + AddNode + ConnectNodes = 4 NR calls * N goroutines).
//
//   2. TestSetContextHelper_ProvisioningFailureIsRetryable
//      A failed provisioning must leave s.ctxHelper in a state where
//      the next call can retry from scratch. Before the fix, the
//      placeholder was rolled back, but a half-failed provision on the
//      SAME placeholder left a non-nil helper in a not-provisioned
//      state for the next call to deal with.
//
//   3. TestSetContext_RestoreInterleavesWithSetContext
//      After handleRestoreBackup completes successfully, the next
//      handleSetContext must NOT use the pre-restore helper. The
//      test uses a controlled barrier on POST /inject/:id so the
//      in-flight set_context is observably concurrent with the
//      restore; the assertion is on the Server.ctxHelper pointer
//      state, which is the production seam the issue names.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// newTestServerAt is a local helper that mirrors newTestServer in
// tools_test.go but with an injected BaseURL — used to point the
// Server at our controlled httptest fixture.
func newTestServerAt(t *testing.T, baseURL, backupDir string) *Server {
	t.Helper()
	c, err := nodered.NewClient(nodered.Options{BaseURL: baseURL, BackupDir: backupDir})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return New(c, Options{Version: "test"})
}

// TestSetContextHelper_ConcurrentFirstCallersProvisionOnce spins up
// N goroutines that all hit the first-call provisioning path at the
// same time. The fixture's POST /flow handler is a barrier: it
// blocks until released, so every goroutine is guaranteed to be
// racing at the same instant. After release, the assertion is on
// (a) the number of CreateFlow calls the runtime saw, and (b) all
// callers observe the same non-nil helper.
func TestSetContextHelper_ConcurrentFirstCallersProvisionOnce(t *testing.T) {
	const N = 16

	var createFlow atomic.Int64
	release := make(chan struct{})
	// The fixture tracks the live flow state across requests so
	// successive GET /flow/:id reads see the previous PUTs — same
	// as a real Node-RED. Without this, ConnectNodes would see an
	// empty nodes list and reject the wire (production reads the
	// in-memory flow, fixture must mirror that).
	var liveMu sync.Mutex
	liveFlow := []byte(`{"id":"runtime_helper_tab","label":"__mcp_context_helper__","nodes":[]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			// snapshotFlows inside CreateFlow backs up the
			// current config before the write.
			_, _ = w.Write([]byte(`[]`))
		case r.Method == "POST" && r.URL.Path == "/flow":
			createFlow.Add(1)
			<-release
			_, _ = w.Write(liveFlow)
		case r.Method == "GET" && r.URL.Path == "/flow/runtime_helper_tab":
			liveMu.Lock()
			body := append([]byte(nil), liveFlow...)
			liveMu.Unlock()
			_, _ = w.Write(body)
		case r.Method == "PUT" && r.URL.Path == "/flow/runtime_helper_tab":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("reading flow update: %v", err)
				http.Error(w, "read", http.StatusBadRequest)
				return
			}
			liveMu.Lock()
			liveFlow = body
			liveMu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s in concurrent-first-callers fixture", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	s := newTestServerAt(t, srv.URL, t.TempDir())

	var wg sync.WaitGroup
	results := make([]*setContextHelper, N)
	errs := make([]error, N)
	ready := make(chan struct{})
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// The caller (handleSetContext in production)
			// takes s.ctxHelperMu before calling
			// ensureSetContextHelper. The test mirrors that
			// contract so the lock guards the critical
			// section the same way it does in production.
			s.ctxHelperMu.Lock()
			defer s.ctxHelperMu.Unlock()
			results[i], _, errs[i] = s.ensureSetContextHelper(context.Background())
		}(i)
	}
	// Give every goroutine a moment to reach the lock; this is
	// scheduling latency, not test time, so a tiny sleep is honest.
	// The barrier on POST /flow does the real synchronization
	// (every goroutine behind the lock will be parked there until
	// we close `release`).
	//
	// ponytail: a single sync.Cond on a "all N reached" signal would
	// be tighter; the 5ms ceiling is the upgrade path if a future
	// test wants the first attempt at the lock to be deterministic
	// on a slow CI runner.
	time.Sleep(5 * time.Millisecond)
	close(ready)
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("ensureSetContextHelper goroutine %d failed: %v", i, err)
		}
	}
	for i, h := range results {
		if h == nil {
			t.Errorf("goroutine %d got nil helper", i)
			continue
		}
		if !h.provisioned() {
			t.Errorf("goroutine %d got unprovisioned helper: %+v", i, h)
		}
	}

	// The first goroutine to acquire the lock provisions; the rest
	// re-check under the lock and skip. Exactly one CreateFlow
	// should reach the runtime.
	if got := createFlow.Load(); got != 1 {
		t.Errorf("expected exactly 1 CreateFlow call under concurrent first-callers, got %d", got)
	}
}

// TestSetContextHelper_ProvisioningFailureIsRetryable forces the
// first provision to fail on the runtime (HTTP 500 from POST /flow),
// and then re-points the fixture to a success path. The second
// ensureSetContextHelper call must observe a nil s.ctxHelper (i.e.
// the failed placeholder was rolled back) and provision successfully.
func TestSetContextHelper_ProvisioningFailureIsRetryable(t *testing.T) {
	var createFlow atomic.Int64
	var fail atomic.Bool
	fail.Store(true)
	var liveMu sync.Mutex
	liveFlow := []byte(`{"id":"runtime_helper_tab","label":"__mcp_context_helper__","nodes":[]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == "POST" && r.URL.Path == "/flow":
			createFlow.Add(1)
			if fail.Load() {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write(liveFlow)
		case r.Method == "GET" && r.URL.Path == "/flow/runtime_helper_tab":
			liveMu.Lock()
			body := append([]byte(nil), liveFlow...)
			liveMu.Unlock()
			_, _ = w.Write(body)
		case r.Method == "PUT" && r.URL.Path == "/flow/runtime_helper_tab":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("reading flow update: %v", err)
				http.Error(w, "read", http.StatusBadRequest)
				return
			}
			liveMu.Lock()
			liveFlow = body
			liveMu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s in provisioning-failure fixture", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	s := newTestServerAt(t, srv.URL, t.TempDir())

	h1, just, err := func() (*setContextHelper, bool, error) {
		s.ctxHelperMu.Lock()
		defer s.ctxHelperMu.Unlock()
		return s.ensureSetContextHelper(context.Background())
	}()
	if err == nil {
		t.Fatalf("expected first ensureSetContextHelper to fail (500 from runtime), got helper=%+v justProvisioned=%v", h1, just)
	}
	if h1 != nil {
		t.Errorf("failed provisioning must return a nil helper, got %+v", h1)
	}
	if s.ctxHelper != nil {
		t.Errorf("failed provisioning must clear the placeholder, got s.ctxHelper=%+v", s.ctxHelper)
	}
	if createFlow.Load() != 1 {
		t.Errorf("expected 1 CreateFlow attempt before the failure, got %d", createFlow.Load())
	}

	// Runtime recovers. The next call must retry from scratch.
	fail.Store(false)
	h2, just, err := func() (*setContextHelper, bool, error) {
		s.ctxHelperMu.Lock()
		defer s.ctxHelperMu.Unlock()
		return s.ensureSetContextHelper(context.Background())
	}()
	if err != nil {
		t.Fatalf("retry ensureSetContextHelper failed: %v", err)
	}
	if h2 == nil || !h2.provisioned() {
		t.Errorf("retry returned an unprovisioned helper: %+v", h2)
	}
	if !just {
		t.Error("retry should report justProvisioned=true (it ran the provision path)")
	}
	if got := createFlow.Load(); got != 2 {
		t.Errorf("expected 2 CreateFlow calls total (failed + retry), got %d", got)
	}
}

// TestSetContext_RestoreCannotDeployWhileInFlightInjectHoldsLock
// proves the issue #310 acceptance criterion directly: while a
// set_context is in flight (parked on the POST /inject/:id barrier
// and still holding Server.ctxHelperMu), a restore_backup must NOT
// be able to deploy POST /flows against the runtime. The runtime
// fixture parks POST /flows behind a barrier and signals when the
// request arrives; the test asserts the restore's deploy step is
// observably blocked on the in-flight set_context's lock, not
// racing past it. The barrier replaces the previous "wait 50ms and
// check the final pointer" stub: that stub only proved the helper
// ended up nil, not that the deploy was serialized with the inject.
//
// Concurrency shape:
//
//   - set_context goroutine: pre-warm completes (helper provisioned
//     and lock released), then the in-flight call takes the lock,
//     runs ensureSetContextHelper, validates, and dispatches the
//     inject. The inject handler parks on the barrier and the
//     goroutine stays parked there — the lock is still held
//     because the inject is the LAST step under Lock() in
//     handleSetContext.
//   - restore goroutine: takes the same lock (issue #310 fix:
//     ctxHelperMu is held across the whole HTTP work, not just
//     the pointer clear). It is therefore blocked on the lock for
//     the entire duration of the parked inject. POST /flows is
//     never called until the lock is released.
//   - Test thread: waits for injectArmed, then asserts deployArrived
//     has NOT been signalled (bounded wait — a real observation, not
//     a sleep). Then releases the inject, the in-flight call returns
//     and releases the lock, the restore acquires the lock and runs
//     its deploy step (signal deployArrived, then park on
//     deployRelease), the test releases deployRelease, and finally
//     asserts pointer state and the next-call re-provision.
func TestSetContext_RestoreCannotDeployWhileInFlightInjectHoldsLock(t *testing.T) {
	backupDir := t.TempDir()
	// A backup that, when restored, must produce a different flow
	// id than the runtime's current tab. The Server's helper
	// pointer is keyed on the runtime's tab id, so a restore that
	// re-points the tab invalidates the helper.
	backupName := "flows-interleave.json"
	if err := os.WriteFile(filepath.Join(backupDir, backupName), []byte(`[{"type":"tab","id":"restored","label":"Restored","nodes":[]}]`), 0o600); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	// Channels to control the interleaving deterministically.
	// injectArmed: closed by the inject handler when the in-flight
	// call is parked (proves the goroutine is past the lock and
	// stuck at the inject).
	// injectRelease: closed by the test to release the inject.
	// deployArmed: closed by the POST /flows handler the moment it
	// is called. Used to assert the restore's deploy step is
	// blocked on the lock, not racing past it.
	// deployRelease: closed by the test to release the deploy.
	injectArmed := make(chan struct{})
	injectRelease := make(chan struct{})
	deployArmed := make(chan struct{})
	deployRelease := make(chan struct{})

	var injectCount atomic.Int64
	var restoreCount atomic.Int64
	var liveMu sync.Mutex
	// liveFlows maps flow id -> its current body. The runtime
	// fixture keeps the in-memory flow state here so successive
	// GET /flow/:id reads see the previous writes (same as real
	// Node-RED), and so the restore can REPLACE the state with
	// the new tab. Without this, AddNode would see an empty
	// nodes list and reject the wire (production reads the
	// in-memory flow, fixture must mirror that).
	liveFlows := map[string][]byte{
		"runtime_helper_tab": []byte(`{"id":"runtime_helper_tab","label":"__mcp_context_helper__","nodes":[]}`),
	}
	// nextFlowID is what CreateFlow returns to the caller as the
	// runtime-assigned id. The Server's provisioning step trusts
	// the response, so the fixture has to return a stable id
	// per created flow.
	var nextFlowID int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/flow":
			nextFlowID++
			liveMu.Lock()
			id := fmt.Sprintf("mcp_ctx_helper_tab_%d", nextFlowID)
			body, _ := io.ReadAll(r.Body)
			// Rewrite the body so the stored doc carries the
			// runtime-assigned id. AddNode reads GET /flow/:id
			// and validates the new node's `z` against the
			// doc's id; without this rewrite the validation
			// would fail and the helper could not be wired.
			var asMap map[string]any
			if err := json.Unmarshal(body, &asMap); err == nil {
				asMap["id"] = id
				rewritten, _ := json.Marshal(asMap)
				liveFlows[id] = rewritten
			} else {
				liveFlows[id] = body
			}
			liveMu.Unlock()
			// Respond with the runtime-assigned id so the
			// Server captures it in extractFlowID and uses
			// it for the follow-up AddNode calls.
			_, _ = w.Write([]byte(fmt.Sprintf(`{"id":%q,"label":"__mcp_context_helper__","nodes":[]}`, id)))
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/flow/"):
			id := strings.TrimPrefix(r.URL.Path, "/flow/")
			liveMu.Lock()
			body, ok := liveFlows[id]
			liveMu.Unlock()
			if !ok {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			_, _ = w.Write(body)
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/flow/"):
			id := strings.TrimPrefix(r.URL.Path, "/flow/")
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("reading flow update: %v", err)
				http.Error(w, "read", http.StatusBadRequest)
				return
			}
			liveMu.Lock()
			liveFlows[id] = body
			liveMu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/inject/"):
			injectCount.Add(1)
			// The second (in-flight) inject parks on the
			// barrier. The first (pre-warm) inject returns
			// immediately so the pre-warm set_context
			// completes and frees the helper lock. We arm
			// the barrier only for the in-flight call so
			// the test thread blocks on the right inject.
			if injectCount.Load() == 2 {
				close(injectArmed)
				<-injectRelease
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == "GET" && r.URL.Path == "/flows":
			// List the current runtime state (used by the
			// pre-restore snapshot inside RestoreFlows
			// and by the pre-warm provisioning step).
			liveMu.Lock()
			tabs := make([][]byte, 0, len(liveFlows))
			for _, body := range liveFlows {
				tabs = append(tabs, body)
			}
			liveMu.Unlock()
			arr, _ := json.Marshal(tabs)
			_, _ = w.Write(arr)
		case r.Method == "POST" && r.URL.Path == "/flows":
			// The deploy step. With the fix, this is reached
			// only after ctxHelperMu is released by the
			// in-flight set_context — the test asserts that
			// property by reading deployArmed before
			// releasing the inject. The deploy body REPLACES
			// the runtime state (a real restore), so the
			// post-restore set_context call re-provisions
			// against an empty runtime.
			restoreCount.Add(1)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("reading restore body: %v", err)
				http.Error(w, "read", http.StatusBadRequest)
				return
			}
			close(deployArmed)
			<-deployRelease
			liveMu.Lock()
			liveFlows = map[string][]byte{}
			var newFlows []map[string]any
			if err := json.Unmarshal(body, &newFlows); err == nil {
				for _, f := range newFlows {
					if id, _ := f["id"].(string); id != "" {
						b, _ := json.Marshal(f)
						liveFlows[id] = b
					}
				}
			}
			liveMu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s in restore-interleaves fixture", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	s := newTestServerAt(t, srv.URL, backupDir)

	// Pre-warm: provision a helper via a synchronous set_context
	// call. This is the helper the restore will invalidate.
	if _, err := s.handleSetContext(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"scope": "global",
			"key":   "seed",
			"value": "1",
		}},
	}); err != nil {
		t.Fatalf("pre-warm set_context: %v", err)
	}
	if s.ctxHelper == nil {
		t.Fatalf("pre-warm set_context did not provision a helper")
	}
	originalHelper := s.ctxHelper

	// Goroutine A: in-flight set_context. It will park on the
	// inject barrier until injectRelease is closed. While parked,
	// it is still holding Server.ctxHelperMu (the inject dispatch
	// is the last statement inside the Lock()/defer Unlock() block
	// in handleSetContext).
	setDone := make(chan error, 1)
	go func() {
		_, err := s.handleSetContext(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Arguments: map[string]any{
				"scope": "global",
				"key":   "interleaved",
				"value": "2",
			}},
		})
		setDone <- err
	}()

	// Wait for the inject to be armed (proves goroutine A is past
	// the provisioning step and parked on the barrier with the
	// lock still held).
	select {
	case <-injectArmed:
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight set_context never reached the inject barrier")
	}

	// Goroutine B: restore. The fix moves ctxHelperMu.Lock() to
	// before the HTTP work, so the restore blocks on the lock
	// for the entire duration of the parked inject. Before the
	// fix, the lock was only held around the pointer clear, so
	// POST /flows was reached while the inject was still parked
	// — a real race, not just a missing pointer invalidation.
	restoreDone := make(chan error, 1)
	go func() {
		_, err := s.handleRestoreBackup(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Arguments: map[string]any{"backup": backupName}},
		})
		restoreDone <- err
	}()

	// The whole point of this test: while the inject is parked
	// (and the lock is held by handleSetContext), the restore's
	// POST /flows must NOT have been called yet. The fixture
	// closes deployArmed the instant the deploy step runs, so a
	// brief bounded wait here is the OBSERVATION, not a sleep:
	// the test fails immediately if the deploy raced past the
	// in-flight set_context.
	select {
	case <-deployArmed:
		t.Fatal("restore deployed POST /flows while an in-flight set_context was parked; ctxHelperMu did not serialize the restore's HTTP work with the inject")
	case <-time.After(200 * time.Millisecond):
		// Expected: the deploy step is still blocked behind
		// the lock. Fall through and continue.
	}

	// Release the inject so goroutine A finishes and releases the
	// lock. The restore (blocked on s.ctxHelperMu.Lock()) will
	// then run its deploy step.
	close(injectRelease)
	select {
	case err := <-setDone:
		if err != nil {
			t.Fatalf("in-flight set_context failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight set_context never returned after inject release")
	}

	// The deploy must now have arrived (it was unblocked by the
	// lock release above). Release it so the restore can finish.
	select {
	case <-deployArmed:
	case <-time.After(5 * time.Second):
		t.Fatal("restore never reached POST /flows after the in-flight set_context released the lock")
	}
	close(deployRelease)
	select {
	case err := <-restoreDone:
		if err != nil {
			t.Fatalf("restore_backup failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("restore_backup never returned after deploy release")
	}

	// The acceptance criterion: after a successful restore, the
	// Server's helper pointer is cleared. The next set_context
	// call will observe nil and re-provision against the new
	// runtime state.
	if s.ctxHelper != nil {
		t.Errorf("restore did not clear s.ctxHelper; got %+v (was %+v before)", s.ctxHelper, originalHelper)
	}
	if restoreCount.Load() != 1 {
		t.Errorf("expected exactly 1 restore deploy, got %d", restoreCount.Load())
	}
	if injectCount.Load() != 2 {
		// 1 from pre-warm + 1 from the in-flight set_context.
		t.Errorf("expected 2 injects (pre-warm + in-flight), got %d", injectCount.Load())
	}

	// The next call must observe nil and re-provision against
	// the new runtime state. This is the user-visible
	// consequence of the invalidation: a stale helper is not
	// just cleared, it is replaced on the next call.
	//
	// ponytail: a tighter test could install a known-original
	// helper and assert the new one is a distinct pointer; the
	// "re-provisioned, not nil" check below is the acceptance
	// contract the issue names.
	if _, err := s.handleSetContext(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"scope": "global",
			"key":   "after-restore",
			"value": "3",
		}},
	}); err != nil {
		t.Fatalf("post-restore set_context failed: %v", err)
	}
	if s.ctxHelper == nil {
		t.Fatal("post-restore set_context did not re-provision a helper")
	}
	if s.ctxHelper == originalHelper {
		t.Error("post-restore set_context reused the pre-restore helper pointer; invalidation did not take effect")
	}
}
