package nodered

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// defaultBackupKeep is the default retention limit.
const defaultBackupKeep = 50

// snapshotSeq is a process-global monotonic counter appended to backup
// filenames as a tie-breaker for clock collisions. The system timer on
// Windows ticks at ~15.6ms by default, so nanosecond timestamps still
// collide under burst writes; this counter makes that impossible.
var snapshotSeq atomic.Uint64

// snapshotFlows fetches the full current flow config and writes it to a
// timestamped file under the configured backup directory, BEFORE any mutating
// operation. It is the safety net that makes flow writes recoverable.
//
// It is fail-closed: if the snapshot cannot be written (unreachable Node-RED,
// unwritable dir), the caller aborts the write rather than risk an
// unrecoverable change. Returns the path of the backup file on success.
func (c *Client) snapshotFlows(ctx context.Context) (string, error) {
	raw, err := c.ListFlows(ctx)
	if err != nil {
		return "", fmt.Errorf("backup: fetching current flows: %w", err)
	}

	dir := c.backupsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("backup: creating dir %q: %w", dir, err)
	}

	// Nanosecond stamp + per-process counter keeps concurrent snapshot
	// writes from overwriting one another — millisecond resolution was
	// the source of #98. The counter is the real collision guard: on
	// Windows the system timer ticks at ~15.6ms by default, so even
	// nanosecond stamps collide under burst writes (CI run 30749881857
	// saw 50 concurrent snapshots collapse to 20 files).
	seq := snapshotSeq.Add(1)
	name := fmt.Sprintf("flows-%s-%06d.json",
		time.Now().UTC().Format("20060102-150405.000000000"), seq)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", fmt.Errorf("backup: writing %q: %w", path, err)
	}

	slog.Info("flows backup written", "file", path, "bytes", len(raw))

	if c.backupKeep > 0 {
		pruneBackups(dir, c.backupKeep)
	}

	return path, nil
}

// pruneBackups deletes the oldest backup files in dir beyond the keep limit.
// Filenames are sorted descending (newest first) because the timestamp is
// encoded in the name with fixed width; everything beyond position keep is
// removed. Deletion errors are logged but do not propagate — a failure to
// prune must never block the caller's write operation.
func pruneBackups(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		slog.Warn("backup prune: reading dir failed", "dir", dir, "error", err)
		return
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "flows-") && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	// Sort descending: newest first (timestamp encoded in name, fixed width).
	sort.Sort(sort.Reverse(sort.StringSlice(names)))

	if len(names) <= keep {
		return
	}
	toDelete := names[keep:]
	deleted := 0
	for _, name := range toDelete {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			slog.Warn("backup prune: removing file failed", "file", name, "error", err)
		} else {
			deleted++
		}
	}
	if deleted > 0 {
		slog.Info("backup prune: deleted old backups", "deleted", deleted, "kept", keep)
	}
}

// BackupInfo describes one saved flow snapshot.
type BackupInfo struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modified"`
}

// backupsDir returns the configured backup directory, defaulting to "backups".
func (c *Client) backupsDir() string {
	if c.backupDir == "" {
		return "backups"
	}
	return c.backupDir
}

// ListBackups returns the saved flow snapshots, newest first. The timestamp is
// encoded in the filename with fixed width, so a descending name sort is a
// reliable chronological order. Returns an empty slice if no backups exist yet.
func (c *Client) ListBackups() ([]BackupInfo, error) {
	entries, err := os.ReadDir(c.backupsDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading backup dir: %w", err)
	}

	var out []BackupInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "flows-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, BackupInfo{Name: e.Name(), Size: info.Size(), ModTime: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}

// ReadBackup returns the raw contents of a saved snapshot by its bare filename.
//
// name must be a bare filename (as returned by ListBackups): any path
// separator or ".." is rejected so a caller — including an LLM — cannot read
// arbitrary files off disk via path traversal.
func (c *Client) ReadBackup(name string) (RawFlow, error) {
	if name == "" {
		return nil, errors.New("backup name is required")
	}
	if name == ".." || strings.ContainsAny(name, `/\`) {
		return nil, errors.New("backup name must be a bare filename, not a path")
	}
	data, err := os.ReadFile(filepath.Join(c.backupsDir(), name))
	if err != nil {
		return nil, fmt.Errorf("reading backup %q: %w", name, err)
	}
	return data, nil
}
