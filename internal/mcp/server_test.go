package mcp

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/fgjcarlos/nodered-mcp/internal/nodered"
)

// TestWithTimeoutRetry_HungHandlerSurfacesInTime is the headline regression
// for issue #42: a handler that never returns must surface a tool error
// well under the 4-minute hang the audit observed. We give the wrapper
// an outer ctx that expires in 100ms — fast for CI, still exercises the
// same deadline + retry path that production hits when toolTimeout fires.
func TestWithTimeoutRetry_HungHandlerSurfacesInTime(t *testing.T) {
	var calls atomic.Int32
	handler := func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		select {
		case <-time.After(5 * time.Second):
			return mcp.NewToolResultText("should not reach"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	s := &Server{}
	wrapped := s.withTimeoutRetry("hung", server.ToolHandlerFunc(handler))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := wrapped(ctx, mcp.CallToolRequest{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from hung handler, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded wrapping, got %v", err)
	}
	// One attempt fits inside the 100ms outer deadline; the wrapper
	// should have tried once and bailed. We don't assert an exact count
	// because the inner callCtx has its own deadline layered on top —
	// what matters is the wall-clock.
	if elapsed > 2*time.Second {
		t.Errorf("wrapper took %s; expected well under the outer 100ms deadline", elapsed)
	}
	if calls.Load() < 1 {
		t.Errorf("expected at least 1 attempt, got %d", calls.Load())
	}
}

// TestWithTimeoutRetry_RetriesUntilSuccess covers the second half of the
// audit fix: an intermittent failure that succeeds on retry must reach
// the success path without leaking the intermediate error.
func TestWithTimeoutRetry_RetriesUntilSuccess(t *testing.T) {
	var calls atomic.Int32
	handler := func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		n := calls.Add(1)
		if n < 2 {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection reset")}
		}
		return mcp.NewToolResultText("ok"), nil
	}

	s := &Server{}
	wrapped := s.withTimeoutRetry("flaky", server.ToolHandlerFunc(handler))

	res, err := wrapped(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("expected success on retry, got error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("expected 2 attempts (1 fail + 1 success), got %d", got)
	}
}

// TestWithTimeoutRetry_NoRetryOnAPIError covers the audit's explicit
// constraint: 4xx/5xx responses are the server answering, not hanging.
// Retrying would just spam it and could double-write.
func TestWithTimeoutRetry_NoRetryOnAPIError(t *testing.T) {
	var calls atomic.Int32
	handler := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return nil, &nodered.APIError{StatusCode: 404, Method: "GET", Path: "/flows", Body: "Cannot GET /flows"}
	}

	s := &Server{}
	wrapped := s.withTimeoutRetry("api-err", server.ToolHandlerFunc(handler))

	_, err := wrapped(context.Background(), mcp.CallToolRequest{})
	if err == nil {
		t.Fatal("expected APIError to surface, got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected 1 attempt (no retry on API error), got %d", got)
	}
}

// TestWithTimeoutRetry_SucceedsOnFirstCall covers the happy path: a
// handler that returns nil on the first call must not be re-run. This is
// the case that prevents accidental double-execution of side-effectful
// tools (the audit called this out as the reason the retry policy is
// "retry only on transport error").
func TestWithTimeoutRetry_SucceedsOnFirstCall(t *testing.T) {
	var calls atomic.Int32
	handler := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return mcp.NewToolResultText("ok"), nil
	}

	s := &Server{}
	wrapped := s.withTimeoutRetry("ok", server.ToolHandlerFunc(handler))

	_, err := wrapped(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected exactly 1 call, got %d", got)
	}
}

// TestWithTimeoutRetry_NoRetryOnNonTransport wraps the safety net for
// the audit's "no side-effect double-run" constraint: an error that
// looks like a server response (not a transport failure) must surface
// immediately without retrying.
func TestWithTimeoutRetry_NoRetryOnNonTransport(t *testing.T) {
	var calls atomic.Int32
	handler := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		// Use NewToolResultError so the handler signals via the result
		// (the audit-confirmed convention). Text does not match any
		// transport marker; the wrapper must NOT retry.
		return mcp.NewToolResultError("validation: missing required field"), nil
	}

	s := &Server{}
	wrapped := s.withTimeoutRetry("validation", server.ToolHandlerFunc(handler))

	res, err := wrapped(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("expected nil error (result carries the failure), got %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected IsError result, got %+v", res)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected exactly 1 call (no retry on non-transport error), got %d", got)
	}
}

// TestWithTimeoutRetry_RetriesOnTransportResult covers the case that
// the audit's headline fix exists for: the handler returns a result
// whose IsError is true AND whose first text content carries a
// transport marker (the universal pattern across all 29 tools). The
// wrapper must retry.
func TestWithTimeoutRetry_RetriesOnTransportResult(t *testing.T) {
	var calls atomic.Int32
	handler := func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		n := calls.Add(1)
		if n < 2 {
			// Mirror what nodered.Client.do emits when its 30s defaultTimeout
			// fires before the wrapper's 15s — the handler sees ctx.Err()
			// and rewrites it via NewToolResultError.
			return mcp.NewToolResultError("calling GET http://localhost:1880/flows: context deadline exceeded"), nil
		}
		return mcp.NewToolResultText("ok"), nil
	}

	s := &Server{}
	wrapped := s.withTimeoutRetry("hung", server.ToolHandlerFunc(handler))

	res, err := wrapped(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("expected success on retry, got error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("expected 2 attempts (1 fail + 1 success), got %d", got)
	}
}