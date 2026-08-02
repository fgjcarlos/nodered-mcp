package nodered

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestListBackups_NewestFirstAndFilters(t *testing.T) {
	dir := t.TempDir()
	// Timestamped names sort chronologically; write out of order on purpose.
	for _, name := range []string{
		"flows-20250101-120000.000.json",
		"flows-20250103-120000.000.json",
		"flows-20250102-120000.000.json",
		"not-a-backup.txt", // must be ignored
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("[]"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	c, _ := NewClient(Options{BaseURL: "http://x", BackupDir: dir})
	got, err := c.ListBackups()
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 backups (txt ignored), got %d", len(got))
	}
	if got[0].Name != "flows-20250103-120000.000.json" {
		t.Errorf("expected newest first, got %s", got[0].Name)
	}
}

func TestListBackups_NoDir(t *testing.T) {
	c, _ := NewClient(Options{BaseURL: "http://x", BackupDir: filepath.Join(t.TempDir(), "nope")})
	got, err := c.ListBackups()
	if err != nil {
		t.Fatalf("expected nil error for missing dir, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no backups, got %d", len(got))
	}
}

func TestReadBackup_RejectsPathTraversal(t *testing.T) {
	c, _ := NewClient(Options{BaseURL: "http://x", BackupDir: t.TempDir()})
	for _, bad := range []string{"", "..", "../secret", `..\secret`, "sub/flows.json", `sub\flows.json`} {
		if _, err := c.ReadBackup(bad); err == nil {
			t.Errorf("expected ReadBackup(%q) to be rejected", bad)
		}
	}
}

func TestRestoreFlows_SnapshotsThenFullDeploy(t *testing.T) {
	dir := t.TempDir()
	var deployType string
	var postBody []byte
	var backedUp bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/flows":
			backedUp = true // pre-restore snapshot fetch
			_, _ = w.Write([]byte(`[{"id":"cur","type":"tab","label":"Current"}]`))
		case r.Method == "POST" && r.URL.Path == "/flows":
			deployType = r.Header.Get("Node-RED-Deployment-Type")
			postBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c, _ := NewClient(Options{BaseURL: srv.URL, Token: "t", BackupDir: dir})

	// A backup taken as the {rev,flows} envelope: the stale rev must NOT be
	// forwarded (we POST a bare array instead).
	backup := RawFlow(`{"rev":"stale","flows":[{"id":"old","type":"tab","label":"Restored"}]}`)
	if err := c.RestoreFlows(context.Background(), backup); err != nil {
		t.Fatalf("RestoreFlows: %v", err)
	}

	if !backedUp {
		t.Error("restore did not snapshot the current config first")
	}
	if deployType != "full" {
		t.Errorf("expected full deployment header, got %q", deployType)
	}
	if containsJSON(postBody, "stale") {
		t.Errorf("stale rev leaked into POST body: %s", postBody)
	}
	if !containsJSON(postBody, `"label":"Restored"`) {
		t.Errorf("restored flow missing from POST body: %s", postBody)
	}
	// The pre-restore snapshot should be on disk.
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("expected 1 pre-restore backup, got %d", len(entries))
	}
}

func TestRestoreFlows_RejectsGarbageBackup(t *testing.T) {
	c, _ := NewClient(Options{BaseURL: "http://x", BackupDir: t.TempDir()})
	if err := c.RestoreFlows(context.Background(), RawFlow(`"just a string"`)); err == nil {
		t.Fatal("expected RestoreFlows to reject a backup with no flow array")
	}
}

// TestSnapshotFlows_ConcurrentWritesHaveDistinctFilenames pins the fix for
// #98: backup filenames used millisecond resolution, so concurrent writes
// within the same ms silently overwrote each other (empirically: 20
// concurrent writes produced 7 files). Nanosecond resolution makes collisions
// vanishingly unlikely. We burst N goroutines and assert the backup dir
// ends up with exactly N distinct files — i.e. zero collisions.
func TestSnapshotFlows_ConcurrentWritesHaveDistinctFilenames(t *testing.T) {
	const N = 50

	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backups")

	// The handler just needs to return something parseable for every GET;
	// concurrency is what we're exercising, not the response shape.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)

	c, _ := NewClient(Options{BaseURL: srv.URL, Token: "t", BackupDir: backupDir})
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.snapshotFlows(ctx)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != N {
		t.Fatalf("expected %d distinct backups, got %d (collisions still happening)", N, len(entries))
	}
}

// TestSnapshotFlows_FileAndDirModeOwnerOnly covers the third invariant
// of #70: the backup file is owner-only (0o600) and, when the
// directory is created, it is owner-only (0o700). Skipped on Windows
// where the POSIX mode bits are not honoured the same way.
func TestSnapshotFlows_FileAndDirModeOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows mode bits differ; permission tests run on Unix only")
	}
	dir := t.TempDir()
	// Place the backup dir one level down so the test exercises
	// MkdirAll and the file write together, not just the write.
	backupDir := filepath.Join(dir, "backups")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	c, _ := NewClient(Options{BaseURL: srv.URL, Token: "t", BackupDir: backupDir})

	if _, err := c.snapshotFlows(context.Background()); err != nil {
		t.Fatalf("snapshotFlows: %v", err)
	}

	dirInfo, err := os.Stat(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if dp := dirInfo.Mode().Perm(); dp != 0o700 {
		t.Errorf("backup dir perm: expected 0o700, got %04o", dp)
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 backup, got %d", len(entries))
	}
	info, err := os.Stat(filepath.Join(backupDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if fp := info.Mode().Perm(); fp != 0o600 {
		t.Errorf("backup file perm: expected 0o600, got %04o", fp)
	}
}
