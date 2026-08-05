package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fgjcarlos/nodered-mcp/internal/lifecycle"
)

// setupPlanID is the canonical id of the setup Plan. It becomes the
// receipt file name under $XDG_STATE_HOME/nodered-mcp/receipts/.
const setupPlanID = lifecycle.PlanID("setup")

// runSetup is the entry point for `nodered-mcp setup`. It builds the
// setup Plan, runs it through the lifecycle Runner, and persists the
// Result as a Receipt. Flags:
//
//	--config-dir <path>  override the target config directory (default ~/.nodered-mcp)
//	--force             re-apply every step even if the receipt says they verified
//	--no-receipt        skip the persistence step (useful in tests)
//
// Setup is intentionally minimal in this first cut: it ensures the
// config dir exists, writes a stub .env.example the user can edit,
// and verifies both are in place. Future issues (#230 remove/rollback,
// #228 update contract) build on this foundation.
func runSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		configDir string
		force     bool
		noReceipt bool
	)
	fs.StringVar(&configDir, "config-dir", defaultConfigDir(), "target config directory (env NODERED_MCP_HOME)")
	fs.BoolVar(&force, "force", false, "re-apply every step even if the previous receipt verified")
	fs.BoolVar(&noReceipt, "no-receipt", false, "do not persist a receipt (test-only)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	plan, err := buildSetupPlan(configDir)
	if err != nil {
		return err
	}

	// Idempotency short-circuit: if the previous receipt verified all
	// steps and the user did not pass --force, skip the re-apply.
	if !force && !noReceipt {
		prev, lerr := loadPreviousReceipt(setupPlanID)
		if lerr == nil && allVerified(prev) {
			fmt.Fprintf(os.Stdout, "setup: already applied at %s, nothing to do (use --force to re-apply)\n",
				prev.EndedAt.Format("2006-01-02 15:04:05 UTC"))
			return nil
		}
	}

	res, runErr := lifecycle.Run(context.Background(), plan)
	renderReceipt(os.Stdout, res)

	if runErr != nil {
		// Apply or verify failed. We still attempt to persist a
		// receipt so the next run knows where we stopped — without
		// it, doctor would have to re-discover the failure each
		// invocation.
		if !noReceipt {
			if store, serr := lifecycle.NewStore(); serr == nil {
				_ = store.Save(res) // best-effort; the error path already surfaces the original
			}
		}
		return runErr
	}

	if !noReceipt {
		store, serr := lifecycle.NewStore()
		if serr != nil {
			return fmt.Errorf("setup: open receipt store: %w", serr)
		}
		if err := store.Save(res); err != nil {
			return fmt.Errorf("setup: persist receipt: %w", err)
		}
	}
	return nil
}

// buildSetupPlan returns the Plan that runSetup executes. Pulled out
// for testability — the plan is the unit under test, not the flag
// parser.
func buildSetupPlan(configDir string) (lifecycle.Plan, error) {
	if strings.TrimSpace(configDir) == "" {
		return lifecycle.Plan{}, fmt.Errorf("setup: empty config dir")
	}
	envExample := filepath.Join(configDir, ".env.example")
	return lifecycle.Plan{
		ID:   setupPlanID,
		Name: "nodered-mcp setup",
		Steps: []lifecycle.Step{
			{
				ID:          "ensure-config-dir",
				Description: "create " + configDir,
				Apply: func(ctx context.Context) error {
					return os.MkdirAll(configDir, 0o700)
				},
				Verify: func(ctx context.Context) error {
					info, err := os.Stat(configDir)
					if err != nil {
						return err
					}
					if !info.IsDir() {
						return fmt.Errorf("%s exists but is not a directory", configDir)
					}
					return nil
				},
			},
			{
				ID:          "write-env-example",
				Description: "write stub .env.example at " + envExample,
				Apply: func(ctx context.Context) error {
					// Idempotent: only write if absent or content
					// drifted. Re-running setup never overwrites a
					// user's edits.
					want := []byte(envExampleContents)
					existing, err := os.ReadFile(envExample)
					if err == nil && string(existing) == string(want) {
						return nil
					}
					return os.WriteFile(envExample, want, 0o600)
				},
				Verify: func(ctx context.Context) error {
					got, err := os.ReadFile(envExample)
					if err != nil {
						return err
					}
					if string(got) != envExampleContents {
						return fmt.Errorf(".env.example content drifted from the managed stub")
					}
					return nil
				},
			},
		},
	}, nil
}

// envExampleContents is the managed stub written by `setup`. Kept as a
// const so the verify check has a stable reference — never derive
// verify content from Apply-time data, that creates non-deterministic
// receipts.
//
// ponytail: this stub is intentionally tiny. Future steps (real Node-RED
// URL/token prompts, OAuth issuer discovery) belong in follow-up
// tickets — bundling them here would expand scope past "foundation".
const envExampleContents = `# nodered-mcp config template — generated by ` + "`nodered-mcp setup`" + `.
# Copy to .env and edit. The server reads these at startup.

# Node-RED base URL the server talks to (required).
# NODERED_URL=http://localhost:1880

# Node-RED admin API token (optional but recommended).
# NODERED_TOKEN=

# MCP transport: stdio (default) or http.
# MCP_TRANSPORT=stdio

# Required when MCP_TRANSPORT=http: bearer token or OAuth issuer.
# MCP_HTTP_TOKEN=
# MCP_OAUTH_ISSUER=
# MCP_OAUTH_AUDIENCE=
`

// defaultConfigDir returns ~/.nodered-mcp, falling back to
// $NODERED_MCP_HOME when set. Resolved once per invocation; not
// cached in a package var so tests can swap the env.
func defaultConfigDir() string {
	if v := os.Getenv("NODERED_MCP_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".nodered-mcp"
	}
	return filepath.Join(home, ".nodered-mcp")
}

// loadPreviousReceipt returns the last persisted receipt for planID,
// mapping lifecycle.ErrReceiptNotFound and ErrReceiptCorrupt to nil
// (the absence is informational, not an error).
func loadPreviousReceipt(planID lifecycle.PlanID) (*lifecycle.Receipt, error) {
	store, err := lifecycle.NewStore()
	if err != nil {
		return nil, err
	}
	rec, err := store.Load(planID)
	if errors.Is(err, lifecycle.ErrReceiptNotFound) || errors.Is(err, lifecycle.ErrReceiptCorrupt) {
		return nil, nil
	}
	return rec, err
}

// allVerified reports whether every step in the receipt finished with
// the "verified" status. Used by setup's idempotency short-circuit.
func allVerified(rec *lifecycle.Receipt) bool {
	if rec == nil {
		return false
	}
	for _, s := range rec.Steps {
		if s.Status != "verified" {
			return false
		}
	}
	return true
}

// renderReceipt writes a one-line-per-step summary to w. Kept here
// (not in lifecycle) because the CLI owns the user-facing copy.
func renderReceipt(w io.Writer, res *lifecycle.Result) {
	if res == nil {
		return
	}
	fmt.Fprintf(w, "%s: %s\n", res.PlanID, res.Receipt.StartedAt.Format("2006-01-02 15:04:05 UTC"))
	for _, s := range res.Receipt.Steps {
		switch s.Status {
		case "verified":
			fmt.Fprintf(w, "  ok    %s\n", s.ID)
		case "applied":
			fmt.Fprintf(w, "  apply %s\n", s.ID)
		case "failed":
			fmt.Fprintf(w, "  FAIL  %s: %s\n", s.ID, s.Err)
		default:
			fmt.Fprintf(w, "  %-6s %s\n", s.Status, s.ID)
		}
	}
}
