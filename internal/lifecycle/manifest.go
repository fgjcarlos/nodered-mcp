package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PathKind classifies an OwnedPath by the kind of side-effect that
// created it. remove/rollback use this to pick the right unlink
// primitive (file vs directory) and to refuse operations on kinds
// it doesn't recognize — a missing Kind is the cheapest signal that
// a receipt is stale or came from a future schema.
type PathKind string

const (
	KindFile PathKind = "file"
	KindDir  PathKind = "dir"
)

// OwnedPath is one entry in a Plan's manifest: a path on disk that
// the Plan created, plus the metadata remove/rollback need to act
// on it safely. The Owner field namespaces paths across plans
// (today every Plan uses "nodered-mcp"; the field exists so two
// plans on the same machine cannot accidentally remove each other's
// state).
type OwnedPath struct {
	Path  string   `json:"path"`
	Kind  PathKind `json:"kind"`
	Owner string   `json:"owner"`
}

// Manifest is the slice of OwnedPaths a Receipt is responsible for.
// remove/rollback walk this list and refuse to touch anything else.
// The Manifest is populated by the Runner at the end of a successful
// run, so an apply that errored halfway produces a Receipt with an
// empty (or partial) Manifest — the safe state for remove/rollback.
type Manifest []OwnedPath

// Has reports whether the manifest contains exactly the given path.
// Used by tests and by the verify surface ("did the last run claim
// this path?").
func (m Manifest) Has(p string) bool {
	for _, op := range m {
		if op.Path == p {
			return true
		}
	}
	return false
}

// ErrForeignPath is returned by a remove/rollback operation when a
// path on disk was expected to belong to the plan but does not match
// the plan's manifest. The caller is expected to surface this as a
// hard error: touching that path would risk deleting user data.
var ErrForeignPath = errors.New("lifecycle: path is not owned by this plan")

// ErrDirNotEmpty is returned by RemoveOwnedPath with KindDir when the
// target directory still has entries after sibling manifest files
// have been unlinked. The CLI surfaces this as "owned files removed,
// foreign entries preserved, the directory itself was left in place".
// This is the central byte-for-byte preservation guarantee: remove
// will never recursively delete a directory whose contents the
// manifest does not enumerate.
var ErrDirNotEmpty = errors.New("lifecycle: directory not empty (foreign entries preserved)")

// validateOwnedPath rejects path-traversal and empty paths. Owner
// is restricted to the same alphabet as plan IDs for symmetry.
func validateOwnedPath(p OwnedPath) error {
	if p.Path == "" {
		return fmt.Errorf("lifecycle: owned path is empty")
	}
	if !filepath.IsAbs(p.Path) {
		return fmt.Errorf("lifecycle: owned path %q is not absolute", p.Path)
	}
	if p.Kind != KindFile && p.Kind != KindDir {
		return fmt.Errorf("lifecycle: owned path %q has unknown kind %q", p.Path, p.Kind)
	}
	if p.Owner == "" {
		return fmt.Errorf("lifecycle: owned path %q has empty owner", p.Path)
	}
	for _, r := range p.Owner {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if !ok {
			return fmt.Errorf("lifecycle: owned path %q has invalid owner %q", p.Path, p.Owner)
		}
	}
	// Reject any path that escapes its root via "..". filepath.Clean
	// collapses ".." segments, so "/abs/../etc" becomes "/etc" (still
	// absolute, no leading ".."). The traversal signal we want to
	// catch is "the cleaned path no longer starts with the original
	// parent". A simpler, more conservative check: a literal ".."
	// segment anywhere in the path is a programmer error in a
	// manifest entry — manifest authors should use absolute paths
	// throughout. We split on the OS separator and refuse any
	// segment that is exactly "..".
	cleaned := filepath.Clean(p.Path)
	if cleaned != p.Path {
		return fmt.Errorf("lifecycle: owned path %q is not in canonical form (clean: %q)", p.Path, cleaned)
	}
	parts := strings.Split(p.Path, string(filepath.Separator))
	for _, part := range parts {
		if part == ".." {
			return fmt.Errorf("lifecycle: owned path %q contains a .. segment", p.Path)
		}
	}
	return nil
}

// RemoveOwnedPath is the public entry point for `remove` and
// `rollback` in the CLI: delete one OwnedPath from disk, validated
// against the manifest that claims it. The package-level helper
// exists so the cmd package can wire it without a Store instance.
//
// For directories, RemoveOwnedPath refuses to recursively delete a
// non-empty directory — the safety contract is "byte-for-byte
// preservation of foreign configuration". An empty dir is fine to
// remove; a dir that still contains foreign entries is left alone
// and the caller is told via removeResult.
func RemoveOwnedPath(p OwnedPath, manifest Manifest) error {
	return removeOwnedPath(p, manifest)
}

// removeOwnedPath deletes one OwnedPath from disk. Returns nil when
// no-op for the same Manifest). Returns ErrForeignPath when the
// path on disk is NOT one of the manifest's declared paths (the
// caller should never reach this branch — the public remove
// surface filters first — but the helper enforces the contract
// internally so the safety property holds end-to-end).
func removeOwnedPath(p OwnedPath, manifest Manifest) error {
	if err := validateOwnedPath(p); err != nil {
		return err
	}
	if !manifest.Has(p.Path) {
		return fmt.Errorf("%w: %s", ErrForeignPath, p.Path)
	}
	info, err := os.Lstat(p.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // already gone — idempotent
		}
		return fmt.Errorf("lifecycle: stat %s: %w", p.Path, err)
	}
	// Cross-check: a file Owner declared as KindDir (or vice versa)
	// is a stale manifest, refuse to rm -rf something that is
	// actually a regular file (or vice versa).
	switch p.Kind {
	case KindFile:
		if info.IsDir() {
			return fmt.Errorf("lifecycle: %s expected to be a file but is a directory", p.Path)
		}
		return os.Remove(p.Path)
	case KindDir:
		if !info.IsDir() {
			return fmt.Errorf("lifecycle: %s expected to be a directory but is a file", p.Path)
		}
		// Safety: a KindDir that still has entries after the
		// manifest's other files have been unlinked means the
		// dir contains foreign configuration. Refuse the rm -rf
		// — leave the dir in place so the foreign content
		// survives. The CLI surfaces this as a clear "owned
		// files removed, foreign entries preserved" message
		// rather than a generic error.
		entries, err := os.ReadDir(p.Path)
		if err != nil {
			return fmt.Errorf("lifecycle: read dir %s: %w", p.Path, err)
		}
		if len(entries) > 0 {
			return ErrDirNotEmpty
		}
		return os.Remove(p.Path)
	default:
		return fmt.Errorf("lifecycle: %s has unknown kind %q", p.Path, p.Kind)
	}
}
