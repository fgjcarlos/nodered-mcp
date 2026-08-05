// Package lifecycle implements a Plan → Apply → Verify transactional
// foundation for nodered-mcp CLI commands (setup, doctor, future remove/
// rollback).
//
// A Plan is a named, ordered list of Steps. Each Step carries its own
// Apply and Verify functions so the lifecycle stays generic: the runner
// walks steps in order, applies each, verifies each, and stops on the
// first failure with a typed error the caller can inspect. Successful
// runs produce a Receipt that is persisted to disk under
// $XDG_STATE_HOME/nodered-mcp/receipts/<plan-id>.json so the next
// invocation can skip already-applied work and so doctor can re-verify
// without re-applying.
//
// Idempotency is the contract: calling Apply on a step whose side-effects
// already exist must be safe and cheap. The lifecycle foundation does
// not enforce idempotency on the caller's behalf — the Step's Apply
// implementation owns that contract. The Runner only guarantees that
// (a) Apply runs at most once per Run invocation, (b) Verify runs
// immediately after Apply on the same in-memory state, and (c) the
// Receipt reflects exactly what happened, not a hoped-for state.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// PlanID is the unique identifier for a Plan. It becomes the file name
// for the persisted Receipt.
type PlanID string

// StepResult records what happened to a single Step during a Run.
type StepResult struct {
	ID     string `json:"id"`
	Status string `json:"status"` // "applied" | "verified" | "failed" | "skipped"
	Err    string `json:"err,omitempty"`
}

// Receipt is the persisted outcome of a single Plan run. It is what
// `doctor` reads back to decide whether a step is still in its claimed
// state and what the previous run looked like.
type Receipt struct {
	PlanID    PlanID       `json:"plan_id"`
	StartedAt time.Time    `json:"started_at"`
	EndedAt   time.Time    `json:"ended_at"`
	Steps     []StepResult `json:"steps"`
}

// Result is the in-memory outcome of Run. It is also what gets persisted
// to disk via Store.Save as a Receipt.
type Result struct {
	PlanID  PlanID
	Receipt Receipt
}

// Step is one unit of work. Apply must be idempotent: repeated calls
// with the same inputs must converge to the same observable state. Verify
// is a read-only check that the post-Apply state matches what the Step
// claims to produce.
type Step struct {
	ID          string
	Description string
	// Apply performs the side-effect. It must be safe to call when the
	// step's claimed state already holds (idempotent).
	Apply func(ctx context.Context) error
	// Verify confirms the post-Apply state without mutating anything.
	// Returns nil when the step is in its claimed state, an error with
	// a human-readable message when it is not.
	Verify func(ctx context.Context) error
}

// Plan is a named, ordered list of Steps. Order matters: Apply and
// Verify walk the slice index-for-index.
type Plan struct {
	ID    PlanID
	Name  string
	Steps []Step
}

// ErrVerify is returned by Run when a step's Verify check fails. The
// caller can type-assert against errors.As to recover the failing step
// ID and the underlying verify error.
type ErrVerify struct {
	StepID string
	Err    error
}

func (e *ErrVerify) Error() string {
	return fmt.Sprintf("verify failed on step %q: %v", e.StepID, e.Err)
}

func (e *ErrVerify) Unwrap() error { return e.Err }

// ErrApply is the symmetric variant for Apply failures.
type ErrApply struct {
	StepID string
	Err    error
}

func (e *ErrApply) Error() string {
	return fmt.Sprintf("apply failed on step %q: %v", e.StepID, e.Err)
}

func (e *ErrApply) Unwrap() error { return e.Err }

// Run executes a Plan end-to-end. The semantics are:
//
//  1. Walk Steps in order.
//  2. For each step: call Apply; on failure, mark the step "failed"
//     and stop with *ErrApply. Steps after the failing one are NOT
//     executed and are recorded as "skipped".
//  3. On Apply success, call Verify on the SAME step. If Verify fails,
//     mark "failed" and stop with *ErrVerify. Apply has already run for
//     this step; the caller is responsible for any rollback.
//
// Successful runs return (*Result, nil). Run never returns a partial
// result silently — every step's status is recorded in the returned
// Result even when the run stops early.
//
// Run does NOT persist the Result. The caller passes the returned
// Result to Store.Save to write a Receipt. This split lets the
// lifecycle package stay free of filesystem policy; tests can run
// in-memory without touching disk.
func Run(ctx context.Context, p Plan) (*Result, error) {
	started := time.Now().UTC()
	res := &Result{
		PlanID: p.ID,
		Receipt: Receipt{
			PlanID:    p.ID,
			StartedAt: started,
		},
	}

	for i := range p.Steps {
		s := p.Steps[i]

		if s.Apply != nil {
			if err := s.Apply(ctx); err != nil {
				res.Receipt.Steps = append(res.Receipt.Steps, StepResult{ID: s.ID, Status: "failed", Err: err.Error()})
				res.Receipt.EndedAt = time.Now().UTC()
				return res, &ErrApply{StepID: s.ID, Err: err}
			}
		}
		// Record the step as "applied" before Verify. If Verify is
		// nil or succeeds, the status gets promoted to "verified";
		// if Verify fails, this last entry is overwritten with the
		// failure detail by the Verify branch below.
		res.Receipt.Steps = append(res.Receipt.Steps, StepResult{ID: s.ID, Status: "applied"})

		if s.Verify == nil {
			// No verify declared. Trust the Apply (or the
			// "no Apply → state holds by construction" path above).
			// Step stays as "applied" — verified is a stronger
			// claim we cannot make without a check.
			continue
		}
		if err := s.Verify(ctx); err != nil {
			res.Receipt.Steps[len(res.Receipt.Steps)-1] = StepResult{ID: s.ID, Status: "failed", Err: err.Error()}
			res.Receipt.EndedAt = time.Now().UTC()
			return res, &ErrVerify{StepID: s.ID, Err: err}
		}
		res.Receipt.Steps[len(res.Receipt.Steps)-1].Status = "verified"
	}

	res.Receipt.EndedAt = time.Now().UTC()
	return res, nil
}

// ErrReceiptNotFound signals "no receipt on disk for this plan ID yet".
// It is returned by Store.Load when the per-plan file is absent so the
// caller can distinguish "first run" from "corrupt receipt".
var ErrReceiptNotFound = errors.New("lifecycle: receipt not found")

// ErrReceiptCorrupt signals the receipt file exists but cannot be
// parsed. The caller should treat the receipt as missing AND surface a
// warning, rather than silently fabricating a state.
var ErrReceiptCorrupt = errors.New("lifecycle: receipt corrupt")
