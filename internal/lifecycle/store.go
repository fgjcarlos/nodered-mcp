package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Store persists Receipts under a root directory. The default root is
// $XDG_STATE_HOME/nodered-mcp/receipts; tests inject a temp dir via
// NewStoreWithRoot.
//
// Layout: <root>/<plan-id>.json. One file per PlanID. NewStore rejects
// plan IDs that contain path separators so the on-disk shape cannot
// escape the root.
type Store struct {
	root string
}

// NewStore builds a Store rooted at $XDG_STATE_HOME/nodered-mcp/receipts.
// The directory is created with 0700 if missing — receipts are not
// sensitive but they are per-user state, so per-user perms are right.
//
// Tests inject a root via the env var NODERED_MCP_RECEIPTS_DIR so the
// production binary keeps the XDG-canonical default and the test
// suite never touches the real home directory.
func NewStore() (*Store, error) {
	root, err := defaultReceiptRoot()
	if err != nil {
		return nil, fmt.Errorf("lifecycle: resolve receipt root: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("lifecycle: create receipt root: %w", err)
	}
	return &Store{root: root}, nil
}

// NewStoreWithRoot is the test seam. The root must already exist; the
// caller is responsible for choosing a safe path (typically t.TempDir()).
func NewStoreWithRoot(root string) (*Store, error) {
	if root == "" {
		return nil, fmt.Errorf("lifecycle: empty receipt root")
	}
	return &Store{root: root}, nil
}

// Root returns the on-disk root used by this Store. Exported so callers
// (and tests) can inspect or clean up.
func (s *Store) Root() string { return s.root }

// Save writes the Receipt from a Result atomically: tmp file in the
// same directory, rename. The plan ID is validated to refuse any
// character that would let the path escape the root.
func (s *Store) Save(r *Result) error {
	if r == nil {
		return fmt.Errorf("lifecycle: nil result")
	}
	if err := validatePlanID(string(r.PlanID)); err != nil {
		return err
	}
	final := filepath.Join(s.root, string(r.PlanID)+".json")
	tmp, err := os.CreateTemp(s.root, ".tmp-"+string(r.PlanID)+"-*.json")
	if err != nil {
		return fmt.Errorf("lifecycle: create tmp receipt: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // best-effort on rename failure
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r.Receipt); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("lifecycle: encode receipt: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("lifecycle: close tmp receipt: %w", err)
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		return fmt.Errorf("lifecycle: rename receipt: %w", err)
	}
	return nil
}

// Load reads the persisted Receipt for planID. Returns ErrReceiptNotFound
// when the file is absent (first run, doctor should treat as "not yet
// applied"). Returns ErrReceiptCorrupt when the file exists but cannot
// be parsed — caller should warn and treat as missing rather than
// fabricate state.
func (s *Store) Load(planID PlanID) (*Receipt, error) {
	if err := validatePlanID(string(planID)); err != nil {
		return nil, err
	}
	path := filepath.Join(s.root, string(planID)+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrReceiptNotFound
		}
		return nil, fmt.Errorf("lifecycle: read receipt: %w", err)
	}
	var got Receipt
	if err := json.Unmarshal(data, &got); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrReceiptCorrupt, err)
	}
	return &got, nil
}

// LastResult is the typed helper callers want 99% of the time: Load,
// return a Result with the same shape Run returned. Doctor uses this
// to render "previous run was at X, N steps verified, M failed".
func (s *Store) LastResult(planID PlanID) (*Result, error) {
	rec, err := s.Load(planID)
	if err != nil {
		return nil, err
	}
	return &Result{PlanID: planID, Receipt: *rec}, nil
}

// RemoveReceipt deletes the persisted receipt for planID. It is the
// store-side counterpart to the package-level RemoveReceipt helper;
// the helper validates the plan ID and walks the on-disk root
// whereas Store knows the root. Best-effort callers should treat
// os.IsNotExist as success (idempotent re-runs of remove/rollback).
func (s *Store) RemoveReceipt(planID PlanID) error {
	if err := validatePlanID(string(planID)); err != nil {
		return err
	}
	path := filepath.Join(s.root, string(planID)+".json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("lifecycle: remove receipt: %w", err)
	}
	return nil
}

// RemoveReceipt is the package-level helper used by the remove and
// rollback CLI commands. It opens the default Store and deletes the
// receipt for planID.
func RemoveReceipt(planID PlanID) error {
	store, err := NewStore()
	if err != nil {
		return err
	}
	return store.RemoveReceipt(planID)
}

// defaultReceiptRoot returns the canonical receipts directory. Uses
// $XDG_STATE_HOME when set (per the XDG Base Directory spec), otherwise
// ~/.local/state, otherwise ~/.nodered-mcp/receipts as a last resort.
//
// NODERED_MCP_RECEIPTS_DIR wins when set — it is the test seam that
// keeps the test suite from writing under the real XDG path.
func defaultReceiptRoot() (string, error) {
	if v := os.Getenv("NODERED_MCP_RECEIPTS_DIR"); v != "" {
		return v, nil
	}
	var base string
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		base = xdg
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "nodered-mcp", "receipts"), nil
}

// validatePlanID refuses anything outside [A-Za-z0-9._-]. Plan IDs come
// from compile-time constants in the calling code (e.g. "setup",
// "doctor") so this is not a user-input concern; the check exists to
// make the on-disk layout impossible to escape if a future caller
// accidentally builds a plan ID from config.
func validatePlanID(id string) error {
	if id == "" {
		return fmt.Errorf("lifecycle: empty plan id")
	}
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if !ok {
			return fmt.Errorf("lifecycle: invalid plan id %q", id)
		}
	}
	return nil
}

// Ensure context import survives even if Run's signature changes —
// keeps the package import graph stable for downstream callers that
// only use Store today and may adopt Run later.
var _ = context.Background
