# Issue #339 — Give palette operations an appropriate MCP timeout budget

Branch: `fix/issue-339-tool-timeout-budgets`; base: `origin/main` @ cd42301;
delivery: one PR per issue (repository policy).
Runner: Go tests, vet, formatting and repository complexity check. Engram
mirror unavailable (the Engram runtime contract is unreadable in this
environment; memory dispatch was rejected).

## Objective
`install_node`, `uninstall_node`, `set_flows`, `set_flows_state` and
`restore_backup` must run with the budget their own client code asks for, not
with the 15-second default every tool gets. The MCP wrapper's deadline is the
*parent* of the client's, so a 15-second parent silently truncates a 5-minute
npm install. The install must run once (never replayed) and must be
cancellable from the client.

## Problem and evidence
**Code:** [internal/mcp/server.go](https://github.com/fgjcarlos/nodered-mcp/blob/main/internal/mcp/server.go)
`withTimeoutAttempts` / `toolTimeout`,
[internal/nodered/nodes.go](https://github.com/fgjcarlos/nodered-mcp/blob/main/internal/nodered/nodes.go)
`installTimeout`,
[internal/nodered/settings.go](https://github.com/fgjcarlos/nodered-mcp/blob/main/internal/nodered/settings.go)
`settingsStateTimeout`.

Measured on the audited tree (`main @ 93f47b1`):

- `internal/mcp/server.go:433` — `const toolTimeout = 15 * time.Second`.
- `internal/mcp/server.go:450` — every attempt is wrapped in
  `context.WithTimeout(ctx, toolTimeout)`.
- `internal/nodered/nodes.go:13` — `const installTimeout = 5 * time.Minute`,
  used by `InstallNode` (`nodes.go:58`) and `UninstallNode` (`nodes.go:80`).
- `internal/nodered/settings.go:20` — `const settingsStateTimeout = 2 *
  time.Minute`, used by `SetFlowsState` (`settings.go:67`) and `setFlows`
  (`settings.go:139`).

The client budgets are dead code on the tool path: a child context cannot
extend its parent's deadline, so `context.WithTimeout(ctx, 5m)` inside
`InstallNode` yields 15s. The same applies to `set_flows` / `set_flows_state`
and their 2-minute budget.

Five tools are affected (`install_node`, `uninstall_node`, `set_flows`,
`set_flows_state`, `restore_backup`). `search_nodes` (10s) and every read
(15s) are inside the default budget and stay as they are.

## Specs
S1. User authorization verbatim: "Implementa, y si lo recomendado en cuanto a
la decision". Decision to apply (recommended, accepted): include `set_flows`
and `set_flows_state` in this issue, because it is the same defect (a client
budget longer than the wrapper's 15s) and costs two lines in the same table.
S2. Issue acceptance verbatim: "instalación simulada superior a 15 s pero
inferior al presupuesto de instalación completa una vez, y se puede cancelar
desde el cliente."
S3. Issue correction verbatim: "presupuestos por categoría, cancelación
coherente y ninguna repetición automática de instalaciones inciertas."
S4. Issue closure validation verbatim: "Añadir una regresión que falle con el
comportamiento actual y pase con la corrección."; "Ejecutar las
comprobaciones adecuadas al alcance y registrar resultados."; "Actualizar la
documentación afectada cuando cambien garantías o límites."
S5. Issue title verbatim: "give palette operations an appropriate MCP timeout
budget".

## Tasks
T1 | S1–S5 | inline | done | per-tool budget table in the MCP wrapper; palette 5m, runtime state 2m, default 15s | commit: 26ae462
T2 | S2–S4 | inline | done | five regressions: budgets per category, slow palette call completes once inside its budget, table keys match the registry, an expired install is not replayed, a write during a long install is refused cleanly | commit: 26ae462, e3ab1ca
T3 | S4 | inline | done | docs/architecture.md, docs/tools.md, CHANGELOG.md updated | commit: 3d23a50
T4 | S4 | inline | done | gofmt, go vet, full Go suite, race suite, complexity check all pass | commit: —
T5 | S2–S4 | independent verifier + parent spot check | done | two gentle-ai-verify rounds (verdict met) plus the native RDD review, which approved the candidate with three non-blocking advisories | commit: —
T6 | S5 | inline | done | work-unit commits on the feature branch, PR to main | commit: 26ae462, e3ab1ca, 3d23a50

Slice boundaries (feature branch, one PR per issue):
- Slice A 26ae462 fix(mcp): give palette operations their own timeout budget —
  323 authored lines. The behaviour and the regressions that pin it.
- Slice B e3ab1ca test(mcp): guard the palette timeout budget against drift —
  354 authored lines. The guard regressions: table keys against the registry,
  no replay of an expired install, a queued write refused cleanly, the
  restore budget on the wire.
- Slice C 22597fe docs: describe the per-tool timeout budgets — 227 authored
  lines. The guarantees that changed, plus this record.
- Slice D (this commit) chore: record the publication evidence — this
  record's Log entry L15 and the slice note.
Total 919 authored changed lines across the four commits. The whole change
is past the ~400-line heuristic because the behaviour and its guards are one
indivisible unit: the table is three lines of code and its value is entirely
in the guarantees the tests pin. A split that separated them would leave a
commit whose tests prove nothing about the change it claims to make.

## Log
L1. User request verbatim: "Crea un plan para resolver la issue #339".
L2. Plan delivered and accepted verbatim: "Implementa, y si lo recomendado en
cuanto a la decision". The plan's decision was to include `set_flows` and
`set_flows_state`; the user accepted the recommendation.
L3. Exploration confirmed the defect and its scope: the MCP wrapper imposes a
flat 15s deadline on every attempt, and the client-side budgets (5m for the
palette, 2m for the runtime state) can never take effect because a child
context cannot extend its parent. Writes already run a single attempt since
#328, so no automatic replay exists to remove — the regression must pin it.
L4. Client cancellation is already wired by mcp-go v0.58.0: the request
context is wrapped in `context.WithCancel` and the cancel func is stored
per in-flight request (`server/request_handler.go:83-93`), and
`notifications/cancelled` calls it (`server/server.go:2331-2340`). No new
cancellation code is needed; the regression must prove the palette path
honours it.
L5. RED observed on the branch `fix/issue-339-tool-timeout-budgets` (based
on origin/main @ cd42301), before any production change:
`go test ./internal/mcp -run TestIssue339ToolBudgets -count=1` fails with
`tool "install_node" got a 14.99993794s budget, want between 4m0s and 6m0s`,
and the same for `uninstall_node` (14.99s), `set_flows` (14.99s) and
`set_flows_state` (14.99s). `create_flow` passes at the 15s default.
L6. Baseline suite on the same tree: `go test ./...` — the only failure is
the new RED regression (`internal/mcp`); `cmd/nodered-mcp`, `internal/config`,
`internal/nodered` and `internal/oauth` pass.
L7. User stop verbatim: "cerramos aqui". The implementation was not started:
no production file changed, nothing was committed and no PR was opened.
The branch holds two untracked files: `internal/mcp/timeout_budget_test.go`
(the RED regression, which fails by design until the fix lands) and this
record. The remaining work is T1–T6, in that order.
L8. User resume verbatim: "continua". The implementation resumed from T1.
The budgets live in the client package as the single source of truth
(`nodered.InstallTimeout`, `nodered.SettingsStateTimeout`, both exported
from internal/nodered), and the MCP wrapper consults a per-tool table keyed
by tool name: the palette tools get the install budget, the runtime-state
tools the settings budget, everything else the 15s default. The table is the
only place the two layers can disagree, and the client constants are the
values it points at.
L9. Regression evidence. Five tests, all GREEN after the fix:
- `TestIssue339ToolBudgets` — the budget each category gets, read off the
  deadline the handler receives. RED before the fix: all five tools got
  14.99s.
- `TestIssue339SlowPaletteInstallCompletesOnce` — an install that outlives
  the default deadline completes, exactly one POST. RED before the fix: the
  install was cut and reported an indeterminate outcome. The default
  deadline is scaled down inside the test so the simulated install stays
  inside CI time; the real numbers are pinned by the first test.
- `TestIssue339BudgetKeysAreRegisteredTools` — every key of the budget table
  names a registered tool, so a renamed tool cannot silently fall back to
  the default.
- `TestIssue339ExpiredPaletteInstallIsNotRepeated` — an install whose own
  budget expires is not replayed: one POST, one indeterminate outcome.
- `TestIssue339WriteDuringLongInstallIsRefused` — a write that arrives
  during a long install is refused cleanly ("no action was started", zero
  POSTs to the runtime) while the install still completes once.
The cancellation test (`TestIssue339ClientCancellationStopsPaletteInstall`)
is a guard on behaviour the fix must not break, not a RED for the fix: the
MCP server already cancels the request context on `notifications/cancelled`,
so it also passes on the base tree. It proves the abort reaches the runtime
and that the write is not replayed.
Sensitivity check: with `toolBudgets` emptied in a scratch copy of the tree,
the first two tests fail exactly as they did before the fix (the install is
cut at the default deadline and reports an indeterminate outcome), so they
test the fix and not the surroundings. Scratch copy removed afterwards.
L10. Two test-side findings worth recording. First, the cancellation test
failed under `-race` because it read a flag before the fake runtime wrote it;
it now waits on a channel, which is deterministic. Second, the fake Node-RED
never observed the abort until it drained the request body: the Go HTTP
server only watches the connection for a client disconnect once the body has
been read to the end (net/http/server.go, `w.reqBody.bodyRemains()`), so a
handler that ignores the body cannot see the abort. A real runtime reads the
body.
L11. Independent verification (gentle-ai-verify) returned verdict met for the
requirement coverage and no blockers, with findings the parent applied:
F1 the record overstated the tests (fixed in L9); F3 the docs overstated
what a cancelling client is told (the CHANGELOG and both doc sections now
say what actually happens per case); F4 the new heading had detached
"### Concurrent edits" from "## Retry safety" (the section was moved, and
the heading order of the pre-existing sections is unchanged); F5
restore_backup was a full deploy left on the 15s default (see L12); F6 the
budget-table comment claimed no test scaled it (the comment now names the
drift guard, and the client-side limit is stated in docs/tools.md).
L12. `restore_backup` is the same POST /flows a full deploy is, so it was
pulled into the same category: `restoreFlowsLocked` now applies
`SettingsStateTimeout` (internal/nodered/flows.go), the MCP budget table
carries `restore_backup`, and the budget test asserts it. The verifier
checked that no other tool sends `Deployment-Type: full` and is missing from
the table.
L13. Three durable regressions were added from the verifier's probes: the
budget table's keys must name registered tools (a renamed tool would fall
back to the default silently), an expired install must not be replayed, and a
write that queues behind a long install must be refused cleanly with nothing
reaching the runtime. Two more cover the restore side: a queued deployment
is refused the same way, and a slow restore completes once. A client-level
test pins the restore budget on the request the client sends.
L14. Checks on the final tree: `gofmt -l .` clean, `go vet ./...` clean,
`git diff --check` clean, `go test ./... -count=1` green,
`go test -race ./... -count=1` green, `scripts/complexity-check.sh` OK
(gocyclo from GOPATH/bin).
L15. Publication: four work-unit commits on
`fix/issue-339-tool-timeout-budgets` (26ae462 behaviour, e3ab1ca guard
regressions, 22597fe docs and this record, plus this commit), pushed and
opened as PR #374 against main. Remote CI (run 43382590827) green on every
job: check, test on ubuntu/macos/windows, race, cross-build, docker-smoke,
npm-wrapper on ubuntu/windows, release-snapshot, release-artifact-smoke on
ubuntu/windows/macos, plus GitGuardian. The issue stays open pending the
maintainer's review and merge: the PR body carries `Closes #339`, and the
merge decision is the maintainer's.
L16. Native RDD review (RDD was enabled after the PR was opened): lineage
`review-b08962a450dd6d0d`, lineage target `sha256:6f1ceb8f...`, the reviewed
tree `d386f0890e0f427a...` being exactly HEAD's tree. The R3 reliability lens
approved the candidate and the acknowledgement burned the authority
(`native-approved-acknowledgement-completed`, delivery left to ordinary
repository policy). Three non-blocking advisories, none of which opens a
correction and none of which reopens the review:
- `internal/mcp/server.go:447-453` (WARNING, informational): the palette
  budget equals the client's rather than exceeding it, so the wrapper's
  deadline is the one that fires at the boundary. Accepted: the wrapper's
  deadline is the parent, so it must not be shorter than the client's, and
  equality is what makes the client's own budget effective.
- `internal/mcp/timeout_budget_deploy_test.go:30-33` (SUGGESTION): the
  comment above the queued-deploy test could name the budgets it scales.
- `internal/mcp/timeout_budget_deploy_test.go:131-133` (SUGGESTION): the
  slow-restore test asserts the POST count and the absence of an
  indeterminate outcome, not the restored content.
Two host-relay steps were needed before the capture was accepted: the
provider's managed assets were outdated (`managed_assets_outdated`, whose
continuation named the exact `gentle-ai sync --agent pi` to run), and the
collect binding is compared canonically, so the artifact subject's schema
identifier had to be the provider's exact `gentle-ai.review-artifact-subject/v2`
rather than the URL form the public projection renders. The model run itself
was confirmed by the user before it ran.
L17. The advisories above are separate later work, not a reason to re-run
review on this candidate, and they are left unfixed here on purpose: the
review receipt stands for the tree that was reviewed, and the maintainer
decides whether to take them.
L18. The three advisories were taken in a follow-up branch
(`fix/issue-339-review-advisories`), because the user asked for a PR and the
advisories were the only outstanding work. What changed:
- R3-budget-equals-not-exceeds: the wrapper now grants the client's budget
  plus `toolBudgetHeadroom` (30s), so the client's own deadline is the one
  that binds the runtime work instead of the wrapper's. With the two equal,
  the parent's fired first and the client never got its full budget once
  the call had queued for the write lock. The new regression
  `TestIssue339WrapperBudgetExceedsTheClientBudget` pins the relationship;
  a scratch copy with the headroom removed fails it for all five tools, so
  it tests the fix rather than the comment.
- R3-queued-deploy-comment-and-timing: the comment above the queued-deploy
  test now names the budgets it scales and says which one keeps its real
  budget.
- R3-slow-restore-weak-assertion: the slow-restore test now parses the
  result and asserts that it names the backup that was restored, on top of
  the POST count and the absence of an indeterminate outcome.
The headroom changes the budget the operator observes, so docs/tools.md and
the CHANGELOG say so. Checks on the follow-up branch: `gofmt -l .` clean,
`go vet ./...` clean, `git diff --check` clean, `go test ./... -count=1`
green, `go test -race ./internal/mcp ./internal/nodered -count=1` green,
`scripts/complexity-check.sh` OK.
