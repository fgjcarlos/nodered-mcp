package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestManifest_PopulatedOnVerifiedSteps: a step with Owns that
// verifies successfully contributes its paths to the Receipt.Manifest.
func TestManifest_PopulatedOnVerifiedSteps(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "managed.cfg")
	plan := Plan{
		ID: "owns",
		Steps: []Step{
			{
				ID:   "w",
				Owns: []OwnedPath{{Path: p, Kind: KindFile, Owner: "test"}},
				Apply: func(ctx context.Context) error {
					return os.WriteFile(p, []byte("managed"), 0o600)
				},
				Verify: func(ctx context.Context) error {
					got, err := os.ReadFile(p)
					if err != nil {
						return err
					}
					if string(got) != "managed" {
						return errors.New("content drift")
					}
					return nil
				},
			},
		},
	}
	res, err := Run(context.Background(), plan)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Receipt.Manifest) != 1 {
		t.Fatalf("Manifest len = %d, want 1", len(res.Receipt.Manifest))
	}
	if res.Receipt.Manifest[0].Path != p {
		t.Errorf("Manifest[0].Path = %q, want %q", res.Receipt.Manifest[0].Path, p)
	}
	if res.Receipt.Manifest[0].Kind != KindFile {
		t.Errorf("Manifest[0].Kind = %q, want %q", res.Receipt.Manifest[0].Kind, KindFile)
	}
}

// TestManifest_NotPopulatedOnFailedStep: a step that fails Apply or
// Verify must NOT add its paths to the manifest — otherwise remove
// would unlink a path the runner never successfully created.
func TestManifest_NotPopulatedOnFailedStep(t *testing.T) {
	plan := Plan{
		ID: "fail",
		Steps: []Step{
			{
				ID:   "boom",
				Owns: []OwnedPath{{Path: "/tmp/nope", Kind: KindFile, Owner: "test"}},
				Apply: func(ctx context.Context) error {
					return errors.New("apply failed")
				},
			},
		},
	}
	res, err := Run(context.Background(), plan)
	if err == nil {
		t.Fatal("Run err = nil, want *ErrApply")
	}
	if len(res.Receipt.Manifest) != 0 {
		t.Errorf("Manifest on failed run = %v, want empty", res.Receipt.Manifest)
	}
}

// TestManifest_NotPopulatedOnVerifyFailure: the contract also holds
// when Apply succeeded but Verify failed. Apply has run, but the
// state may have drifted; the manifest stays empty so remove
// doesn't trust the post-state.
func TestManifest_NotPopulatedOnVerifyFailure(t *testing.T) {
	plan := Plan{
		ID: "v-fail",
		Steps: []Step{
			{
				ID:     "drift",
				Owns:   []OwnedPath{{Path: "/tmp/nope", Kind: KindFile, Owner: "test"}},
				Apply:  func(ctx context.Context) error { return nil },
				Verify: func(ctx context.Context) error { return errors.New("drift") },
			},
		},
	}
	res, err := Run(context.Background(), plan)
	if err == nil {
		t.Fatal("Run err = nil, want *ErrVerify")
	}
	if len(res.Receipt.Manifest) != 0 {
		t.Errorf("Manifest on verify-fail = %v, want empty", res.Receipt.Manifest)
	}
}

// TestManifest_MultipleStepsPreserveOrder: the manifest preserves
// the order in which steps verified, so a Plan author can list
// child paths before parents and remove will unlink children first.
func TestManifest_MultipleStepsPreserveOrder(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	c := filepath.Join(dir, "c")
	plan := Plan{
		ID: "order",
		Steps: []Step{
			{ID: "first", Owns: []OwnedPath{{Path: a, Kind: KindFile, Owner: "x"}},
				Apply:  func(ctx context.Context) error { return os.WriteFile(a, nil, 0o600) },
				Verify: func(ctx context.Context) error { _, err := os.Stat(a); return err }},
			{ID: "second", Owns: []OwnedPath{{Path: b, Kind: KindFile, Owner: "x"}},
				Apply:  func(ctx context.Context) error { return os.WriteFile(b, nil, 0o600) },
				Verify: func(ctx context.Context) error { _, err := os.Stat(b); return err }},
			{ID: "third", Owns: []OwnedPath{{Path: c, Kind: KindFile, Owner: "x"}},
				Apply:  func(ctx context.Context) error { return os.WriteFile(c, nil, 0o600) },
				Verify: func(ctx context.Context) error { _, err := os.Stat(c); return err }},
		},
	}
	res, err := Run(context.Background(), plan)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := []string{}
	for _, m := range res.Receipt.Manifest {
		got = append(got, m.Path)
	}
	want := []string{a, b, c}
	sort.Strings(want) // ignore order for this assertion
	sort.Strings(got)
	for i, w := range want {
		if got[i] != w {
			t.Errorf("Manifest[%d] = %q, want %q (full got %v want %v)", i, got[i], w, got, want)
		}
	}
}

