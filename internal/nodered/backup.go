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
	"time"
)

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

	// ponytail: millisecond stamp; two writes in the same ms overwrite one
	// backup. Add a counter/nanos if that ever bites.
	name := "flows-" + time.Now().UTC().Format("20060102-150405.000") + ".json"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", fmt.Errorf("backup: writing %q: %w", path, err)
	}

	slog.Info("flows backup written", "file", path, "bytes", len(raw))
	return path, nil
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
