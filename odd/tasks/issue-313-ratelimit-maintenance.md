# Issue #313 — Bound rate-limiter maintenance cost, document proxy behavior

## Objective
Make per-request maintenance in the per-IP rate limiter O(1) amortized
instead of a full O(n) scan under the shared mutex, and document the
reverse-proxy bucket-sharing limitation plus safe deployment guidance.

## Problem and evidence
`perIPLimiter.get` (internal/mcp/http_ratelimit.go:39-57) does this on
every request for a **new** IP:

```go
l.limiters[ip] = &ipEntry{limiter: lim, last: now}
if len(l.limiters) > evictEvery {      // 1024, not a cap
    for k, e := range l.limiters {      // full O(n) scan
        if now.Sub(e.last) > limiterTTL { delete(l.limiters, k) }
    }
}
```

Three compounding problems, all confirmed by reading the code:

1. **The scan is not amortized.** It runs on every new-key insert above
   the threshold, not once per interval.
2. **The scan can delete nothing.** Entries younger than the 10-minute
   TTL survive, so a churn of many distinct IPs keeps the map large and
   re-scans it on every subsequent insert. The threshold is a trigger,
   never a bound.
3. **It holds the global mutex for the whole scan**, so it serializes
   every concurrent request, and `rate.Limiter.Allow()` for unrelated
   IPs behind it.

The issue explicitly does not claim a measured DoS. The defect is
structural: unbounded per-request work proportional to map size.

## Design chosen
Amortized sweep: a counter of inserts since the last sweep; sweep only
when both the counter and the TTL have been reached, so the amortized
cost per request is O(1) and no request pays more than O(n) once per
TTL window. Entries are still only evicted when genuinely stale, so
memory retention stays bounded by TTL × arrival rate, not by a hard cap
that would silently weaken enforcement.

Rejected alternatives, and why:
- **Hard cap on map size** (drop-oldest or reject-new above N): the
  issue calls the 1024 threshold "not a hard cap" deliberately, and a
  cap would let an attacker evict honest clients' buckets by churning
  IPs, which is a worse failure than a bounded sweep.
- **A new dependency** (ttlcache, ristretto, expirable map): the
  acceptance says avoid a new service unless necessary, and the fix is
  a counter plus a moved `if`. Not necessary.
- **Per-entry finalizer / timer per IP**: one timer goroutine per
  entry is a worse resource profile than a single sweep.

## Scope and constraints
- Fix #313 only.
- Preserve authentication ordering: the limiter stays BEFORE auth
  (server.go:735-754) and after the body cap. Do not reorder.
- Preserve `rate.Limiter` semantics, burst behavior, the 429 response
  and the `Retry-After` header.
- No new dependency; `go.mod` must not change.
- No `X-Forwarded-For` trust. Document the limitation; do not fix it.
- No push, PR or issue closure without authorization.
- English artifacts; no AI or co-author attribution.

## Work unit
- [x] T1: Amortize the eviction sweep in perIPLimiter. Commit 3644d3a.
  - Route: delegated direct (one non-trivial file + regressions).
  - Risk: medium — a lock is held and a security control is being
    changed; eviction mistakes could either leak memory or silently
    reset an offender's burst allowance.
  - Acceptance: churn inserts cost O(1) amortized; stale entries are
    still evicted; rate enforcement unchanged; concurrent access safe
    under `-race`.
  - Verification: churn benchmark-style assertion, stale eviction,
    enforcement, concurrency, `go test ./...`, `go test -race ./...`,
    `go vet ./...`, gofmt, `git diff --check`.
  - Runtime proof: in-package tests, no network.
  - Rollback: revert the one issue-scoped commit.
  - Observed: gofmt/vet/full/race all green, go.mod untouched.
    Sabotage in a throwaway worktree: reverting the gate to
    `len(limiters) > evictEvery` fails the churn test 3/3 (183-199ms
    against the 150ms threshold) and fails the stale-eviction test.
- [x] T2: Document reverse-proxy bucket sharing and deployment guidance.
  Commit 894c744.
  - Route: inline (docs only, passive).
  - Risk: passive — documentation, no executable effect.
  - Acceptance: SECURITY.md and docs/configuration.md state that
    `clientIP` uses `RemoteAddr`, so behind a reverse proxy all clients
    share one bucket; recommend proxy-side per-client limits; state
    explicitly that `X-Forwarded-For` must not be trusted blindly and
    that forwarded-address support would need an explicit
    trusted-proxy policy.
  - Verification: structural readback. Section added to SECURITY.md:113
    and a note under the rate-limit table in docs/configuration.md,
    with the anchor verified to resolve.

## Delivery
Forecast: roughly 60-140 authored changed lines (T1 including tests).
Actual: 2 commits, 162 changed lines, no new dependency.
Strategy: ask-on-risk, one commit per work unit, one PR. Push and PR
remain the maintainer's decision.

## Progress
Task document created before the first source write. Engram mirror
pending: this runtime exposes no Engram tools.

Verified before coding:
- `rateLimitByIP` is wired at server.go:751-752 and wraps the mux
  inside `maxBodyHandler`, so the ordering is body-cap -> rate limit ->
  auth. The fix must not move any of it.
- `clientIP` (http_ratelimit.go:71-79) reads `r.RemoteAddr` only. No
  header is consulted, which is why the issue calls the proxy behavior
  a documented limitation rather than a bug.
- Existing tests: `TestRunHTTP_RateLimit_BurstThen429` and a
  `clientIP` table test in http_ratelimit_test.go. Both must stay green;
  the new tests extend that file rather than replacing it.
- SECURITY.md has a "How to harden an install" section (line 57) and
  docs/configuration.md documents the four `MCP_HTTP_RATE_*` variables
  (lines 42-44). T2 documents in both places.

### Parent verification after the writer's pass

The writer reported the race test dropping 10.28s -> 0.01s, which
looked like a deleted test. Checked directly: the file went from 5 to 8
top-level tests with zero removed lines, so the speedup is the old
per-insert O(n) scan dominating wall time, not lost coverage.

Ran the sabotage myself rather than trusting the report — see T1 above.

## Next step
Await independent verification, then push the branch and open one PR
linking #313 (both remain the maintainer's decision).

