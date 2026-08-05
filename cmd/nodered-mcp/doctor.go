package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/fgjcarlos/nodered-mcp/internal/lifecycle"
)

// doctorPlanID is the canonical id of the doctor's last verified
// Plan. Doctor reads receipts/<plan-id>.json for whichever PlanID is
// passed via --plan; the default is setupPlanID because setup is the
// only Plan shipped today.
const doctorDefaultPlanID = setupPlanID

// runDoctor is the entry point for `nodered-mcp doctor`. It is the
// read-only counterpart to runSetup: no Apply is ever invoked, only
// Verify on the steps that the last receipt recorded.
//
//	--config-dir <path>  override the target config directory
//	--plan <id>          which receipt to re-verify (default "setup")
//
// Doctor prints one of three outcomes:
//
//	ok      every step in the last receipt still verifies
//	drift   some step no longer verifies — surface the failure list
//	missing no receipt on disk yet (first run; suggest `setup`)
func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		configDir string
		planID    string
	)
	fs.StringVar(&configDir, "config-dir", defaultConfigDir(), "target config directory (env NODERED_MCP_HOME)")
	fs.StringVar(&planID, "plan", string(doctorDefaultPlanID), "which plan receipt to re-verify")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	store, err := lifecycle.NewStore()
	if err != nil {
		return fmt.Errorf("doctor: open receipt store: %w", err)
	}
	rec, err := store.Load(lifecycle.PlanID(planID))
	switch {
	case errors.Is(err, lifecycle.ErrReceiptNotFound):
		fmt.Fprintf(os.Stdout, "doctor: no receipt for plan %q yet — run `nodered-mcp setup` first\n", planID)
		return nil
	case errors.Is(err, lifecycle.ErrReceiptCorrupt):
		fmt.Fprintf(os.Stdout, "doctor: receipt for plan %q is corrupt on disk; re-run `setup` to rebuild\n", planID)
		return nil
	case err != nil:
		return fmt.Errorf("doctor: load receipt: %w", err)
	}

	plan, err := buildDoctorPlan(lifecycle.PlanID(planID), configDir, rec)
	if err != nil {
		return err
	}

	// Doctor is read-only — override every Apply with a no-op. The
	// plan we ran originally may have side-effects; we want to verify
	// the post-state without re-applying it.
	for i := range plan.Steps {
		plan.Steps[i].Apply = nil
	}

	res, runErr := lifecycle.Run(context.Background(), plan)
	renderReceipt(os.Stdout, res)

	// Any non-help error from the runner — *ErrVerify, *ErrApply, or
	// a step's domain error — is "drift": the live state no longer
	// matches what the last receipt claimed. Surface it as exit 2
	// and a diagnostic, not as a generic non-zero error code that
	// CI scripts would treat as "doctor crashed".
	if runErr != nil {
		fmt.Fprintf(os.Stdout, "doctor: drift detected — run `nodered-mcp setup --force` to repair\n")
		doctorExit(2)
	}

	// Defensive: if Run somehow returned nil but a step isn't
	// "verified" (the runner currently never does this, but the
	// contract is "verified means ok, anything else is drift"),
	// surface that as drift too.
	for _, s := range res.Receipt.Steps {
		if s.Status != "verified" {
			fmt.Fprintf(os.Stdout, "doctor: drift detected — run `nodered-mcp setup --force` to repair\n")
			doctorExit(2)
		}
	}
	fmt.Fprintf(os.Stdout, "doctor: ok — last applied %s, %d step(s) verified\n",
		rec.EndedAt.Format("2006-01-02 15:04:05 UTC"), len(res.Receipt.Steps))
	return nil
}

// doctorExit is the seam that lets tests assert on the drift path
// without terminating the test process. Production wiring uses
// os.Exit; tests override it to record the requested code.
var doctorExit = func(code int) { os.Exit(code) }

// buildDoctorPlan re-derives the Verify functions from the config the
// receipt claims to have applied. The receipt carries the step IDs in
// order; the Verify functions are reconstructed from configDir +
// step type. Today only setupPlanID is supported; future plans wire
// their own verify builder.
func buildDoctorPlan(planID lifecycle.PlanID, configDir string, rec *lifecycle.Receipt) (lifecycle.Plan, error) {
	switch planID {
	case setupPlanID:
		// Re-build the setup plan but with nil Apply (we override
		// to nil again in runDoctor — here we just need the Verify
		// functions to exist).
		p, err := buildSetupPlan(configDir)
		if err != nil {
			return lifecycle.Plan{}, err
		}
		// Trim steps the receipt did not record. If a future
		// setup adds steps and the user has an old receipt,
		// doctor verifies only the steps we know how to check.
		known := make(map[string]bool, len(rec.Steps))
		for _, s := range rec.Steps {
			known[s.ID] = true
		}
		kept := p.Steps[:0]
		for _, s := range p.Steps {
			if known[s.ID] {
				kept = append(kept, s)
			}
		}
		p.Steps = kept
		return p, nil
	default:
		return lifecycle.Plan{}, fmt.Errorf("doctor: no verify builder registered for plan %q", planID)
	}
}
