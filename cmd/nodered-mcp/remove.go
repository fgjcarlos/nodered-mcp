package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/fgjcarlos/nodered-mcp/internal/lifecycle"
)

// runRemove is the entry point for `nodered-mcp remove`. It loads
// the last receipt for the plan and unlinks every path in the
// manifest, refusing to touch anything outside it.
//
//	--plan <id>          which plan receipt to remove (default "setup")
//	--yes                skip the confirm prompt
//	--dry-run            print the actions without unlinking
//
// Foreign configuration preservation is enforced by the lifecycle
// manifest, not by this function: we walk Receipt.Manifest only.
// Anything not in the manifest is invisible to remove.
func runRemove(args []string) error {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		planID string
		yes    bool
		dryRun bool
	)
	fs.StringVar(&planID, "plan", string(setupPlanID), "which plan receipt to remove")
	fs.BoolVar(&yes, "yes", false, "skip the confirm prompt")
	fs.BoolVar(&dryRun, "dry-run", false, "print actions without unlinking")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	store, err := lifecycle.NewStore()
	if err != nil {
		return fmt.Errorf("remove: open receipt store: %w", err)
	}
	rec, err := store.Load(lifecycle.PlanID(planID))
	switch {
	case errors.Is(err, lifecycle.ErrReceiptNotFound):
		fmt.Fprintf(os.Stdout, "remove: no receipt for plan %q; nothing to do\n", planID)
		return nil
	case errors.Is(err, lifecycle.ErrReceiptCorrupt):
		return fmt.Errorf("remove: receipt for plan %q is corrupt; refusing to remove without a manifest — re-run `setup` first", planID)
	case err != nil:
		return fmt.Errorf("remove: load receipt: %w", err)
	}

	if len(rec.Manifest) == 0 {
		fmt.Fprintf(os.Stdout, "remove: plan %q has no manifest entries (last run failed or predates #230); nothing to remove\n", planID)
		return nil
	}

	// Confirm unless --yes. We confirm by listing exactly what we
	// are about to unlink — no "are you sure? [y/N]" line noise
	// that auto-yes scripts would have to fight.
	fmt.Fprintf(os.Stdout, "remove: about to unlink %d path(s) owned by plan %q:\n", len(rec.Manifest), planID)
	for _, p := range rec.Manifest {
		fmt.Fprintf(os.Stdout, "  %s [%s]\n", p.Path, p.Kind)
	}
	if !yes {
		fmt.Fprintf(os.Stderr, "remove: re-run with --yes to confirm (or --dry-run to preview)\n")
		return fmt.Errorf("remove: aborted (use --yes to confirm)")
	}

	for _, p := range rec.Manifest {
		if dryRun {
			fmt.Fprintf(os.Stdout, "  [dry-run] would unlink %s [%s]\n", p.Path, p.Kind)
			continue
		}
		if err := lifecycleRemoveOwned(p, rec.Manifest); err != nil {
			// A directory with foreign content is a successful
			// outcome, not a failure: the owned files inside
			// the directory are gone, the foreign ones survive,
			// and the directory itself is left in place so the
			// foreign content remains reachable. Surface this
			// as a status line and keep going.
			if errors.Is(err, lifecycle.ErrDirNotEmpty) {
				fmt.Fprintf(os.Stdout, "  kept %s (foreign entries present, preserved)\n", p.Path)
				continue
			}
			return fmt.Errorf("remove: unlink %s: %w", p.Path, err)
		}
		fmt.Fprintf(os.Stdout, "  ok %s\n", p.Path)
	}

	// Best-effort: drop the receipt so a subsequent `setup` starts
	// from a clean slate. If the unlink fails (read-only store,
	// permissions), remove still succeeded for the user-facing
	// state; surface the warning but do not fail the command.
	if !dryRun {
		if err := lifecycleRemoveReceipt(lifecycle.PlanID(planID)); err != nil {
			fmt.Fprintf(os.Stderr, "remove: warning: could not delete receipt file: %v\n", err)
		}
	}
	// After the manifest walk, attempt to prune the config dir
	// only if it ended up empty. Foreign content keeps the dir
	// alive — the function emits a "kept (foreign entries
	// preserved)" line and returns silently. This is what
	// enforces the byte-for-byte preservation contract: a user
	// who dropped a foreign file into the config dir survives
	// `remove` with that file intact.
	pruneConfigDirIfEmpty(defaultConfigDir(), dryRun)
	return nil
}

