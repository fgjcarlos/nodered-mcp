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
	if connected, ever, lastErr := tail.State(); connected || ever || lastErr != "" {
		t.Errorf("fresh tail = (connected=%v, everConnected=%v, lastError=%q), want (false, false, \"\")", connected, ever, lastErr)
	}
	tail.setState(false, errors.New("connection refused"))
	if connected, ever, lastErr := tail.State(); connected || ever || lastErr == "" {
		t.Errorf("after a failed dial = (connected=%v, everConnected=%v, lastError=%q), want (false, false, non-empty)", connected, ever, lastErr)
	}
	tail.setState(true, nil)
	if connected, ever, lastErr := tail.State(); !connected || !ever || lastErr != "" {
		t.Errorf("after connecting = (connected=%v, everConnected=%v, lastError=%q), want (true, true, \"\")", connected, ever, lastErr)
	}
	// A silent drop keeps the "it did connect" memory, so the matrix
	// can tell a lost connection from one that never came up.
	tail.setState(false, nil)
	if connected, ever, _ := tail.State(); connected || !ever {
		t.Errorf("after a silent drop = (connected=%v, everConnected=%v), want (false, true)", connected, ever)
	}
}

// The status tail keeps the same contract as the debug tail.
func TestStatusTailStateReportsConnection(t *testing.T) {
	tail := newStatusTail("ws://127.0.0.1:1/comms", "", false)
	if connected, ever, lastErr := tail.State(); connected || ever || lastErr != "" {
		t.Errorf("fresh tail = (connected=%v, everConnected=%v, lastError=%q), want (false, false, \"\")", connected, ever, lastErr)
	}
	tail.setState(false, errors.New("connection refused"))
	if connected, ever, lastErr := tail.State(); connected || ever || lastErr == "" {
		t.Errorf("after a failed dial = (connected=%v, everConnected=%v, lastError=%q), want (false, false, non-empty)", connected, ever, lastErr)
	}
	tail.setState(true, nil)
	if connected, ever, lastErr := tail.State(); !connected || !ever || lastErr != "" {
		t.Errorf("after connecting = (connected=%v, everConnected=%v, lastError=%q), want (true, true, \"\")", connected, ever, lastErr)
	}
	// A silent drop keeps the "it did connect" memory, so the matrix
	// can tell a lost connection from one that never came up.
	tail.setState(false, nil)
	if connected, ever, _ := tail.State(); connected || !ever {
		t.Errorf("after a silent drop = (connected=%v, everConnected=%v), want (false, true)", connected, ever)
	}
}
