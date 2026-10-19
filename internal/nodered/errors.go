package nodered

// TransportError marks an HTTP exchange that failed without a complete response.
// It does not imply that a submitted write was not executed by the runtime.
// The wrapped error retains its existing (redacted) operator-facing message.
type TransportError struct {
	Err error
}

func (e *TransportError) Error() string { return e.Err.Error() }
func (e *TransportError) Unwrap() error { return e.Err }