// TestValidateOwnedPath rejects the malformed inputs the manifest
// is meant to catch: relative paths, unknown kinds, empty owner,
// traversal attempts. "Abs" cases use platform-correct absolute
// paths (filepath.IsAbs is platform-specific; on Windows "/abs/file"
// is NOT absolute but "C:\\abs\\file" is).
func TestValidateOwnedPath(t *testing.T) {
	absFile := filepath.Join(string(filepath.Separator)+"abs", "file")
	absDir := filepath.Join(string(filepath.Separator)+"abs", "dir")
	cases := []struct {
		name string
		p    OwnedPath
		ok   bool
	}{
		{"abs file", OwnedPath{absFile, KindFile, "owner"}, true},
		{"abs dir", OwnedPath{absDir, KindDir, "owner"}, true},
		{"relative", OwnedPath{"rel/file", KindFile, "owner"}, false},
		{"empty path", OwnedPath{"", KindFile, "owner"}, false},
		{"empty owner", OwnedPath{absFile, KindFile, ""}, false},
		{"unknown kind", OwnedPath{absFile, "symlink", "owner"}, false},
		{"traversal", OwnedPath{absFile + string(filepath.Separator) + ".." + string(filepath.Separator) + "etc", KindFile, "owner"}, false},
		{"bad owner", OwnedPath{absFile, KindFile, "ow ner"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOwnedPath(tc.p)
			if tc.ok && err != nil {
				t.Errorf("err = %v, want nil", err)
			}
			if !tc.ok && err == nil {
				t.Errorf("err = nil, want validation error")
			}
		})
	}
}

// TestRemoveOwnedPath_KindMismatch_Refuses: a manifest that
// declares a directory and a filesystem that holds a regular file
// at the same path is a stale receipt. Refuse rather than rm -rf
// blindly (or rm a directory the user owns).
func TestRemoveOwnedPath_KindMismatch_Refuses(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "thing")
	if err := os.WriteFile(f, []byte("data"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	manifest := Manifest{{Path: f, Kind: KindDir, Owner: "test"}}
	if err := removeOwnedPath(manifest[0], manifest); err == nil {
		t.Fatal("removeOwnedPath(dir) on a file returned nil, want kind-mismatch error")
	}
	if _, err := os.Stat(f); err != nil {
		t.Errorf("file was unlinked despite kind mismatch: %v", err)
	}
}

// TestRemoveOwnedPath_AlreadyGone_Idempotent: re-running remove
// after a successful first remove is a no-op, not an error.
func TestRemoveOwnedPath_AlreadyGone_Idempotent(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "ephemeral")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	manifest := Manifest{{Path: f, Kind: KindFile, Owner: "test"}}
	if err := removeOwnedPath(manifest[0], manifest); err != nil {
		t.Fatalf("first remove: %v", err)
	}
	// Second call: the file is gone. removeOwnedPath must return nil.
	if err := removeOwnedPath(manifest[0], manifest); err != nil {
		t.Errorf("second remove: %v, want nil (idempotent)", err)
	}
}

// TestRemoveOwnedPath_NotInManifest_ErrForeign: passing a path that
// is not in the manifest must return ErrForeignPath so callers can
// surface "I am not allowed to touch that" without nuking the
// filesystem.
func TestRemoveOwnedPath_NotInManifest_ErrForeign(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "user.txt")
	if err := os.WriteFile(f, []byte("precious"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	manifest := Manifest{} // empty
	p := OwnedPath{Path: f, Kind: KindFile, Owner: "test"}
	if err := removeOwnedPath(p, manifest); !errors.Is(err, ErrForeignPath) {
		t.Errorf("err = %v, want ErrForeignPath", err)
	}
	if _, err := os.Stat(f); err != nil {
		t.Errorf("user file was unlinked: %v", err)
	}
}

// TestRemoveManifest_DirEmpty: a KindDir that is empty is fair
// game — removeOwnedPath unlinks it. Foreign-preservation is
// exercised in TestForeignConfigPreserved.
func TestRemoveManifest_DirEmpty(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatalf("seed: %v", err)
	}
	manifest := Manifest{{Path: sub, Kind: KindDir, Owner: "test"}}
	if err := removeOwnedPath(manifest[0], manifest); err != nil {
		t.Fatalf("remove empty dir: %v", err)
	}
	if _, err := os.Stat(sub); err == nil {
		t.Errorf("empty subdir still present after remove")
	}
}

