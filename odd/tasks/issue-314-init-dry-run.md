# Issue #314 — Preview merged client config with `init --dry-run`

## Objective
Let an operator see the exact configuration `init --write` would merge,
before anything touches the filesystem, with secrets redacted and
unsupported formats rejected clearly.

## Problem and evidence
`init` today (cmd/nodered-mcp/init.go) has exactly one write path.
`runInit` (line 125) parses `--all` and `--write`; with `--write` it calls
`writeClientConfig` (201), which resolves the target via `writableTarget`
(230) and calls `mergeServerIntoFile` (242).

`mergeServerIntoFile` reads the existing file, rejects a malformed
collection, sets `servers["nodered"]`, and ends at exactly one call:
`writeJSONObject(path, root)` (line 260). Everything before that call is
pure computation — the read, the type assertion, the merge. So the
preview is the same function with the final call replaced by a print.

Evidence of the current limitation: the non-`--write` path (line 155)
prints `renderConfig(...)`, a *freshly generated* snippet for a client
that may have no config file yet. It does not show the merge. The issue's
motivating case is rerunning `--write` against a file that already holds
other MCP servers: there is no way to verify the actual merged result
without writing it.

## Design chosen
`--dry-run` performs the real merge into memory and prints the exact
bytes that `writeJSONObject` would produce, then stops. It is a
**sibling of `--write`, not a mode of it**: no file, directory, backup,
temp file or permission change happens on the path.

Implementation seam, deliberately minimal:
- Split `mergeServerIntoFile` at its one write. Extract the pure part
  into a function returning the merged root; `mergeServerIntoFile`
  keeps its current signature and behavior by calling the pure part then
  `writeJSONObject`. `--dry-run` calls the pure part and marshals.
- Reject unsupported formats exactly as `--write` does, via the same
  `writableTarget` check, with wording that says preview is unsupported
  rather than implying a write was attempted.

Redaction: the merged `nodered` entry already goes through
`envWithoutToken` (line 258), so the new entry never carries a token.
The preview must also redact secrets in **pre-existing** entries of the
merged document, since the operator's own file may hold a token for
another server and the preview prints the whole document. Redact by key
name for the known secret-bearing keys; do not attempt value sniffing.

Rejected alternatives:
- **A separate `--show-merged` command or a lifecycle subcommand**: the
  issue says extend the existing init, and #229/#247 retired the
  setup/doctor machinery this would echo.
- **A `--dry-run` that re-implements the merge**: would drift from the
  write path and preview something other than what is written. The one
  reason to prefer it (no refactor) does not outweigh previewing a
  different result.

## Scope and constraints
- Fix #314 only.
- `mergeServerIntoFile` keeps its exact current behavior and signature;
  all existing tests must stay green untouched.
- All `writeJSONObject` safety invariants (#70) stay where they are —
  the dry-run path must not reach them at all.
- Unsupported automatic-write formats are rejected, not previewed.
- Malformed-collection and invalid-JSON safeguards must behave
  identically in preview and in write.
- No new dependency; `go.mod` must not change.
- No push, PR or issue closure without authorization.
- English artifacts; no AI or co-author attribution.

## Work unit
- [x] T1: Add `--dry-run` to init. Commits 7267af0 (writer) + 28ec939
  (parent correction).
  - Route: delegated direct (init.go + tests; 2 non-trivial files).
  - Risk: medium — this is a code path that reads user config; the
    danger is a preview that quietly mutates, and a refactor of the
    write path that changes write behavior.
  - Acceptance: filesystem provably unchanged (no file, no `.bak`, no
    `.tmp-*`, no new dir) after a successful preview; the preview equals
    the bytes the write would produce; unrelated server entries
    preserved; malformed collection and invalid JSON rejected in both
    modes; unsupported formats rejected; no secret in the output.
  - Verification: filesystem-unchanged test, merge-equivalence test,
    redaction test, rejection tests, existing suite,
    `go test ./...`, `go test -race ./...`, `go vet ./...`, gofmt.
  - Runtime proof: `t.TempDir()` fixtures, no network, no real config
    touched.
  - Rollback: revert the one issue-scoped commit.
  - Observed: gofmt/vet/full/race green, go.mod untouched.
- [x] T2: Document `--dry-run` in docs/clients.md. Commit 5e38666.
  - Route: inline (docs only, passive).
  - Acceptance: flag semantics, that it writes nothing, and the
    unsupported-format behavior.
  - Verification: structural readback.

## Delivery
Forecast: roughly 120-220 authored changed lines including tests.
Actual: 4 commits, ~600 changed lines, of which 458 are the new test
file. No new dependency.
Strategy: ask-on-risk, one commit per work unit, one PR. Push and PR
remain the maintainer's decision.

## Progress
Task document created before the first source write. Engram mirror
pending: this runtime exposes no Engram tools.

Verified before coding:
- `mergeServerIntoFile` ends at `writeJSONObject(path, root)` — the single
  write point. Everything above it is pure.
- `writableTarget` (230) returns ok=false for claude-code, vscode,
  opencode and pi (no `writePath`), which is exactly the
  unsupported-automatic-write set.
- `readJSONObject` (266) returns `{}` for a missing or blank file and an
  error for non-empty invalid JSON, plus the non-object-collection check
  at 255. All three behaviors must be identical in preview.
- `envWithoutToken` (421) already strips NODERED_TOKEN from the new entry.
- Existing tests: 4 `mergeServerIntoFile` tests plus backup-failure,
  no-stale-temp and file-mode tests in init_test.go, and
  `TestEnvWithoutToken_*` in init_pure_test.go. All must stay green.
- docs/clients.md:8 already mentions `init --write`; T2 extends that.
- Confirmed against the catalog: 3 clients have a `writePath`
  (claude-desktop, cursor, gemini), 4 do not. `NODERED_TOKEN` is the only
  secret-bearing key in this repo's own `examples/`.

### Parent correction (28ec939)

The writer's split of the merge silently dropped the config path from
the invalid-JSON refusal, degrading an operator-facing message from
"existing config at /home/x/.cursor/mcp.json is not valid JSON" to
"existing config is not valid JSON".

It survived review because the test that asserts the path
(`_RejectsNonObjectServerCollection`) covers the *collection* error, not
the JSON-parse error, and `_RefusesInvalidJSON` only asserted that some
error came back.

Three fixes:
1. The path is now a parameter of the seam, so the message is built in
   one place. This removed the `strings.HasPrefix` re-wrap the writer had
   added in `mergeServerIntoFile` to reassemble the old wording — which
   would have silently stopped matching if the message ever changed.
2. `readJSONObject` became a thin wrapper over `parseJSONObject` instead
   of a second copy of the same validation, so the two read paths cannot
   drift.
3. Added a path assertion to `_RefusesInvalidJSON`. Sabotage-verified:
   degrading the message fails it, restoring passes it.