// pruneConfigDirIfEmpty removes configDir iff it exists and is
// empty after the manifest walk. Pulled out so the setup plan can
// pass the same path it created — the user's foreign files keep
// the dir alive; only an all-foreign-cleared dir is fair game.
// Best-effort: foreign content is preserved, and a non-empty dir
// is a successful outcome.
func pruneConfigDirIfEmpty(configDir string, dryRun bool) {
	info, err := os.Stat(configDir)
	if err != nil {
		return // already gone — perfect
	}
	if !info.IsDir() {
		return
	}
	entries, err := os.ReadDir(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remove: warning: could not read %s: %v\n", configDir, err)
		return
	}
	if len(entries) > 0 {
		fmt.Fprintf(os.Stdout, "  kept %s (foreign entries present, preserved)\n", configDir)
		return
	}
	if dryRun {
		fmt.Fprintf(os.Stdout, "  [dry-run] would unlink empty %s\n", configDir)
		return
	}
	if err := os.Remove(configDir); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "remove: warning: could not remove empty %s: %v\n", configDir, err)
		return
	}
	fmt.Fprintf(os.Stdout, "  ok %s (empty)\n", configDir)
}

// lifecycleRemoveOwned and lifecycleRemoveReceipt are the package's
// thin seam to the lifecycle helpers. Tests can swap them via
// the exported var, but production wiring goes straight to the
// lifecycle package.
var (
	lifecycleRemoveOwned   = lifecycle.RemoveOwnedPath
	lifecycleRemoveReceipt = lifecycle.RemoveReceipt
)

// runRollback is the entry point for `nodered-mcp rollback`. It
// reverses the most recent run: if the last receipt's status was
// "verified" (a successful run), rollback unlinks the manifest
// just like `remove`. If the last receipt is corrupt or predates
// the manifest field, the command errors out with an actionable
// hint.
//
// The two commands intentionally share the same code path because
// today's setup is fully atomic step-by-step — there is no
// "intermediate state" to undo that remove would not also undo.
// Future plans that introduce non-atomic Steps can split the
// semantics; for now, rollback == remove.
func runRollback(args []string) error {
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		planID string
		yes    bool
		dryRun bool
	)
	fs.StringVar(&planID, "plan", string(setupPlanID), "which plan receipt to roll back")
	fs.BoolVar(&yes, "yes", false, "skip the confirm prompt")
	fs.BoolVar(&dryRun, "dry-run", false, "print actions without unlinking")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	store, err := lifecycle.NewStore()
	if err != nil {
		return fmt.Errorf("rollback: open receipt store: %w", err)
	}
	rec, err := store.Load(lifecycle.PlanID(planID))
	switch {
	case errors.Is(err, lifecycle.ErrReceiptNotFound):
		fmt.Fprintf(os.Stdout, "rollback: no receipt for plan %q; nothing to roll back\n", planID)
		return nil
	case errors.Is(err, lifecycle.ErrReceiptCorrupt):
		return fmt.Errorf("rollback: receipt for plan %q is corrupt; refusing to roll back without a manifest", planID)
	case err != nil:
		return fmt.Errorf("rollback: load receipt: %w", err)
	}

	if len(rec.Manifest) == 0 {
		// A verified-once-and-now-empty case is impossible: a
		// verified step with Owns always populates the manifest.
		// Reaching here means the last run never reached
		// verified for any step, i.e. it failed. There is no
		// "intermediate state" to roll back to — the partial
		// files from the failed run were not declared as owned
		// (the manifest contract is "verified ⇒ owned"), so
		// rollback is a no-op. Tell the user what we know.
		fmt.Fprintf(os.Stdout, "rollback: last run for plan %q did not verify any step (manifest empty); no managed state to roll back\n", planID)
		fmt.Fprintf(os.Stdout, "rollback: hint: if files were left behind, they are foreign to this plan and were not owned by it — inspect manually\n")
		return nil
	}

	fmt.Fprintf(os.Stdout, "rollback: about to unlink %d path(s) from the last receipt of plan %q:\n", len(rec.Manifest), planID)
	for _, p := range rec.Manifest {
		fmt.Fprintf(os.Stdout, "  %s [%s]\n", p.Path, p.Kind)
	}
	if !yes {
		fmt.Fprintf(os.Stderr, "rollback: re-run with --yes to confirm (or --dry-run to preview)\n")
		return fmt.Errorf("rollback: aborted (use --yes to confirm)")
	}

	for _, p := range rec.Manifest {
		if dryRun {
			fmt.Fprintf(os.Stdout, "  [dry-run] would unlink %s [%s]\n", p.Path, p.Kind)
			continue
		}
		if err := lifecycleRemoveOwned(p, rec.Manifest); err != nil {
			if errors.Is(err, lifecycle.ErrDirNotEmpty) {
				fmt.Fprintf(os.Stdout, "  kept %s (foreign entries present, preserved)\n", p.Path)
				continue
			}
			return fmt.Errorf("rollback: unlink %s: %w", p.Path, err)
		}
		fmt.Fprintf(os.Stdout, "  ok %s\n", p.Path)
	}

	if !dryRun {
		if err := lifecycleRemoveReceipt(lifecycle.PlanID(planID)); err != nil {
			fmt.Fprintf(os.Stderr, "rollback: warning: could not delete receipt file: %v\n", err)
		}
	}
	pruneConfigDirIfEmpty(defaultConfigDir(), dryRun)
	return nil
}
