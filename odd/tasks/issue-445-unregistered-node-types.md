# Issue #445 — Reject unregistered Node-RED node types
https://github.com/fgjcarlos/nodered-mcp/issues/445

## Specs

- S1: `add_node` and `update_node` with an uninstalled type return an error and deploy nothing.
- S2: The error names the type and suggests `search_nodes` or `install_node`.
- S3: `exec`, `Exec` and `exec ` are blocked the same way as `exec`.
- S4: Tests for a nonexistent type, a type with whitespace and a type with uppercase letters.
- S5: Before deploying, query `GET /nodes` (cached per process) and reject any `type` that is not a registered type, `tab`, `group`, `junction`, `subflow` or an existing `subflow:<id>`.
- S6: Normalize the type (trim) before the denylist filter and compare case-insensitively, or reject types containing whitespace.
- S7: Apply the same validation in `create_flow`, `update_flow`, `import_flow` and `set_flows`.

## Tasks

- T1 (S1–S7): Validate node types before covered writes and normalize denylist comparisons; test and document the behavior. Route: worker, independent verify, native review. Commit: `a989ba1` (implementation, tests, and docs; 431 authored diff lines). Native review approved and acknowledged (`review-ea0a497afc5d0ee1`).

## Log

- L1: Resuelve la issue #445
- L2: Reproduced unregistered type acceptance and case/whitespace denylist mismatches on the base. Focused tests, `go test ./...`, `go vet ./...`, formatting, targeted race checks, and the complexity gate pass on the candidate. The complexity gate retains the pre-existing #443 warning. Independent verification observed no deployments for invalid inputs; its missing-subflow diagnostic finding was fixed and pinned by a focused test.
- L3: Native review approved the committed candidate and acknowledged the approval. Advisory findings only; no correction transition was offered. No push or PR requested.
