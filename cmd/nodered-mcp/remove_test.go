package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fgjcarlos/nodered-mcp/internal/lifecycle"
)

// TestRemove_RequiresYes: runRemove without --yes must return an
// error and leave the file on disk. The confirm surface is the
// first safety net.
func TestRemove_RequiresYes(t *testing.T) {
	withReceiptsRoot(t)
	cfg := withConfigDir(t)
	plan, _ := buildSetupPlan(cfg)
	if _, err := runPlanAndSave(t, plan); err != nil {
		t.Fatalf("seed: %v", err)
	}
	stub := filepath.Join(cfg, ".env.example")

	if err := runRemove(nil); err == nil {
		t.Fatal("runRemove without --yes returned nil, want an error")
	}
	if _, err := os.Stat(stub); err != nil {
		t.Errorf(".env.example unlinked despite missing --yes: %v", err)
	}
}

// TestRemove_DryRun_DoesNotUnlink: --dry-run prints the action and
// leaves the disk untouched.
func TestRemove_DryRun_DoesNotUnlink(t *testing.T) {
	withReceiptsRoot(t)
	cfg := withConfigDir(t)
	plan, _ := buildSetupPlan(cfg)
	if _, err := runPlanAndSave(t, plan); err != nil {
		t.Fatalf("seed: %v", err)
	}
	stub := filepath.Join(cfg, ".env.example")
	before, _ := os.Stat(stub)

	if err := runRemove([]string{"--dry-run", "--yes"}); err != nil {
		t.Fatalf("runRemove dry-run: %v", err)
	}
	after, _ := os.Stat(stub)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Errorf(".env.example mtime changed under --dry-run; unlink happened")
	}
}

// TestRemove_PreservesForeign: the headline guarantee. A foreign
// file dropped into the config dir must survive `remove`.
func TestRemove_PreservesForeign(t *testing.T) {
	withReceiptsRoot(t)
	cfg := withConfigDir(t)
	plan, _ := buildSetupPlan(cfg)
	if _, err := runPlanAndSave(t, plan); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Drop foreign content into the config dir BEFORE remove.
	foreign := filepath.Join(cfg, "user.cfg")
	if err := os.WriteFile(foreign, []byte("precious"), 0o600); err != nil {
		t.Fatalf("seed foreign: %v", err)
	}
	foreignDir := filepath.Join(cfg, "user_dir")
	if err := os.MkdirAll(foreignDir, 0o700); err != nil {
		t.Fatalf("seed foreign dir: %v", err)
	}

	if err := runRemove([]string{"--yes"}); err != nil {
		t.Fatalf("runRemove: %v", err)
	}

	// Owned file is gone.
	stub := filepath.Join(cfg, ".env.example")
	if _, err := os.Stat(stub); err == nil {
		t.Errorf(".env.example still present after remove")
	}
	// Foreign file: byte-for-byte preserved.
	got, err := os.ReadFile(foreign)
	if err != nil {
		t.Errorf("foreign file unlinked: %v", err)
	}
	if string(got) != "precious" {
		t.Errorf("foreign content = %q, want byte-for-byte preserved", got)
	}
	// Foreign dir: still there.
	if _, err := os.Stat(foreignDir); err != nil {
		t.Errorf("foreign dir unlinked: %v", err)
	}
	// Config dir: still there (because foreign content lives inside).
	if _, err := os.Stat(cfg); err != nil {
		t.Errorf("config dir unlinked despite foreign content: %v", err)
	}
}

// TestRemove_PrunesEmptyConfigDir: with no foreign content, remove
// should also unlink the config dir itself.
func TestRemove_PrunesEmptyConfigDir(t *testing.T) {
	withReceiptsRoot(t)
	cfg := withConfigDir(t)
	plan, _ := buildSetupPlan(cfg)
	if _, err := runPlanAndSave(t, plan); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := runRemove([]string{"--yes"}); err != nil {
		t.Fatalf("runRemove: %v", err)
	}

	if _, err := os.Stat(cfg); err == nil {
		t.Errorf("config dir still present after empty remove: %s", cfg)
	}
}

