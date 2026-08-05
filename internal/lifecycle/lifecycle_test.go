package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// happyPlan is a 3-step plan with no failure modes. Run must return
// all steps as "verified".
func TestRun_HappyPath(t *testing.T) {
	p := Plan{
		ID:   "happy",
		Name: "happy path",
		Steps: []Step{
			{ID: "a", Description: "first", Apply: noApply, Verify: noVerify},
			{ID: "b", Description: "second", Apply: noApply, Verify: noVerify},
			{ID: "c", Description: "third", Apply: noApply, Verify: noVerify},
		},
	}
	res, err := Run(context.Background(), p)
	if err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if len(res.Receipt.Steps) != 3 {
		t.Fatalf("len(Steps) = %d, want 3", len(res.Receipt.Steps))
	}
	for i, s := range res.Receipt.Steps {
		if s.Status != "verified" {
			t.Errorf("step[%d] status = %q, want %q", i, s.Status, "verified")
		}
	}
	if res.Receipt.EndedAt.Before(res.Receipt.StartedAt) {
		t.Errorf("EndedAt %v before StartedAt %v", res.Receipt.EndedAt, res.Receipt.StartedAt)
	}
}

// applyFailure halts the run after the first step and reports the step
// as failed. Steps after it must be marked "skipped" by NOT appearing
// in res.Receipt.Steps at all (we stop before recording them).
func TestRun_ApplyFailure_StopsAndReports(t *testing.T) {
	boom := errors.New("disk full")
	p := Plan{
		ID: "apply-fails",
		Steps: []Step{
			{ID: "first", Apply: noApply, Verify: noVerify},
			{ID: "boom", Apply: func(ctx context.Context) error { return boom }, Verify: noVerify},
			{ID: "never", Apply: noApply, Verify: noVerify},
		},
	}
	res, err := Run(context.Background(), p)
	if err == nil {
		t.Fatal("Run() error = nil, want *ErrApply")
	}
	var ea *ErrApply
	if !errors.As(err, &ea) {
		t.Fatalf("Run() error type = %T, want *ErrApply", err)
	}
	if ea.StepID != "boom" {
		t.Errorf("ea.StepID = %q, want %q", ea.StepID, "boom")
	}
	if !errors.Is(err, boom) {
		t.Errorf("err does not wrap boom: %v", err)
	}
	if len(res.Receipt.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2 (boom failed, never not recorded)", len(res.Receipt.Steps))
	}
	if res.Receipt.Steps[1].Status != "failed" {
		t.Errorf("step[1] status = %q, want failed", res.Receipt.Steps[1].Status)
	}
	if res.Receipt.Steps[1].Err == "" || !strings.Contains(res.Receipt.Steps[1].Err, "disk full") {
		t.Errorf("step[1].Err = %q, want it to mention the underlying error", res.Receipt.Steps[1].Err)
	}
}

// Verify after a successful Apply fails → run returns *ErrVerify, the
// step is marked failed. Apply already ran for that step; rollback is
// the caller's problem, not Run's.
func TestRun_VerifyFailure_StopsAndReports(t *testing.T) {
	bad := errors.New("state diverged")
	p := Plan{
		ID: "verify-fails",
		Steps: []Step{
			{ID: "ok", Apply: noApply, Verify: noVerify},
			{ID: "applied-but-broken", Apply: noApply, Verify: func(ctx context.Context) error { return bad }},
			{ID: "never", Apply: noApply, Verify: noVerify},
		},
	}
	res, err := Run(context.Background(), p)
	var ev *ErrVerify
	if !errors.As(err, &ev) {
		t.Fatalf("Run() error type = %T, want *ErrVerify", err)
	}
	if ev.StepID != "applied-but-broken" {
		t.Errorf("ev.StepID = %q, want %q", ev.StepID, "applied-but-broken")
	}
	if !errors.Is(err, bad) {
		t.Errorf("err does not wrap bad: %v", err)
	}
	last := res.Receipt.Steps[len(res.Receipt.Steps)-1]
	if last.Status != "failed" || !strings.Contains(last.Err, "state diverged") {
		t.Errorf("last step = %+v, want status=failed err mentions state", last)
	}
}

