package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestSetContextReviewCanceledWriteWaiter(t *testing.T) {
	for _, preCanceled := range []bool{true, false} {
		t.Run(fmt.Sprintf("preCanceled=%v", preCanceled), func(t *testing.T) {
			rt := &helperRuntime{}
			armed, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" && len(r.URL.Path) > 8 && r.URL.Path[:8] == "/inject/" {
					close(armed)
					<-release
				}
				rt.serve(w, r)
			}))
			defer h.Close()
			defer once.Do(func() { close(release) })
			s := newTestServerAt(t, h.URL, t.TempDir())
			first := make(chan error, 1)
			go func() {
				result, err := callHelperTool(context.Background(), s, "set_context", map[string]any{"scope": "global", "key": "audit", "value": "42"})
				if err == nil && result.IsError {
					err = fmt.Errorf("set_context: %+v", result)
				}
				first <- err
			}()
			select {
			case <-armed:
			case <-time.After(time.Second):
				t.Fatal("inject not armed")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if preCanceled {
				cancel()
			}
			done := make(chan error, 1)
			go func() {
				result, err := callHelperTool(ctx, s, "install_node", map[string]any{"module": "example"})
				if err == nil && (!result.IsError || resultFirstText(result) != "waiting for context helper lock: context canceled; no action was started") {
					err = fmt.Errorf("unexpected cancellation: %+v", result)
				}
				done <- err
			}()
			if !preCanceled {
				select {
				case err := <-done:
					t.Fatalf("waiter returned before cancellation: %v", err)
				case <-time.After(20 * time.Millisecond):
				}
				cancel()
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(100 * time.Millisecond):
				t.Fatal("canceled write waited for unrelated inject lock")
			}
			once.Do(func() { close(release) })
			if err := <-first; err != nil {
				t.Fatal(err)
			}
			rt.mu.Lock()
			defer rt.mu.Unlock()
			if rt.creates != 1 || rt.injects != 1 || len(rt.items) != 3 {
				t.Fatalf("canceled write changed state: creates=%d injects=%d entries=%d", rt.creates, rt.injects, len(rt.items))
			}
		})
	}
}

func TestSetContextReviewCollisionTarget(t *testing.T) {
	rt := &helperRuntime{items: []json.RawMessage{json.RawMessage(`{"id":"user","type":"tab","label":"User"}`), json.RawMessage(`{"id":"mcp_ctx_helper_fn","type":"function","z":"user","func":"return msg;"}`)}}
	h := httptest.NewServer(http.HandlerFunc(rt.serve))
	defer h.Close()
	s := newTestServerAt(t, h.URL, t.TempDir())
	before, _ := json.Marshal(rt.items)
	res, err := callHelperTool(context.Background(), s, "set_context", map[string]any{"scope": "node", "id": "mcp_ctx_helper_fn", "key": "audit", "value": "42"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("error=%v result=%s injects=%d", res.IsError, resultFirstText(res), rt.injects)
	after, _ := json.Marshal(rt.items)
	if !res.IsError || rt.injects != 0 || rt.creates != 0 || string(before) != string(after) {
		t.Fatal("explicit unrelated node ID silently targets replacement helper")
	}
}

func TestSetContextReviewOldVersionScopeError(t *testing.T) {
	_, s := versionedFixture(t, "4.0.0", nil)
	primeVersionCache(t, s)
	for _, scope := range []string{"flow", "node"} {
		res, err := callHelperTool(context.Background(), s, "set_context", map[string]any{"scope": scope, "key": "audit", "value": "42"})
		if err != nil || !res.IsError {
			t.Fatalf("%+v %v", res, err)
		}
		_, expected := RefuseForVersion("set_context", s.nrClient.CachedNodeRedVersion())
		if got := resultFirstText(res); got != expected {
			t.Errorf("%s: got %q want baseline %q", scope, got, expected)
		}
	}
}

func TestSetContextReviewForeignTargetText(t *testing.T) {
	rt := &helperRuntime{}
	h := httptest.NewServer(http.HandlerFunc(rt.serve))
	defer h.Close()
	s := newTestServerAt(t, h.URL, t.TempDir())
	if res := helperCall(t, s); res.IsError {
		t.Fatalf("setup: %+v", res)
	}
	for _, scope := range []string{"flow", "node"} {
		before, _ := json.Marshal(rt.items)
		res, err := callHelperTool(context.Background(), s, "set_context", map[string]any{"scope": scope, "id": "foreign", "key": "audit", "value": "42"})
		if err != nil || !res.IsError {
			t.Fatalf("%+v %v", res, err)
		}
		expected := `scope "flow" can only target the helper's own flow context, so the id must be the helper flow id (got "foreign"); call list_flows to discover it`
		if scope == "node" {
			expected = `scope "node" can only target the helper's function node's own context, so the id must be the helper function id (got "foreign")`
		}
		if got := resultFirstText(res); got != expected {
			t.Errorf("%s: got %q; want %q", scope, got, expected)
		}
		after, _ := json.Marshal(rt.items)
		if string(before) != string(after) || rt.injects != 1 || rt.creates != 1 {
			t.Fatal("rejected call mutated state")
		}
	}
}
