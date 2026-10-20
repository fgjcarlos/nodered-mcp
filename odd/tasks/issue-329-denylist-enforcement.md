# Issue 329 — Enforce the node denylist on every mutating surface
Branch: `fix/issue-329-denylist-enforcement`; base: `b1d48550732773343a141ad11f4bbef7e30c8c13`; delivery: single PR (one branch per issue, `CONTRIBUTING.md`); forecast ~700 authored lines.
Runner: Go tests, vet, formatting and the repository complexity check. Engram mirror unavailable (no callable memory tools in this runtime); this file is the authoritative record.

## Specs
S1. User authorization verbatim: “Resuelve la issue #329”. Issue #329: “fix: [P1][A02] enforce the node denylist across patches, imports and nested subflows”.
S2. Issue finding verbatim: “Se confirma que un nodo de tipo prohibido llega a la petición de despliegue por cuatro caminos: cambiar el `type` mediante `update_node`, importar un tab anidado mediante `import_flow`, `create_subflow`, y pasar definiciones en `subflows` dentro de un documento de flujo. El recorrido de denylist solo comprueba `nodes` y `configs` del nivel inmediato. `update_subflow` y la creación directa del helper de contexto comparten omisiones visibles por lectura, aunque no se han contado como reproducciones independientes.”
S3. Issue correction verbatim: “centralizar la política sobre la configuración efectiva que se va a desplegar, recorrer subflows, comprobar el nodo final después de aplicar parches y respetar políticas personalizadas también al crear helpers.”
S4. Issue acceptance verbatim: “pruebas de todas las superficies mutantes con las políticas por defecto y una política personalizada; el rechazo sucede antes de cualquier despliegue. `read-only` no sustituye este control cuando se habilitan escrituras.”
S5. Issue closure validation verbatim: “Añadir una regresión que falle con el comportamiento actual y pase con la corrección.”; “Ejecutar las comprobaciones adecuadas al alcance y registrar resultados.”; “Actualizar la documentación afectada cuando cambien garantías o límites.”
S6. Preserved behaviour (issue #81 / #311, not to be reopened): the check runs before the runtime is contacted and before a backup snapshot is taken; the refusal is a typed tool error naming `MCP_NODE_DENYLIST` and the denied type; the operator opt-out (`MCP_NODE_DENYLIST=`) still allows every type; `restore_backup` keeps its backup-specific wording.

## Design
The policy is evaluated on the content a call *submits*, in its **final** (post-merge) form, and the enforcement point is the layer that knows that final form: the `nodered` client, which is the only place that computes the effective document a write would deploy. The MCP layer keeps ownership of the policy *content* (`MCP_NODE_DENYLIST`) and of the operator-facing wording.

Granularity (deliberate, documented in `SECURITY.md`): a whole-document write is judged in full; a granular write is judged on the nodes it introduces or touches. A denied type already present in the runtime is not judged by a write that leaves it alone, but a write that touches it — patching it, or wiring it — is refused, because that is how a denied node gets armed.

Interface (nodered package, exact names):

    // NodePolicy reports whether a node type may be deployed.
    // It returns true for an ALLOWED type. A nil policy allows
    // every node type.
    type NodePolicy func(nodeType string) bool

    // DeniedNodeError reports that a write was refused because the
    // effective configuration carried a forbidden node type.
    type DeniedNodeError struct{ NodeType string }

    func (e *DeniedNodeError) Error() string
    func (c *Client) SetNodePolicy(policy NodePolicy)
    func (c *Client) NodeTypeAllowed(nodeType string) bool

The MCP layer installs the policy on the client it drives (`mcpserver.New`), maps `*nodered.DeniedNodeError` to the canonical operator-facing message, and keeps a pre-flight check for the `set_context` helper so a forbidden helper node type is refused before anything is provisioned.

## Tasks
T1 | S1–S4 | parent (nodered surface) | done | policy engine, recursive node-type extraction, enforcement on every write path of the client | commit: —
T2 | S1–S4 | parent (mcp surface) | done | policy installation, canonical refusal mapping, `set_context` helper pre-flight, removal of the duplicated MCP-layer walkers | commit: —
T3 | S1–S5 | parent | done | regressions: four audited paths, custom policy, mutating-surface matrix, positive controls | commit: —
T4 | S1–S6 | parent | done | full suite, vet, gofmt, complexity gate, public differential | commit: —
T5 | S1–S6 | independent verifier | done | three rounds of independent verification; each round found one real gap, all fixed and pinned | commit: —
T6 | S5 | parent | done | `SECURITY.md`, `docs/architecture.md`, `docs/tools.md`, `CHANGELOG.md`, tool descriptions | commit: —
T7 | S1 | parent | pending | work-unit commits, push, PR | commit: —

## Log
L1. User request verbatim: “Resuelve la issue #329”.
L2. Issue source: https://github.com/fgjcarlos/nodered-mcp/issues/329
L3. Exploration by reading the code, confirmed against the audit's four paths: `handleUpdateNode` (`internal/mcp/tools_flows.go`) applies an arbitrary property patch and never consults the denylist, so `{"type":"exec"}` reaches the deployment; `handleImportFlow` (`internal/mcp/clipboard.go`) normalizes the clipboard tab and calls `CreateFlow` with no denylist check; `handleCreateSubflow` / `handleUpdateSubflow` (`internal/mcp/tools_subflows.go`) send a subflow definition, whose nested `nodes` carry node types, with no check; `findDeniedNodeInFlow` (`internal/mcp/tools_shared.go`) reads only the immediate `nodes` and `configs` keys, so the `subflows` key that `flattenFlowUpdate` (`internal/nodered/flows_update.go`) flattens into the deployment is never judged. `provisionSetContextHelper` (`internal/mcp/setcontext.go`) creates a `function` and an `inject` node straight through the client, so a custom policy naming either type is bypassed. Scope decided: the policy judges the content the call submits, in final form; the enforcement point is the client, which is the only layer that computes the effective document; pre-existing runtime content is not judged, matching today's per-surface granularity.
L4. Design decided: one policy engine in the `nodered` package, one recursive walker that follows the admin API's own shapes (document with `nodes`/`configs`/`subflows`, flat `/flows` array, single node), enforcement at the entry of every client write path plus a post-merge check of the final node for `update_node`; the MCP layer installs the policy, renders the canonical refusal, and pre-checks the helper node types. No new dependency, no new tool, no change to the read surface.
L5. Red evidence: `internal/mcp/denylist_test.go` fails 10 tests against the base commit `b1d4855` (four audited paths, the nested-subflow document, the custom-policy matrix, the helper pre-flight) and passes on the branch. The first RED run also exposed a polarity bug in `deniedNodeTypeForType` (it still used the "denied=true" convention after the predicate was frozen to "allowed=true").
L6. Public differential: 34 scenarios through the tool handlers, base vs branch. 29 identical; 5 intended changes (the four audited paths plus `update_subflow`, all now refused with the canonical message). A second differential after the verifier fixes showed the same 5 differences and no new one.
L7. Independent verification (three rounds, `gentle-ai-verify`, read-only). Each round found one real gap, all fixed and pinned with a regression: (a) `instantiate_subflow` consulted the runtime before refusing a denied `subflow:<id>`; (b) the `set_context` helper refusal was untyped and did not use the canonical wording; (c) `instantiate_subflow` merged the caller's params into the instance node and judged them only after the subflow lookup; (d) the patched node was judged by its top-level type only, so a member collection carried by the patch escaped; (e) `connect_nodes` could wire a forbidden node. One deliberate consequence was accepted and pinned: refusing before the runtime is contacted moves the malformed-instance-payload error ahead of the "unknown subflow" error.
L9. Delivery: one branch and one PR for the issue, as `CONTRIBUTING.md` asks. The branch carries eight reviewable commits (engine, engine tests, client enforcement, MCP wiring, the four audited paths, the surface matrix, the refusal contract, the documentation) but totals ~1.8k authored lines, above the ~400-line slice the repository's commit hook warns about. The slice boundary is recorded here rather than split into chained PRs: the engine without its enforcement, or the enforcement without its regressions, is not reviewable on its own, and the whole feature is one security control that must land together. The alternative — two chained PRs — was used for #328 because that issue needed two independent slices; #329 does not.
L10. Known limits, deliberately not changed: a forbidden type already in the runtime can still be armed by other means (the policy governs what MCP introduces and arms); denying a structural type such as `tab` does not block a nested document that carries no `type` key, because the policy ignores absent keys rather than guessing.
