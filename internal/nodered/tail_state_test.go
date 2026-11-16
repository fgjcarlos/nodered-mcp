package nodered

import (
	"errors"
	"testing"
)

// Issue #341: the capability matrix needs to know what the MCP
// observed of a tail — connected, failed, or still connecting — without
// copying the message buffer. Snapshot would copy every buffered
// message, so the tails expose their connection state on its own.

// A fresh tail has never dialled: not connected, no error recorded.
func TestDebugTailStateReportsConnection(t *testing.T) {
	tail := newDebugTail("ws://127.0.0.1:1/comms", "", false, 4)
	if connected, lastErr := tail.State(); connected || lastErr != "" {
		t.Errorf("fresh tail = (connected=%v, lastError=%q), want (false, \"\")", connected, lastErr)
	}
	tail.setState(false, errors.New("connection refused"))
	if connected, lastErr := tail.State(); connected || lastErr == "" {
		t.Errorf("after a failed dial = (connected=%v, lastError=%q), want (false, non-empty)", connected, lastErr)
	}
	tail.setState(true, nil)
	if connected, lastErr := tail.State(); !connected || lastErr != "" {
		t.Errorf("after connecting = (connected=%v, lastError=%q), want (true, \"\")", connected, lastErr)
	}
}

// The status tail keeps the same contract as the debug tail.
func TestStatusTailStateReportsConnection(t *testing.T) {
	tail := newStatusTail("ws://127.0.0.1:1/comms", "", false)
	if connected, lastErr := tail.State(); connected || lastErr != "" {
		t.Errorf("fresh tail = (connected=%v, lastError=%q), want (false, \"\")", connected, lastErr)
	}
	tail.setState(false, errors.New("connection refused"))
	if connected, lastErr := tail.State(); connected || lastErr == "" {
		t.Errorf("after a failed dial = (connected=%v, lastError=%q), want (false, non-empty)", connected, lastErr)
	}
	tail.setState(true, nil)
	if connected, lastErr := tail.State(); !connected || lastErr != "" {
		t.Errorf("after connecting = (connected=%v, lastError=%q), want (true, \"\")", connected, lastErr)
	}
}
