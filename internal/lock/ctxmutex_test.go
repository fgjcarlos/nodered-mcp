package lock

import (
	"context"
	"testing"
	"time"
)

// TestCtxMutexExcludes covers the plain contract: the second caller waits
// until the holder releases.
func TestCtxMutexExcludes(t *testing.T) {
	var m CtxMutex
	m.Lock()

	got := make(chan struct{})
	go func() {
		m.Lock()
		close(got)
		m.Unlock()
	}()

	select {
	case <-got:
		t.Fatal("the second caller entered while the mutex was held")
	case <-time.After(50 * time.Millisecond):
	}

	m.Unlock()
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("the second caller never entered after the release")
	}
}

// TestCtxMutexLockContextGivesUp covers the cancelable wait: a caller whose
// context is done gives up instead of waiting, and the refusal leaves the
// mutex exactly as it was.
func TestCtxMutexLockContextGivesUp(t *testing.T) {
	var m CtxMutex
	m.Lock()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.LockContext(canceled); err == nil {
		t.Fatal("LockContext with a canceled context acquired the mutex")
	}

	expired, cancelExpired := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelExpired()
	if err := m.LockContext(expired); err == nil {
		t.Fatal("LockContext with an expired context acquired the mutex")
	}

	// The refused acquisitions must not have consumed the token: one
	// release still frees the mutex for the next caller.
	m.Unlock()
	acquired := make(chan struct{})
	go func() {
		if err := m.LockContext(context.Background()); err == nil {
			close(acquired)
		}
	}()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("the mutex was not free after the release")
	}
}