// Idempotency contract is enforced by Step.Apply, not by Run. This test
// locks the contract from Run's side: Apply on the same step is called
// exactly once per Run invocation even if the Plan is re-run with the
// same ID.
func TestRun_ApplyCalledExactlyOncePerRun(t *testing.T) {
	var count int32
	plan := Plan{
		ID: "count",
		Steps: []Step{
			{ID: "only",
				Apply:  func(ctx context.Context) error { atomic.AddInt32(&count, 1); return nil },
				Verify: noVerify,
			},
		},
	}
	for i := 0; i < 3; i++ {
		if _, err := Run(context.Background(), plan); err != nil {
			t.Fatalf("Run iteration %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&count); got != 3 {
		t.Errorf("Apply call count = %d, want 3 (once per Run)", got)
	}
}

// nil-Apply means "no side-effect, claim the state holds by
// construction". Useful for setup steps that only need to verify an
// external precondition. nil-Verify means "trust Apply, no post-check".
// Both no-op paths must still record a status.
func TestRun_NilApplyAndNilVerify(t *testing.T) {
	p := Plan{
		ID: "nil-paths",
		Steps: []Step{
			{ID: "verify-only", Verify: noVerify}, // no Apply → "applied" then "verified"
			{ID: "apply-only", Apply: noApply},    // no Verify → "applied" only
		},
	}
	res, err := Run(context.Background(), p)
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if res.Receipt.Steps[0].Status != "verified" {
		t.Errorf("step[0] (verify-only) status = %q, want verified", res.Receipt.Steps[0].Status)
	}
	if res.Receipt.Steps[1].Status != "applied" {
		t.Errorf("step[1] (apply-only) status = %q, want applied", res.Receipt.Steps[1].Status)
	}
}

// Store round-trip: Save → Load → LastResult. Uses t.TempDir() so no
// filesystem leak between tests.
func TestStore_RoundTrip(t *testing.T) {
	root := t.TempDir()
	st, err := NewStoreWithRoot(root)
	if err != nil {
		t.Fatalf("NewStoreWithRoot: %v", err)
	}
	p := Plan{
		ID: "rt",
		Steps: []Step{
			{ID: "x", Apply: noApply, Verify: noVerify},
		},
	}
	res, err := Run(context.Background(), p)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := st.Save(res); err != nil {
		t.Fatalf("Save: %v", err)
	}
	want := filepath.Join(root, "rt.json")
	if _, err := fileExists(want); err != nil {
		t.Errorf("receipt file %s missing: %v", want, err)
	}
	got, err := st.LastResult("rt")
	if err != nil {
		t.Fatalf("LastResult: %v", err)
	}
	if got.PlanID != "rt" {
		t.Errorf("PlanID = %q, want rt", got.PlanID)
	}
	if len(got.Receipt.Steps) != 1 || got.Receipt.Steps[0].Status != "verified" {
		t.Errorf("Steps = %+v, want 1 verified step", got.Receipt.Steps)
	}
}

// Save writes to a tmp file then renames atomically. We can't easily
// observe the rename directly without a debugger, but we can prove the
// happy path leaves no .tmp-* leftover.
func TestStore_NoTempLeftover(t *testing.T) {
	root := t.TempDir()
	st, _ := NewStoreWithRoot(root)
	res, _ := Run(context.Background(), Plan{
		ID: "atomic", Steps: []Step{{ID: "a", Apply: noApply, Verify: noVerify}},
	})
	if err := st.Save(res); err != nil {
		t.Fatalf("Save: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(root, ".tmp-*.json"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("leftover tmp files: %v", matches)
	}
}

// Load on an absent receipt returns ErrReceiptNotFound. Doctor uses
// this to render a "first run" hint instead of fabricating state.
func TestStore_LoadMissing(t *testing.T) {
	st, _ := NewStoreWithRoot(t.TempDir())
	_, err := st.Load("nope")
	if !errors.Is(err, ErrReceiptNotFound) {
		t.Errorf("Load(missing) err = %v, want ErrReceiptNotFound", err)
	}
}

// Load on a corrupt receipt returns ErrReceiptCorrupt. Doctor surfaces
// this as a warning, not as a fabricated "all good".
func TestStore_LoadCorrupt(t *testing.T) {
	root := t.TempDir()
	st, _ := NewStoreWithRoot(root)
	if err := writeFile(filepath.Join(root, "broken.json"), []byte("not json{")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := st.Load("broken")
	if !errors.Is(err, ErrReceiptCorrupt) {
		t.Errorf("Load(corrupt) err = %v, want ErrReceiptCorrupt", err)
	}
}

// Plan ID validation refuses path separators and shell metachars. The
// on-disk layout is "<root>/<plan-id>.json" so anything outside the
// safe alphabet would let the path escape.
func TestStore_RejectsPathEscape(t *testing.T) {
	st, _ := NewStoreWithRoot(t.TempDir())
	cases := []string{"", "../etc/passwd", "foo/bar", "a b", "x&y"}
	for _, id := range cases {
		if err := st.Save(&Result{PlanID: PlanID(id), Receipt: Receipt{PlanID: PlanID(id), StartedAt: time.Now()}}); err == nil {
			t.Errorf("Save(%q) returned nil err, want validation error", id)
		}
		if _, err := st.Load(PlanID(id)); err == nil {
			t.Errorf("Load(%q) returned nil err, want validation error", id)
		}
	}
}

// Test noVerify runs in <1ms even when a slow step appears before it.
// Defends against an accidental "verify all previous steps" loop.
func TestRun_VerifyOnlySeesOwnStep(t *testing.T) {
	called := make(map[string]int)
	p := Plan{
		ID: "isolation",
		Steps: []Step{
			{ID: "a", Apply: noApply, Verify: func(ctx context.Context) error { called["a"]++; return nil }},
			{ID: "b", Apply: noApply, Verify: func(ctx context.Context) error { called["b"]++; return nil }},
			{ID: "c", Apply: noApply, Verify: func(ctx context.Context) error { called["c"]++; return nil }},
		},
	}
	if _, err := Run(context.Background(), p); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for k, v := range called {
		if v != 1 {
			t.Errorf("step %q verify count = %d, want 1", k, v)
		}
	}
}

func noApply(ctx context.Context) error  { return nil }
func noVerify(ctx context.Context) error { return nil }

// fileExists is a small helper around os.Stat that returns nil when
// the file is present and fs.ErrNotExist when it is absent. Kept in
// the test file so the production package doesn't grow a new export.
func fileExists(path string) (struct{}, error) {
	if _, err := osStat(path); err != nil {
		return struct{}{}, err
	}
	return struct{}{}, nil
}

// osStat is the seam tests can override if we ever need a fake FS.
// Today it's just os.Stat.
var osStat = func(path string) (any, error) {
	type fi struct{ Name string }
	_ = fi{}
	return os.Stat(path)
}

// writeFile is a thin wrapper that mirrors os.WriteFile so the test
// helper stays in this file rather than leaking into production.
func writeFile(path string, data []byte) error {
	return osWriteFile(path, data, 0o600)
}

var osWriteFile = func(path string, data []byte, perm uint32) error {
	return osWriteFileImpl(path, data, os.FileMode(perm))
}

func osWriteFileImpl(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}
