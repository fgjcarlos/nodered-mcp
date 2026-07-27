package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRecoverPanics_TurnsPanicInto500 confirms the recover wrapper makes
// handler panics visible: the wrapper returns 500 and never crashes the
// server. Without it, a panic in mcp-go's streamable handler would close
// the connection silently (issue #24 symptom).
func TestRecoverPanics_TurnsPanicInto500(t *testing.T) {
	handler := recoverPanics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Code; got != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", got)
	}
}

// TestLogRequests_RecordsStatus confirms logRequests does not interfere
// with the handler's response: the status code it sees is the one the
// handler writes.
func TestLogRequests_RecordsStatus(t *testing.T) {
	handler := logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Code; got != http.StatusTeapot {
		t.Errorf("status: got %d, want 418", got)
	}
}

// TestRecoverPanics_PreservesResponseWritten: if the handler had already
// flushed a partial response before panicking, the wrapper does not try
// to write a second one (which would corrupt the wire).
func TestRecoverPanics_PreservesResponseWritten(t *testing.T) {
	handler := recoverPanics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Already-Flushed", "yes")
		w.WriteHeader(http.StatusAccepted)
		// Panic after headers are on the wire.
		panic("boom after headers")
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// The wrapper must not have overwritten the 202 status the handler set.
	if got := rr.Code; got != http.StatusAccepted {
		t.Errorf("status: got %d, want 202 (handler's status must survive the panic)", got)
	}
}