// TestRemove_NoReceipt: a missing receipt is a no-op, not an error.
// The user gets a hint, the command exits 0.
func TestRemove_NoReceipt(t *testing.T) {
	withReceiptsRoot(t)
	withConfigDir(t)
	if err := runRemove([]string{"--yes"}); err != nil {
		t.Errorf("runRemove with no receipt: %v, want nil", err)
	}
}

// TestRemove_Idempotent: running remove twice in a row — first
// actually unlinks, second has no receipt — both succeed.
func TestRemove_Idempotent(t *testing.T) {
	withReceiptsRoot(t)
	cfg := withConfigDir(t)
	plan, _ := buildSetupPlan(cfg)
	if _, err := runPlanAndSave(t, plan); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := runRemove([]string{"--yes"}); err != nil {
		t.Fatalf("first runRemove: %v", err)
	}
	if err := runRemove([]string{"--yes"}); err != nil {
		t.Errorf("second runRemove: %v, want nil (no-op)", err)
	}
}

// TestRollback_NoManifest: a receipt with no manifest (last run
// failed or predates #230) makes rollback a no-op with an
// actionable hint, not an error.
func TestRollback_NoManifest(t *testing.T) {
	withReceiptsRoot(t)
	cfg := withConfigDir(t)
	stub := filepath.Join(cfg, ".env.example")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(stub, []byte("data"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	// Hand-craft a receipt with empty manifest.
	plan, _ := buildSetupPlan(cfg)
	rec, _ := runPlanAndSave(t, plan)
	rec.Receipt.Manifest = nil
	st := newSeededStore(t)
	_ = st.Save(rec)

	if err := runRollback([]string{"--yes"}); err != nil {
		t.Errorf("runRollback on empty manifest: %v, want nil", err)
	}
	if _, err := os.Stat(stub); err != nil {
		t.Errorf("file unlinked despite empty-manifest rollback (no-op): %v", err)
	}
}

// TestRollback_HappyPath: a verified receipt with a manifest is
// unlinked, just like `remove`. The path exercises the main
// runRollback code path (was 34% before this test).
func TestRollback_HappyPath(t *testing.T) {
	withReceiptsRoot(t)
	cfg := withConfigDir(t)
	plan, _ := buildSetupPlan(cfg)
	if _, err := runPlanAndSave(t, plan); err != nil {
		t.Fatalf("seed: %v", err)
	}
	stub := filepath.Join(cfg, ".env.example")

	if err := runRollback([]string{"--yes"}); err != nil {
		t.Fatalf("runRollback: %v", err)
	}
	if _, err := os.Stat(stub); err == nil {
		t.Errorf(".env.example still present after rollback")
	}
}

// TestRollback_NoReceipt: missing receipt is a no-op (mirrors the
// remove behavior).
func TestRollback_NoReceipt(t *testing.T) {
	withReceiptsRoot(t)
	withConfigDir(t)
	if err := runRollback([]string{"--yes"}); err != nil {
		t.Errorf("runRollback with no receipt: %v, want nil", err)
	}
}

// TestPruneConfigDirIfEmpty covers the small helper directly so we
// can assert on every branch without spinning up a full receipt.
func TestPruneConfigDirIfEmpty(t *testing.T) {
	t.Run("gone", func(t *testing.T) {
		// No error, no panic, no output line — just a silent
		// return on ENOENT.
		pruneConfigDirIfEmpty("/tmp/nope-should-not-exist-xyz", false)
	})
	t.Run("empty", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("seed: %v", err)
		}
		pruneConfigDirIfEmpty(dir, false)
		if _, err := os.Stat(dir); err == nil {
			t.Errorf("empty dir not pruned")
		}
	})
	t.Run("foreign", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "x"), []byte("a"), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}
		pruneConfigDirIfEmpty(dir, false)
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("dir with foreign content was pruned: %v", err)
		}
	})
}

// newSeededStore returns a Store rooted at the test's temp dir. The
// receipts root is the same as the one withReceiptsRoot installed
// via env var, so any Save writes to the same place the production
// code reads from.
func newSeededStore(t *testing.T) *lifecycle.Store {
	t.Helper()
	if os.Getenv("NODERED_MCP_RECEIPTS_DIR") == "" {
		t.Fatal("withReceiptsRoot not called")
	}
	s, err := lifecycle.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}
