# Issue #448 — connect_nodes must validate ports, inputs and outputs
Source: https://github.com/fgjcarlos/nodered-mcp/issues/448. Branch: `fix/issue-448-connect-nodes-ports`.

## Specs
- S1: "A decimal, negative or out-of-range `port` → error."
- S2: "A wire into a node without input or from a node without output → error explaining why."
- S3: "A duplicate wire neither deploys nor creates a backup." — "If the wire already exists, answer "already connected", without deploying and without a backup."
- S4: "`validate_flow` reports impossible wires already present in a flow."
- S5: "Validate `port` as a non-negative integer lower than the source node's `outputs`."
- S6: "A table of core nodes without input or without output. For the rest, use `inputs` and `outputs` from the type registry (`GET /nodes`) when available."

## Tasks
- T1 [done] S1 S2 S3 S5 S6 — `internal/nodered`: core side table (`ports.go`), endpoint and port refusal, duplicate reported as `ErrWireExists` without a write; tests.
- T2 [done] S4 — `internal/nodered`: `validate_flow` reports `impossible_wire` on the tab and flat shapes; full-deployment write paths unchanged; tests.
- T3 [done] S1 S3 — `internal/mcp`: decimal port refused, duplicate answers already connected with no deploy and no backup; tool description, docs (en/es), CHANGELOG; tests. Implementation `b2bb849`.

## Log
- L1: Resuelve la issue #448
- L2: The previous session stopped on a provider quota error before any write; this session started from a clean tree and branched from `origin/main` `93f1a1a`.
- L3: `GET /nodes` publishes no `inputs`/`outputs` (see `NodeModuleInfo`), so the registry half of S6 has nothing to read; the core table lists the types the issue names plus the core nodes whose registration (`inputs:0` or `outputs:0`) is certain: catch, status, http in, mqtt in, websocket in, mqtt out, websocket out.
- L5: RED observed: `nodered` did not build on the new symbols and `mcp` sent `POST /flows` plus a backup for a duplicate wire and passed port 1.5 through. GREEN: `go test ./internal/nodered ./internal/mcp` ok; `scripts/complexity-check.sh` OK; `ConnectNodesInFlow` went from cyclomatic 16 to 15 by extracting `wireEndpoints`. Two fixtures wired impossible pairs and were corrected: `orderedConfig` (tab `a` now also holds `k` → `k2`) and the HTTP reachability case (`dead` is a function, not a debug).
- L6: Slice boundary: the work was 489 authored lines when first committed, past the ~400-line advisory slice. The first lineage (`review-358c61065555e3b5`, three lenses, 551 lines) died on a reviewer relay timeout (956975ms against a 956973ms bound, no verdict) and was abandoned with the user's authorization; the history was then split into `628c4f8` (`internal/nodered`) and `b2bb849` (`internal/mcp` + docs), tree identical to the abandoned tip (`git diff` of 0 lines). Both were re-reviewed as separate candidates and approved.
- L8: `review-0b872dacf14b8242` approved candidate B (205 lines, risk+reliability, 25.8s and 12.3s): advisory `R3-t-error-goroutine` only (tests log from the handler goroutine; kept because the surrounding suite does the same). `review-acdcafa8230d25b4` approved candidate A (346 lines, risk+reliability, 13.7s and 22.7s): advisory `R1-hardcoded-port-tables`, `R3-idem` (the observable change is the requested one) and `R3-order` (the filter lives in `validateEffectiveConfig` only). Both acknowledged; authority burned. Delivery stays the user's decision.
- L7: Differential check against `93f1a1a` on 12 guard fixtures (`validateFlowWires`, `validateFlowWiresWithoutFunctionSyntax`, `ValidateFlow`, `ValidateFlows`): the only difference is `impossible_wire` appearing in the report for that fixture; both write guards still return nil for every fixture, so no unrequested behavior changed.
- L4: `set_flows` and `update_flow` keep accepting a configuration that already carries an impossible wire: the issue scopes the refusal to `connect_nodes` and the report to `validate_flow`. Pinned by a test.
