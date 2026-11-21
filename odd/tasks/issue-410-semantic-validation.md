# Issue #410 — Semantic flow validation
Source: https://github.com/fgjcarlos/nodered-mcp/issues/410
Branch: `feat/issue-410-semantic-validation`. Forecast: one work unit, ~330 authored lines; delivery strategy: `ask-on-risk`.

## Specs
- S1: “warnings-only checks in `validate_flow`”
- S2: “(a) `wires` length vs declared outputs”
- S3: “(b) change/switch property types”
- S4: “(c) http in without a reachable http response.”

## Tasks
- T1 · S1–S4 · Inline, test-first · Verified · Commit: this work unit (`feat(mcp): report advisory semantic flow warnings`).

## Log
- L1: Resuelve la issue #410
- L2: User selected “Implementar el primer paso (recomendado)”.
- L3: RED: all three warning cases failed on the original handler; the clean response test passed. GREEN: regression and structural tests, full Go suite, race detector, vet, build, tidy, complexity check and vulnerability scan passed. Aggregate coverage: 87.10% (floor: 81.20%).
