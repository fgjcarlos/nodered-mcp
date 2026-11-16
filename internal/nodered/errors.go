package nodered

// TransportError marks an HTTP exchange that failed without a complete response.
// It does not imply that a submitted write was not executed by the runtime.
// The wrapped error retains its existing (redacted) operator-facing message.
type TransportError struct {
	Err error
}

func (e *TransportError) Error() string { return e.Err.Error() }
func (e *TransportError) Unwrap() error { return e.Err }

// WriteLockRefusedError reports that a mutation gave up waiting for the
// client write lock because the caller's context was done (issue #347).
//
// Nothing was sent to the runtime, so this is not an indeterminate outcome:
// the caller knows the write did not happen. The wrapped cause keeps the
// context error, so the caller can still tell a cancellation from a
// deadline.
type WriteLockRefusedError struct {
	Cause error
}

func (e *WriteLockRefusedError) Error() string {
	return "waiting for write lock: " + e.Cause.Error() + "; no action was started"
}

func (e *WriteLockRefusedError) Unwrap() error { return e.Cause }
