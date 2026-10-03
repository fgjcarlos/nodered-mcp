# Issue #310 — Context helper synchronization

## Objective
Synchronize set_context helper lifecycle, including creation, publication, failure rollback and backup restore invalidation, without introducing dependencies or unrelated behavior changes.

## Problem and evidence
The replaceable Server.ctxHelper pointer is accessed outside a stable lock. Concurrent provisioning and restore can race or invalidate a successful helper. Audit baseline: origin/main 086bf1a. Git tree was clean before work.

## Scope and constraints
- Fix #310 only; do not implement #311 denylist changes.
- Preserve read-only behavior, existing validation, client write guards and backup safeguards.
- Use existing Go testing framework and stdlib synchronization.
- No push, PR, or issue closure without further authorization.
- English artifacts; no AI or co-author attribution.

## Work unit
- [x] T1: Add deterministic concurrent provisioning, failure/retry and restore interaction regressions; observe RED on the base; implement stable synchronization across all helper users and observe GREEN.
  - Route: delegated direct; preparation-for-write and multiple non-trivial files require one writer.
  - Risk: high (concurrency and deployment mutations).
  - Acceptance: no shared-pointer or metadata races; at most one provisioning for simultaneous first calls; failed provisioning safely retryable; restore cannot leave a stale helper used after invalidation.
  - Verification: focused race regressions, go test ./..., go test -race ./..., go vet ./..., gofmt check, git diff --check; independent read-only verifier plus parent focused spot check.
  - Runtime proof: local httptest Node-RED fixtures; no real Node-RED connection.
  - Rollback: revert the one issue-scoped behavior/test commit.
  - Commit: 97bd2c6ff7820a46df9eb41d77aa4aa33200b489 (behavior and regressions).

## Delivery
Forecast: approximately 200–350 authored changed lines; advisory estimate, not a hard cap. Strategy: single-pr, explicitly authorized by the user after disclosure of the 721-line behavior/test diff. Commit, push and PR authorized; merge remains the maintainer decision.

## Progress
Task document created before source changes. Engram mirror pending: no Engram tools available in this runtime's tool catalog.

## Verification evidence and delivery decision
Writer observed RED and GREEN for concurrent first callers and restore deployment during an active inject. One scoped correction moved the restore lock before deployment. Final independent verification passed full tests, race tests, vet, formatting and whitespace checks. Parent repeated focused race tests successfully. Failed-restore pointer preservation is structurally verified but lacks a dedicated regression. No real Node-RED integration was run.

Actual authored changed lines: 721 (including 595 new test lines). The user explicitly authorized a single PR despite the size; behavior/test commit recorded above. Engram mirror remains pending/unavailable.

## Next step
Publish the authorized single PR linking #310 and monitor CI.
