# Issue #312 — Redact sensitive URL components consistently in logs

## Objective
Make every URL that reaches a log line or a returned error pass through the
existing `redactURL` helper, so operator-supplied credentials and sensitive
query parameters never appear in logs while host/path context survives.

## Problem and evidence
`redactURL` (internal/nodered/client.go:361) already strips query and userinfo
and is used for exactly one error (client.go:252, from #110). Seven other
surfaces emit or return URLs raw:

| Site | Kind | Leak |
|------|------|------|
| client.go:141 `nodered client created` | log | configured BaseURL |
| client.go:246 `nodered request` | log | per-request URL |
| client.go:327 `nodered raw request` | log | per-request URL |
| client.go:331 `calling GET %s` | error | per-request URL |
| config.go:262 `config loaded` | log | `cfg.NodeRedURL` |
| comms.go:193 `debug tail connected` | log | `wsURL` |
| status.go:283 `status tail connected` | log | `wsURL` |

The websocket sites matter: `commsURL` (comms.go:110) derives from the same
BaseURL and only rewrites scheme+path, so a configured
`http://host:1880/?token=…` reaches `slog.Info` intact — a query-string
credential logged at Info level, not Debug.

The `slog.Info("debug tail connected"…)` / `status tail connected` pair and
the ws dial errors (comms.go:172, status.go:260) are the sites the issue asks
to inspect; the dial errors are returned to the snapshot/operator surface and
are covered by the same rule.

Not claimed and not fixed here: the MCP listener `addr` in server.go — a
host:port of our own bind with no operator credential in it. Authorization
header values are already never logged and stay that way. No new logging
subsystem: reuse `slog` and the existing helper.

Audit baseline: origin/main 44e9e4c (#311 merged as #318). Git tree clean
before work.

## Scope and constraints
- Fix #312 only.
- Reuse `redactURL`; extend it only if userinfo/query handling is wrong
  (verified correct already — see Progress).
- No new dependency, no new logging package, no Authorization logging.
- Both package boundaries: `redactURL` lives in `internal/nodered`, but
  `internal/config` must not import `internal/nodered` (it imports nothing
  internal today). Export the helper rather than duplicate it.
- No push, PR or issue closure without authorization.
- English artifacts; no AI or co-author attribution.

## Work unit
- [x] T1: Route every URL log/error site through the shared redactor.
  - Route: delegated direct (client, comms, status, config + regressions).
  - Risk: medium — a log-only change, but it touches error strings other
    code may match on; the ws dial errors are the sensitive ones.
  - Acceptance: see issue #312 bullets; no synthetic secret appears in any
    log line; host/path retained.
  - Verification: focused log-capture regressions, `go test ./...`,
    `go test -race ./...`, `go vet ./...`, gofmt, `git diff --check`.
  - Runtime proof: local httptest fixtures; no real Node-RED.
  - Rollback: revert the one issue-scoped commit.
  - Commit: 5d9f7aa (writer) + b009a00 (parent correction, see Progress).

## Delivery
Forecast: roughly 90–180 authored changed lines; advisory estimate.
Actual: 445 changed lines across 6 files, 313 of them the new
`logredaction_test.go`. The test file is the bulk and is justified: the
acceptance asks for a regression per site, and the writer confirmed each
test fails when its wrapper is removed.
Strategy: ask-on-risk, single PR. Push and PR remain the maintainer's
decision.

## Progress
Task document created before the first source write. Engram mirror pending:
this runtime exposes no Engram tools.

Premise verified before coding: `redactURL` already handles userinfo
(`parsed.User = nil`) and the whole query (`parsed.RawQuery = ""`) and
returns `[unparseable URL]` on a parse failure, so no extension is needed —
only reuse at the seven sites. Existing coverage: TestRedactURL (client_test.go:382).
A log-capture helper already exists in this repo at
internal/mcp/http_loopback_warn_test.go:68; the nodered-package test will use
its own minimal one rather than importing across packages.

### Parent correction (b009a00)

The writer's first pass replaced both connectivity error messages with a
bare `redactedWrap(err)`, deleting `cannot reach Node-RED at %s` and
`calling GET %s`. Issue #312 asks for logs and errors that "preserve
useful destination context without secrets", so the fix removed exactly the
context the issue wants kept. b009a00 restores both wordings with the
redacted URL substituted, keeping the wrapper for the inner `*url.Error`
string. Also added the `ponytail:` ceiling marker to `redactStringURLs`.

Sabotage run confirmed the regression bites: reverting the wrapper at both
error sites makes `TestLogRedaction_GetRaw_ErrorWrapsRedactedURL` and
`TestLogRedaction_DoURL_ConnectivityErrorWrapsRedactedURL` fail with the
token visible in the rendered message; restoring makes them pass.

Design note carried to review: `redactStringURLs` is a 30-line byte scanner
over rendered error text. It exists because coder/websocket reports an
`http://` handshake URL even when the caller passed a `ws://` URL, so a
targeted string replacement never matches. Flagged to the independent
verifier as the main place where a simpler correct approach might exist.

### Independent review found one more lost prefix (eb98e03)

The independent verifier caught what I missed on the first correction:
b009a00 restored destination context in `client.go` but not in the two
WebSocket dial errors, which had the same regression. The token was
stripped there, so it was not a leak — but `connecting to <URL>` had been
dropped, the exact context issue #312 asks to preserve.

The existing regression could not see it: it asserts the token is absent,
and bare `redactedWrap(err)` already guarantees that. Sabotage confirmed
this — removing the wrapper from the ws sites leaves the ws test green.
Restoring the prefix is therefore untested by the suite as written; it was
verified against a real dial to a dead endpoint, which renders
`connecting to ws://127.0.0.1:1/comms: failed to WebSocket dial: ...`
with userinfo and `?token=` stripped. Closing that test gap is the
candidate follow-up, not part of this fix.

Full suite, race suite, vet, gofmt and whitespace all green on eb98e03.

## Next step
Await independent verification, then push the branch and open one PR
linking #312 (both remain the maintainer's decision).

