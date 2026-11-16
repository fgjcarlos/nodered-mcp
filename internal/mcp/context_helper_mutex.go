package mcp

import "github.com/fgjcarlos/nodered-mcp/internal/lock"

// helperMutex keeps the existing Lock/Unlock contract for internal callers,
// while allowing request handlers to abandon a queued acquisition on timeout.
//
// It is the shared context-aware mutex (internal/lock): the client write
// lock guards the mutations with the same primitive, so both waits honor the
// caller's deadline (issue #347).
type helperMutex = lock.CtxMutex