// TestRemoveManifest_DirWithForeign_Refuses: a KindDir that still
// has foreign entries must NOT be removed. The CLI surfaces
// ErrDirNotEmpty as a "kept" status, not an error. The test
// confirms the lifecycle package returns the typed error and that
// the foreign entries survive.
func TestRemoveManifest_DirWithForeign_Refuses(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(filepath.Join(sub, "deep"), 0o700); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "deep", "user.txt"), []byte("precious"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	manifest := Manifest{{Path: sub, Kind: KindDir, Owner: "test"}}
	err := removeOwnedPath(manifest[0], manifest)
	if !errors.Is(err, ErrDirNotEmpty) {
		t.Errorf("err = %v, want ErrDirNotEmpty", err)
	}
	if _, statErr := os.Stat(sub); statErr != nil {
		t.Errorf("dir with foreign content was unlinked: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(sub, "deep", "user.txt")); statErr != nil {
		t.Errorf("foreign file was unlinked: %v", statErr)
	}
}

// TestForeignConfigPreserved: a foreign file outside the manifest
// must survive remove untouched. This is the headline guarantee
// the issue calls out: "Foreign configuration remains byte-for-byte
// preserved by tested scenarios."
func TestForeignConfigPreserved(t *testing.T) {
	root := t.TempDir()

	// Owned path: the manifest claims it.
	owned := filepath.Join(root, "owned.cfg")
	if err := os.WriteFile(owned, []byte("owned"), 0o600); err != nil {
		t.Fatalf("seed owned: %v", err)
	}

	// Foreign paths: the user added these by hand. The manifest
	// does NOT claim them.
	foreign1 := filepath.Join(root, "user.cfg")
	foreign2 := filepath.Join(root, "user_dir")
	if err := os.WriteFile(foreign1, []byte("user data"), 0o600); err != nil {
		t.Fatalf("seed foreign1: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(foreign2, "sub"), 0o700); err != nil {
		t.Fatalf("seed foreign2: %v", err)
	}

	manifest := Manifest{{Path: owned, Kind: KindFile, Owner: "test"}}

	// Run remove over the manifest.
	for _, p := range manifest {
		if err := removeOwnedPath(p, manifest); err != nil {
			t.Fatalf("remove %s: %v", p.Path, err)
		}
	}

	// Owned is gone.
	if _, err := os.Stat(owned); err == nil {
		t.Errorf("owned file still present: %s", owned)
	}

	// Foreign 1: byte-for-byte preserved.
	got, err := os.ReadFile(foreign1)
	if err != nil {
		t.Fatalf("foreign1 read after remove: %v", err)
	}
	if string(got) != "user data" {
		t.Errorf("foreign1 content = %q, want byte-for-byte preserved %q", got, "user data")
	}

	// Foreign 2: tree preserved, including the sub-dir.
	if _, err := os.Stat(foreign2); err != nil {
		t.Errorf("foreign2 gone after remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(foreign2, "sub")); err != nil {
		t.Errorf("foreign2/sub gone after remove: %v", err)
	}
}

// TestManifestHas covers the small helper used by tests and the
// verify surface.
func TestManifestHas(t *testing.T) {
	m := Manifest{
		{Path: "/a", Kind: KindFile, Owner: "x"},
		{Path: "/b", Kind: KindDir, Owner: "x"},
	}
	if !m.Has("/a") {
		t.Error(`m.Has("/a") = false, want true`)
	}
	if m.Has("/c") {
		t.Error(`m.Has("/c") = true, want false`)
	}
}

// TestRemoveReceipt_Absent_IsNoOp: removing a receipt that does
// not exist must be idempotent. The CLI calls this at the end of
// remove/rollback; a transient race (e.g. user manually deletes
// the file) must not turn into a hard error.
func TestRemoveReceipt_Absent_IsNoOp(t *testing.T) {
	root := filepath.Join(t.TempDir(), "receipts")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("seed: %v", err)
	}
	st, err := NewStoreWithRoot(root)
	if err != nil {
		t.Fatalf("NewStoreWithRoot: %v", err)
	}
	if err := st.RemoveReceipt("absent"); err != nil {
		t.Errorf("RemoveReceipt(absent) err = %v, want nil", err)
	}
}

// TestRemoveReceipt_Present: a real receipt gets deleted; the
// load afterwards returns ErrReceiptNotFound.
func TestRemoveReceipt_Present(t *testing.T) {
	root := filepath.Join(t.TempDir(), "receipts")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("seed: %v", err)
	}
	st, err := NewStoreWithRoot(root)
	if err != nil {
		t.Fatalf("NewStoreWithRoot: %v", err)
	}
	// Write a receipt by hand.
	path := filepath.Join(root, "present.json")
	if err := os.WriteFile(path, []byte(`{"plan_id":"present"}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := st.RemoveReceipt("present"); err != nil {
		t.Fatalf("RemoveReceipt: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Errorf("receipt file still present after remove")
	}
}

// TestPackageRemoveReceipt covers the package-level helper used
// by the CLI. It walks NODERED_MCP_RECEIPTS_DIR via NewStore.
func TestPackageRemoveReceipt(t *testing.T) {
	t.Setenv("NODERED_MCP_RECEIPTS_DIR", filepath.Join(t.TempDir(), "r"))
	if err := RemoveReceipt("never-existed"); err != nil {
		t.Errorf("RemoveReceipt(never-existed) err = %v, want nil", err)
	}
}

// TestPackageRemoveOwned is the public surface the cmd uses.
func TestPackageRemoveOwned(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "x")
	if err := os.WriteFile(f, []byte("a"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m := Manifest{{Path: f, Kind: KindFile, Owner: "test"}}
	if err := RemoveOwnedPath(m[0], m); err != nil {
		t.Errorf("RemoveOwnedPath err = %v", err)
	}
}

// TestDefaultReceiptRoot_XDGDrives: with NODERED_MCP_RECEIPTS_DIR
// unset and XDG_STATE_HOME set, the canonical root is derived
// from XDG. Keeps the XDG-spec contract tested end-to-end.
func TestDefaultReceiptRoot_XDGDrives(t *testing.T) {
	t.Setenv("NODERED_MCP_RECEIPTS_DIR", "")
	t.Setenv("XDG_STATE_HOME", "/tmp/fake-xdg-state")
	got, err := defaultReceiptRoot()
	if err != nil {
		t.Fatalf("defaultReceiptRoot: %v", err)
	}
	want := filepath.Join("/tmp/fake-xdg-state", "nodered-mcp", "receipts")
	if got != want {
		t.Errorf("defaultReceiptRoot = %q, want %q", got, want)
	}
}

// TestNewStore_CreatesRoot: NewStore (not the test-only NewStoreWithRoot)
// must create the on-disk root. On Unix the perms are 0700; on
// Windows os.MkdirAll ignores the mode bits, so the assertion is
// perm == 0700 on Unix and "is a directory" elsewhere. Skipping
// the perm check on Windows avoids a flake that has nothing to
// do with the production contract.
func TestNewStore_CreatesRoot(t *testing.T) {
	t.Setenv("NODERED_MCP_RECEIPTS_DIR", filepath.Join(t.TempDir(), "r"))
	st, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	info, err := os.Stat(st.Root())
	if err != nil {
		t.Fatalf("Stat root: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("root is not a directory: %s", st.Root())
	}
	// Windows os.MkdirAll ignores the mode bits, so the
	// perms that come back from Stat are 0777. Skip the
	// 0700 check there. The /tmp dir is the safe source of
	// truth on Unix; on Windows the production contract is
	// "directory exists at the configured path", which we
	// already asserted.
	if info.Mode().Perm()&0o777 == 0o777 {
		t.Skipf("platform ignores MkdirAll mode (perms = %o); skipping 0700 check", info.Mode().Perm())
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("root perms = %o, want 0700", perm)
	}
}

// TestTypedError_ErrorStrings: lock the contract that *ErrVerify
// and *ErrApply render the step id and the wrapped error in
// their Error() method. Lives in the lifecycle package (not the
// cmd package) so the coverage profile credits the call site.
func TestTypedError_ErrorStrings(t *testing.T) {
	v := &ErrVerify{StepID: "vstep", Err: errors.New("inner")}
	if got := v.Error(); !strings.Contains(got, `verify failed on step "vstep"`) {
		t.Errorf(`ErrVerify.Error() = %q, want it to contain the step id`, got)
	}
	if got := v.Unwrap().Error(); got != "inner" {
		t.Errorf("Unwrap() = %q, want inner", got)
	}
	a := &ErrApply{StepID: "astep", Err: errors.New("inner")}
	if got := a.Error(); !strings.Contains(got, `apply failed on step "astep"`) {
		t.Errorf(`ErrApply.Error() = %q, want it to contain the step id`, got)
	}
	if got := a.Unwrap().Error(); got != "inner" {
		t.Errorf("Unwrap() = %q, want inner", got)
	}
}
