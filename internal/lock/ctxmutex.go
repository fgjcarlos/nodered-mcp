// Package lock provides the context-aware mutex that guards the write
// paths.
//
// A plain sync.Mutex cannot be abandoned: a caller whose context is
// canceled or whose deadline passes stays parked on Lock() until the
// current holder finishes, so a write queue outlives the timeout it was
// promised (issue #347). CtxMutex hands the waiter's context to the
// acquisition, so the waiter gives up instead.
package lock

import (
	"context"
	"sync"
)

// CtxMutex is a mutex whose acquisition can be abandoned. Its zero value is
// ready to use; it must not be copied after first use.
type CtxMutex struct {
	once  sync.Once
	token chan struct{}
}

func (m *CtxMutex) init() {
	m.once.Do(func() { m.token = make(chan struct{}, 1); m.token <- struct{}{} })
}

// LockContext acquires the mutex. It returns ctx.Err() without holding the
// mutex when ctx is already done, or becomes done while waiting.
func (m *CtxMutex) LockContext(ctx context.Context) error {
	m.init()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.token:
		if err := ctx.Err(); err != nil {
			m.Unlock()
			return err
		}
		return nil
	}
}

// Lock acquires the mutex, ignoring cancellation. It is the contract for
// callers that have no context to give up on.
func (m *CtxMutex) Lock() { _ = m.LockContext(context.Background()) }

// Unlock releases the mutex. Unlocking a mutex that is not held is a
// programming error and panics, the way sync.Mutex does: a silent no-op
// would let two writers hold the lock at once.
func (m *CtxMutex) Unlock() {
	m.init()
	select {
	case m.token <- struct{}{}:
	default:
		panic("lock: unlock of unlocked CtxMutex")
	}
}
