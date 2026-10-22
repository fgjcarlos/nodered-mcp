package mcp

import (
	"context"
	"sync"
)

// helperMutex keeps the existing Lock/Unlock contract for internal callers,
// while allowing request handlers to abandon a queued acquisition on timeout.
// Its zero value is ready to use; it must not be copied after first use.
type helperMutex struct {
	once  sync.Once
	token chan struct{}
}

func (m *helperMutex) init() {
	m.once.Do(func() { m.token = make(chan struct{}, 1); m.token <- struct{}{} })
}

func (m *helperMutex) LockContext(ctx context.Context) error {
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

func (m *helperMutex) Lock()   { _ = m.LockContext(context.Background()) }
func (m *helperMutex) Unlock() { m.init(); m.token <- struct{}{} }
