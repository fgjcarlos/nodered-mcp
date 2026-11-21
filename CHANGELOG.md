# Changelog

All notable changes to this project are documented here. The format
follows [Keep a Changelog 1.1](https://keepachangelog.com/en/1.1.0/);
this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `import_flow` names each `http in` route (method + url) of the copy that an
  existing node already serves, with that node and its tab, and reminds that
  deleting the copy leaves the original route unregistered until that tab is
  redeployed. The import still happens (#409).

### Fixed

- Drop the npm workflow step that re-deprecated `@fgjcarlos/nodered-mcp@0.6.2`
  on every release. The deprecation has been in place since August, but the
  step re-sent it each time and failed the v0.8.0 `publish-main` job when the
  registry answered `E422`, which skipped the `latest` promotion.
- `nodered-mcp init` now tells Pi users to paste into
  `$PI_CODING_AGENT_DIR/mcp.json` when that variable is set, instead of the
  hardcoded `~/.pi/agent/mcp.json` that Pi does not read under the override
  (#397).
- Keep the answers in the `init --dry-run` snippet for clients without a
  writable config target (Claude Code, VS Code, OpenCode, Pi). The preview
  rendered the snippet from empty values, so it printed an empty
  `NODERED_URL` and no `NODERED_BACKUP_DIR`; it now prints the same snippet a
  plain `init` prints for the same answers, still without the token (#394).
- Report the Docker image version without the tag's leading `v`: the image
  printed `v0.8.0` while the goreleaser binaries and the npm package report
  `0.8.0`. The Dockerfile now strips a single leading `v` from `VERSION`
  (#395).
- Wait long enough for npmjs to serve a fresh publish. The post-publish
  registry reads gave up after ~45 s, while npmjs took about 6 minutes to
  serve the last v0.8.0 package; `scripts/wait-for-registry.sh` now retries
  26 times with sleeps capped at 30 s, about 10 minutes per package (#396).
- Recommend `MCP_DEBUG_STREAM=true` instead of `MCP_DEBUG_STREAM=on` in the
  startup warning, the `get_debug_messages` and `get_node_status` errors, the
  capability guidance and the `get_node_status` description. The config parser
  accepts boolean values only, so every hint led to a server that refused to
  start with `strconv.ParseBool: parsing "on": invalid syntax` (#402).
- The `search_nodes` description no longer advertises a
  `NODERED_SEARCH_BASE_URL` variable that nothing reads; it names the public
  npm registry (`registry.npmjs.org`) the search always calls. A test now
  fails when a tool names an env var `config.go` does not read (#403)
- `get_runtime_info` no longer reports `get_flows_state` as `setting_disabled`
  when `settings.runtimeState.enabled` is off: Node-RED mounts
  `GET /flows/state` unconditionally and gates only `POST /flows/state`
  (`set_flows_state`). The `get_flows_state` 404 hint no longer tells the
  operator to enable `runtimeState` (#406).
- Stop promising a local-log fallback for `get_runtime_logs`. The
  `get_runtime_info` guidance said the tool "will fall back to the local log
  file under ~/.node-red/", and the 404 error named a `~/.node-red/*.log` file,
  but the tool only calls `GET /logs` and stock Node-RED writes no such file.
  Both now point at the runtime's own output: stdout (journalctl, docker logs)
  or the file a logging handler writes if one is configured (#407).
- Refuse a subflow definition whose `out` port wire has a missing or
  non-integer `port`, or a `port` past its target's outputs (or, on a
  pass-through, past the `in` ports). The runtime indexes the wires by that
  port when an instance starts, so the output was dropped silently or the
  start threw after the deploy had answered 204. `create_subflow`,
  `update_subflow`, `set_flows`, `restore_backup` and `validate_flow` now
  report it as `invalid_subflow_port` before any backup or write (#408)

## [0.8.0] - 2026-11-20

### Added

- feat(init): add `--dry-run` to preview the merged config (#314, PR #321). A
  plain `init` prints a freshly generated snippet, not the merge, so an
  operator rerunning `--write` against a config that already holds other MCP
  servers had no way to verify the real result without writing it.
  `init --dry-run` performs the real merge in memory, redacts the
  secret-bearing values and prints the exact bytes the write would produce: it
  creates no file, no parent directory, no `.bak` and no temporary file, and
  changes no permissions. The merge was split at its single write
  (`mergeServer` is pure, `mergeServerIntoFile` writes), so the preview and
  the commit share one computation by construction.
- feat(mcp): refuse known-incompatible `set_context` and `inject_node` payload
  calls (#316, PR #323). The version gate for `set_context` and for
  `inject_node` with a payload called the version probe from inside the
  request handler, and that probe fires a real `GET /settings`: an
  argument-rejection path contacted the runtime and raced the handler's own
  writes. The client now exposes a non-probing cached accessor
  (`CachedNodeRedVersion`) and a `probed` flag that tells a cold cache from a
  probe that ran and found no version, so a call the runtime cannot serve is
  refused before any side effect, naming the detected and the required
  version, while a runtime whose version could not be detected still runs and
  says the call was not verified.

### Changed

Behaviour a client or an operator may depend on. Each item is detailed
under Fixed.

- **Breaking:** `export_flow` returns the editor's flat clipboard array —
  one `tab` element followed by its nodes and the config nodes and subflow
  definitions they use — instead of `[{tab, nodes}]` (#337). Code that
  parses the old shape must be updated. `import_flow` takes the flat
  clipboard and still accepts the legacy nested shape.
- **Docker:** the image's working directory and default
  `NODERED_BACKUP_DIR` are now `/var/lib/nodered-mcp` and
  `/var/lib/nodered-mcp/backups` (#346). A volume mounted at the old
  `/home/nonroot/backups` no longer receives the snapshots; mount it at
  the new path.
- Deployments are validated as a whole before they reach the runtime:
  `set_flows`, `restore_backup` and the subflow writes refuse a
  configuration with dangling wires or missing subflow definitions, among
  other structural problems; `set_flows` and `restore_backup` take
  `force: true` as the explicit override (#334).
- New refusals: `delete_subflow` refuses a subflow that is still in use
  (#332), `inject_node` refuses a payload on a node that is not an inject
  node (#338), and `set_context` and `inject_node` with a payload refuse a
  Node-RED version known not to support them (#316).
- Mutating tools are no longer retried after an ambiguous transport
  failure; only read tools retry (#328).
- `validate_flow` reports the new issue kinds `missing_type` and
  `orphan_node` (#336); `get_runtime_info` reports the new capability
  states `stream_disconnected` and `stream_unavailable` with a reason and a
  remedy (#315, #341).
- `init` returns an error when no supported client is detected (#302) and
  `init --write` refuses a config whose MCP server collection is not an
  object instead of replacing it (#303).

### Fixed

- Rewrite only real references when `import_flow` remints ids (#371,
  PR #390). Any string value equal to a reminted id was rewritten, so free
  text that named an old id — a node name, a payload, a function body — was
  silently changed when a clipboard with short hand-written ids was imported
  into the instance it came from. The rewrite now covers the structural
  fields (`id`, `wires`, `links`, `scope`, `g`, a group's `nodes`) plus the
  properties whose value is the id of a reminted config node, which keeps
  config references such as an mqtt node's broker wired.
- Close three `import_flow` clipboard partition edge cases (#370). A node
  with canvas coordinates but no `z` was filed as a global config node,
  and dropped without a word when its id already existed on the
  instance; it is now refused with a message that names it. A global
  config node was reused whenever the instance had any element with its
  id, so the imported nodes could bind to an object of another type or
  to another tab's config node; it is now reused only when the instance
  has it as a global config node of the same type, and otherwise travels
  with the tab under a reminted id. The denylist pre-check keeps judging
  every element of the clipboard, including the members of a subflow
  definition and the global config nodes the instance already has, and
  this is now documented as the intended policy.
- Gate every publication channel on the test suite of the tagged commit
  (#349). The CI workflow only ran for `main` and pull requests, so a
  tag push produced no test run at all: the release workflow published
  the GitHub Release from its own artifact checks, the Docker workflow
  pushed the image from a single unguarded job, and a tag whose commit
  had never passed the functional, race or vet checks could still
  publish one of the channels. The CI workflow is now callable as a
  reusable workflow and also runs for tags, and both publishing
  workflows call it as a gate that every publishing job depends on, so
  a failing test publishes nothing. The npm channel publishes the
  exact commit the gated release run built and verifies that the tag
  resolves to it. The moving `latest` reference — the GitHub "Latest
  release" badge, the `:latest` image tag and the `latest` npm
  dist-tag — now follows one rule in all three channels: only a stable
  tag that is the newest stable version takes it, so a prerelease or a
  re-run of an older tag publishes its exact version without taking
  `latest` away from a newer stable release.
- Bound and coalesce the JWKS refreshes for unknown key IDs, so an
  invalid token cannot make the server download the key set once per
  request (#348). The `kid` travels in an unverified token header and
  the requester chooses it, but every miss forced a fresh download of
  the whole document, and simultaneous misses each fetched their own
  copy. The refreshes are now serialised (the callers that miss at the
  same time share one download) and a miss-driven refresh happens at
  most once per minute, while a rotation is still picked up as soon as
  the window opens. A failed attempt is shared with the callers that
  were waiting for it, so an unreachable identity provider does not
  turn into a queue of downloads.
- Make the mutation lock waits cancelable, so a write whose context is
  done gives up instead of waiting past its deadline (#347). The client
  write lock was a plain `sync.Mutex`: a write that arrived while another
  write held the guard stayed parked on `Lock()` until the holder
  finished, so a canceled request waited past the timeout it was
  promised and the write queue outlived the timeout. The wait is now
  bound to the caller's context, the refusal ("waiting for write lock:
  ...; no action was started") reaches the caller as a refusal instead of
  an indeterminate outcome, and the context-aware mutex moved to
  `internal/lock` so the server's context helper lock and the client
  write lock share one implementation.
- Give the container image an explicit, persistent backup directory, and
  prove the snapshot survives a container recreation (#346). The runtime
  stage inherited its working directory from the distroless base
  (`/home/nonroot`), so the `backups` default landed in the container's
  writable layer: the snapshots were lost when the container was
  recreated, and no Docker example mounted a volume to keep them. The
  image now sets a working directory it owns and points
  `NODERED_BACKUP_DIR` at `/var/lib/nodered-mcp/backups`, the Docker
  examples mount a volume there, and the smoke test performs a real
  deployment and checks that the snapshot is still there after the
  container is recreated.
- Build a granular edit on the snapshot it deploys (#345). A granular
  edit read the nested tab, applied the patch to that document and then
  read the whole configuration again for the array to deploy and the
  revision to deploy it with. Two reads, two snapshots: a change another
  editor made in between was written back with the old value while the
  deployment carried the new revision, so the runtime had no reason to
  refuse it. The edit is now built on the snapshot it deploys — the base
  of the edit is the tab as the flat array holds it, the array to deploy
  is that same array, and the revision is that same response's revision.
  The window left is the runtime's own: a deployment that lands after the
  snapshot makes the revision stale and the runtime answers 409, which the
  client does not work around. Building the base from the stored
  configuration also preserves what the runtime's nested reconstruction
  used to lose: the tab's `env`, `info` and unknown fields, the
  configuration nodes, the order of the entries and the wires of a link
  out node. Those wires are the runtime's to recompute, so the exception
  moved into the shared rule engine, where every write path inherits it.
- Keep reading the npm registry until it shows the expected value
  (#344). `scripts/wait-for-registry.sh` accepted the first non-empty
  stdout, but right after `npm dist-tag add` the registry can answer
  with the version the tag pointed at before the promotion — a value
  that is non-empty, so the helper reported success and the release
  workflow's own comparison failed without a retry. The helper now
  takes `--expect VALUE` and retries a stale value like an empty one,
  and it reports the command's real error instead of discarding its
  stderr.
- Decide the "at least one tab" rule of a deployment structurally, and
  pin it with regressions (#343). The rule decodes the `type` of every
  entry instead of searching the payload for the bytes `"type":"tab"`,
  so the same document behaves the same whether it arrives compact,
  indented or as objects, and a tab mentioned inside a property is not
  a tab.
- Redact the sensitive parts of the Node-RED URL from the HTTP errors
  (#342). `getRaw` recorded the whole joined URL in `APIError.Path`, so
  a 404 on `/flow/global` carried the base URL's query string — and
  with it any token the operator had embedded there — into the error,
  the logs and the MCP client. The error now names the request path
  only (`nodered GET /flow/global: HTTP 404`), like the JSON endpoints
  already did, and a configuration error (an unparsable base URL, a
  URL that could not be joined, a request that could not be built)
  names the reason it failed on instead of the URL it failed on. The
  body of an error is still the one Node-RED returned.
- Report the debug and node-status stream capabilities from what the
  MCP observed, instead of a constant (#341). `get_node_status` and
  `get_debug_messages` were classified as `stream_disabled` no matter
  what, even with `MCP_DEBUG_STREAM=on` and the `/comms` WebSocket
  connected — the guide then told an operator to enable a flag they
  had already enabled. The matrix now combines the opt-in (the
  operator's configuration) with the observed connection: `ok` when the
  tail is connected, `stream_disabled` when the opt-in is off,
  `stream_disconnected` when the dial failed, `stream_unavailable`
  when no `/comms` endpoint could be derived, and `unknown` while the
  tail has not reported a connection yet. An enabled flag alone is
  never reported as `ok`.
- Detect the Node-RED version against a local runtime, and recover
  from a failed probe (#340). The version probe was skipped for every
  loopback URL, which also skipped the shipped default
  (`http://localhost:1880`): a real local Node-RED was never probed,
  so the runtime version gate stayed inactive until some other
  operation happened to warm the cache, and `set_context` reported an
  unknown version on a runtime it could have checked. A fixture is
  now an explicit option instead of a URL shape, the gate resolves the
  version before the operation that depends on it (refusing an
  incompatible runtime without side effects), and a failed probe is
  retried instead of being memoized for the whole session.
- Give the palette and runtime-state tools their own timeout budget
  (#339). Every tool attempt ran under a flat 15-second deadline, and
  the client asks for more on the slow paths: an npm install needs
  five minutes, a full deploy or a runtime restart two. The deadline
  the wrapper sets is the parent of the client's, and a child context
  cannot extend its parent, so those budgets never took effect — an
  `install_node` was cut at 15 seconds and reported an indeterminate
  outcome while the runtime kept installing. `install_node`,
  `uninstall_node`, `set_flows`, `set_flows_state` and `restore_backup`
  now run under their own budget; every other tool keeps the 15
  seconds. A cancelled call aborts the request to the runtime and is
  never replayed.
- Give the palette and deployment budgets an allowance over the
  client's own, so the runtime work gets its full budget instead of
  losing the time the call spent waiting for the write lock (#339).
  With the two deadlines equal the wrapper's fired first and the
  client's own budget never bound the work; this is what the R3
  reliability review asked for.
- Refuse `inject_node` with a `payload` on a node that is not of type
  `inject` (#338). The payload path skipped the type check, so a call on
  a comment node sent `POST /inject/:id` and reported "fired with
  payload" while nothing fired. Both paths now return the same typed
  error and send no fire request.
- Speak the real editor clipboard format in `export_flow` and
  `import_flow` (#337). Both assumed a single-element array holding a
  nested tab, so a real editor export (the tab and its nodes as separate
  elements) was refused as "clipboard has N tabs", and `export_flow`
  produced a document the editor does not export. `export_flow` now
  returns the flat array — the tab, its nodes, and the global config
  nodes and subflow definitions they use — and `import_flow` counts tabs
  by `type`. Importing a copy into the same instance used to fail on
  duplicate ids; when any node id already exists, every node id of the
  copy is now reminted and its wires and references rewritten. Global
  config nodes and subflows the instance has are reused; a missing
  subflow is refused with the step to take. The nested shape earlier
  releases exported is still accepted.
- Correct the `import_flow` and `export_flow` messages the flat-clipboard
  change left inaccurate (#369). The reminted note reported every reminted
  id as an existing one; it now says how many of the copy's ids collided
  and how many were reminted ("1 of the copy's 3 node ids already
  existed on this instance, so all 3 were reminted ..."). A nested
  clipboard with several tabs is refused as "clipboard has N tabs" again
  instead of "clipboard has no tab", and a failed read in `export_flow`
  keeps the released `reading flow:` prefix.

- Validate a flat flow array as a whole in `validate_flow` (#336). The
  validator rebuilt one document per tab, so an id shared by two tabs, a
  node outside every tab and the nodes of a subflow definition were never
  checked, and a node without a `type` passed everywhere. `validate_flow`
  now reports `duplicate_id` across the whole array, `orphan_node` for a
  node or group whose `z` names no tab or subflow (with its dangling wires),
  and a new `missing_type` issue. The full deployment check (`set_flows`,
  backup restore) and the tab write guard (`create_flow`, `update_flow`)
  refuse a node without a type as well; a type the runtime coerces into a
  name, such as `["inject"]`, is still accepted.
- Restore Streamable HTTP streaming on the HTTP transport (#335). The
  request logging wrapper hid `http.Flusher` from mcp-go, so a `GET /mcp`
  listening stream was answered with `405 Streaming unsupported` and SSE
  responses were unavailable. The wrapper now forwards `Flush` and exposes
  `Unwrap` for `http.ResponseController`.

- Validate the effective configuration before a deployment (#334). A full
  deployment (`set_flows`) and a restore (`restore_backup`) installed
  whatever they were given, and the subflow writes did not look at the
  topology of the definition at all. Measured against a real Node-RED, a
  deploy with `wires:[["missing"]]` answers 204 and the client reports
  success, and a deploy that instances a subflow whose definition is
  missing leaves the runtime stopped, waiting for a node type that will
  never be registered. The deployment paths now check the configuration as
  a whole before anything is sent to the runtime — no request, no backup —
  and refuse a wire that names no node in the same tab or subflow, a node
  whose `z` names neither a tab nor a subflow definition, two entries with
  the same id, an instance of a subflow whose definition is not in the
  configuration, and a subflow port that names a node which is not in the
  definition.
- Refuse a null flow document instead of panicking on it (#333).
  `json.Unmarshal("null", &map)` returns no error and leaves the map nil,
  so the next write into that map panicked with "assignment to entry in nil
  map". Three tool paths reached it: `create_flow` and `import_flow`, which
  normalize the document with the fill enabled, and `inject_node`, which
  writes the marker into the payload map. A flow document is now refused,
  because null is not a flow document. An inject payload is wrapped, because
  every value that is not a JSON object is already wrapped under "payload",
  so the caller's value still reaches the inject's `msg`. Neither transport
  lets the process die: over HTTP the repository's own `recoverPanics`
  wrapper turned the panic into a 500, and over stdio mcp-go recovered it
  into a JSON-RPC -32603.
- Refuse deleting a subflow definition that is still in use (#332). The
  runtime does not check that no instance of a subflow is in use before the
  definition is removed: deleting a definition that a flow tab still
  instances leaves the instance node pointing at a missing type, the next
  deploy logs "Waiting for missing types to be registered" and the affected
  flows never deploy. `delete_subflow` now scans the runtime for instance
  nodes of type `subflow:<id>` — in flow tabs and nested inside another
  subflow definition — and refuses the delete with the list of references
  to remove first. The check runs before the backup and the write, so a
  refused delete leaves the runtime untouched. Cascading deletion stays out
  of scope: removing the instances along with the definition is a
  destructive operation the operator has to ask for explicitly.
- Recover `set_context` helpers across restarts (#331). The helper was a
  process-local cache, so the first call after a restart had to provision
  a replacement. Each call now reads the active flow configuration and
  verifies an isolated helper's executable code, node configuration,
  ownership and wiring before reusing it, so a helper that survives the
  process is discovered and reused instead of replaced. When no supported
  helper exists, the server creates the complete tab in one guarded
  `POST /flow`, with node ids that do not collide with the existing
  configuration and no intermediate empty-tab deployments. A failed or
  ambiguous response is returned without automatic replay, and a later
  explicit call discovers the helper the runtime actually installed.
- Clarify the structural backup recovery limits (#330). The backup is a
  structural snapshot of `GET /flows`, not a Node-RED disaster-recovery
  backup: it does not cover the separate encrypted credential store or the
  keys and configuration needed to decrypt it, and restoring a snapshot can
  itself remove nodes and their credentials. The write tools that change
  the flow configuration now advertise those limits before execution, and
  `restore_backup` repeats them in its successful response, so the
  recovery an operator can rely on is stated where the tool is called
  instead of only in the documentation. The Go toolchain was updated for
  the crypto/x509 security patch (the module now requires Go 1.26.11).

- Enforce the node denylist on every mutating surface (#329).
  `MCP_NODE_DENYLIST` is now judged on the effective configuration a write
  would deploy, and the document is walked recursively, so a forbidden type
  nested inside a subflow definition is refused. `update_node` is judged on
  the node as it is after the patch is merged, which closes the path where a
  patch changed a node's `type` to a forbidden value; `connect_nodes` is
  judged on the nodes it wires. `import_flow`, `create_subflow`,
  `update_subflow` and `instantiate_subflow` are covered too, and the
  server's own `set_context` helper obeys a custom policy. The refusal names
  `MCP_NODE_DENYLIST` and the denied type, and happens before the runtime is
  contacted and before any backup snapshot is taken — except for the two
  checks that cannot know the effective content without reading the runtime
  first (the merged node, and the nodes a wire touches), which refuse after
  a read but never after a deployment.

- Do not replay mutating MCP tools after ambiguous transport failures (#328).
  Writes and actions run once and report an indeterminate outcome with guidance
  to inspect the runtime before repeating. Read tools retain up to three
  attempts; transport classification uses typed errors, not message substrings.

- Preserve editor tab order when updating a flow or editing its nodes (#324).
  Updates now back up the revisioned configuration and deploy in place using
  Admin API v2 with deployment type `flows`. Unrelated configuration and omitted
  metadata are retained; revision conflicts are returned without retrying.

- fix(mcp): make the `get_runtime_info` capability states actionable (#315, PR
  #322). A degraded tool was rendered as a bare string such as
  `version_too_low`, so the operator had to know out of band that the remedy
  is to upgrade Node-RED. The facts needed to make each entry actionable were
  already on the runtime probe the matrix consumed, so the change is a
  presentation over existing signals: a new `capabilityGuidance` adds a reason
  and a remedy to every non-`ok` entry, keeps `unknown` explicit with an
  empty remedy, and leaves the absence of a guidance entry as the signal that
  the tool works.
- fix(mcp): amortize the per-IP rate limiter eviction sweep (#313, PR #320).
  The limiter scanned the whole map on every new-key insert once it exceeded
  1024 entries, holding the global mutex for the entire pass: O(n) per request
  under churn, and with every entry younger than the 10-minute TTL the scan
  deleted nothing. The pass is now gated on two conditions -- the inserts
  since the last sweep reached the threshold, and one TTL window elapsed since
  it -- so an insert stays O(1) under the lock and a single O(n) pass runs at
  most once per TTL window. The map is still uncapped on purpose: a hard cap
  would let an attacker churn IPs to evict honest clients' buckets. The
  behaviour behind a reverse proxy is now documented too, including why
  `X-Forwarded-For` is ignored on purpose.
- fix(nodered): route every logged URL through `RedactURL` (#312, PR #319).
  Every URL that reaches a log line or a returned error now passes through the
  existing redaction helper, so a query-string token or the userinfo of
  `NODERED_URL` never lands in operator-visible output. The internal callers
  were migrated to the exported `RedactURL`, and the error wrap sites use a
  small `redactedErr` type whose `Error` scrubs every URL-shaped substring
  while preserving the wrapped error.
- fix(mcp): enforce the node denylist when restoring flow backups (#311, PR
  #318). `restore_backup` deploys the whole flow configuration but never ran
  the `MCP_NODE_DENYLIST` check the other write tools run, so a backup saved
  before a policy change could reintroduce a now-prohibited node type and
  reopen the RCE path issue #81 closed. The guard reuses the shared walkers
  and runs on the same envelope the client deploys with, fires before the
  snapshot and before `POST /flows`, and names the denied type.
- fix(mcp): synchronize the context helper lifecycle (#310, PR #317). The
  `set_context` helper was provisioned, published, rolled back and injected
  without a single owner of that lifecycle, so a concurrent first caller could
  provision a duplicate tab and a restore could invalidate the helper under an
  in-flight call. One server-owned mutex now covers provisioning, publication,
  rollback and injection, and a backup restore is serialized against the
  invalidation. Regressions cover concurrent first callers, a provisioning
  failure and retry, restore exclusion and reprovisioning.
- fix(init): refuse invalid MCP server collections (#303, PR #306). `init
  --write` could replace an existing MCP server collection that is not an
  object, destroying a configuration it does not understand. The refusal
  happens before the backup and before the write, so the original config is
  preserved.
- fix(init): make the client discovery evidence-based (#302, PR #309). A GUI
  client is now detected from the platform's own application and config
  evidence and a CLI client from an executable on `PATH`; OpenCode and Pi
  joined the supported catalog, and the catalog, the documentation and the
  examples are checked for parity. The clients are detected before the setup
  prompts, and a clear error is returned when none is found.
- fix(init): preserve the invoked symlink path (#116, PR #307). The generated
  config used `os.Executable()`, which resolves a symlink, so a
  package-manager upgrade that repointed the binary changed the command in the
  config. The invoked path is used instead, with a fallback to `nodered-mcp`
  and a documented `PATH` requirement when it cannot be resolved.

- fix(npm): retry registry reads after `npm publish` and `npm
  dist-tag add` to tolerate npmjs propagation delay (#298). The
  v0.7.2 release required two manual reruns because the verifier
  steps treated E404 and stale `latest` reads as fatal failures.
  The retry helper (`scripts/wait-for-registry.sh`) now backs
  every post-write `npm view` in `.github/workflows/npm.yml`
  (five attempts, linear backoff, ~45 s budget) and the workflow
  contract test (`bin/npm_workflow_order_test.js`) enforces that
  each verifier step routes through it.

### Security

Summary of the security-relevant fixes; each is detailed under Fixed.

- The node denylist (`MCP_NODE_DENYLIST`) is enforced on every surface that
  can deploy a node type, including `restore_backup` and `import_flow`,
  which could otherwise deploy an `exec` node (#311, #329).
- Credentials and tokens are redacted from every logged URL and from HTTP
  error messages (#312, #342).
- JWKS refreshes for unknown key ids are bounded and coalesced, so an
  invalid token with an invented `kid` can no longer make the server
  download the key set once per request (#348).
- The per-IP rate limiter eviction sweep is amortized (#313).
- The Go toolchain moved to 1.26.11 for the crypto/x509 security patch
  (#330), and `golang.org/x/text` to 0.41.0 for GO-2026-6629 (#351).

### Dependencies

- chore(deps): bump the actions group: `docker/setup-qemu-action` 4.2.0 -> 4.3.0
  and `docker/setup-buildx-action` 4.2.0 -> 4.3.0 (PR #301);
  `docker/setup-qemu-action` -> 4.4.0, `docker/setup-buildx-action` -> 4.4.1
  and `docker/build-push-action` 7.3.0 -> 7.4.0 (PR #305);
  `actions/setup-node` 7.0.0 -> 7.1.0, `goreleaser/goreleaser-action` 7.2.3 ->
  7.3.0, `actions/upload-artifact` 7.0.1 -> 7.0.2 and
  `actions/download-artifact` 8.0.1 -> 8.0.2 (PR #326); `docker/login-action`
  4.6.0 -> 4.7.0 (PR #360) and 4.7.0 -> 4.8.0 (PR #365).
- chore(deps): bump `golang.org/x/time` 0.15.0 -> 0.16.0 (PR #304).
- chore(deps): bump `golang.org/x/text` 0.40.0 -> 0.41.0 (#351). The audit
  advisory GO-2026-6629 (panic in `x/text/secure/precis`) is fixed in v0.41.0;
  `x/text` stays an indirect dependency and `govulncheck` still reports zero
  reachable vulnerabilities.

## [0.7.2] - 2026-08-24

### Fixed

- fix(init): detect Claude Desktop before its config exists (#289).
- fix(release): pin the release Go toolchain (#293).
- fix(release): serialize the npm package promotion (#294), so the main
  package and the platform packages are never promoted at the same time.
- chore(release): pin and verify the release toolchain (#295).
- chore(release): bump the package metadata to v0.7.2 (#297).

### Changed

- docs(npm): expand the platform documentation for the npm install
  matrix (#275).
- ci: remove the publish-main retry workflow once the E403 root cause was
  identified (#281), and the one-off publish-main retry it was replaced by.

### Dependencies

- chore(deps): bump the actions group (docker/login-action, download-artifact,
  upload-artifact, goreleaser-action, checkout) and github.com/mark3labs/mcp-go
  (PRs #282-#287).

## [0.7.1] - 2026-08-06

### Fixed

- fix(update): accept the platform package as the npm channel neighbour
  (#276) (#277).
- fix(npm): publish the main package with `NPM_SECRET` while Trusted
  Publishing is disabled, bump the version to 0.7.1 and scope the guard
  (#279).

## [0.7.0] - 2026-08-05

### Added

- feat(npm): ship native binaries through npm-native platform
  packages and remove the postinstall downloader (#257). The main
  `@fgjcarlos/nodered-mcp` package now lists six scoped platform
  packages as exact-version `optionalDependencies`, and npm's own
  `os`/`cpu` filter selects the right one at install time. The
  `bin/nodered-mcp.js` wrapper resolves the matching platform
  package from `node_modules` and re-execs its native executable
  with inherited stdio, argv, signals and environment. Registry
  integrity replaces the legacy checksum-verified GitHub
  downloader, so a future GitHub Release deletion no longer
  breaks installation of an already-published npm version.

### Removed

- `bin/install-impl.js`, `bin/tar.js`, `bin/install-impl_test.js`,
  `bin/tar_test.js`, `bin/install_message_test.js` — retired with
  the postinstall downloader; no supported code path consumes them.
- The `postinstall` lifecycle script from the main package's
  `package.json`. `npm install -g --ignore-scripts` now works.

### Changed

- `bin/nodered-mcp.js` rewritten as a platform resolver with
  distinct actionable errors for unsupported OS/architecture,
  missing optional package, non-executable native binary, and
  child-process startup failure.
- `scripts/release-gate.js` derives its required-asset list from
  the same goreleaser `name_template` that
  `scripts/build-platform-packages.js` consumes, keeping the
  release gate and the npm publisher aligned.
- `.github/workflows/npm.yml` reordered into the eight-step
  publication ordering the issue mandates: build → validate →
  assemble platform packages → publish six natives → verify
  registry → publish main. Idempotent: a re-run of an
  already-published exact version is a no-op, never a partial
  release.
- README.md and README.es.md updated to describe the new channel
  and drop the legacy timeout/retry env-var documentation.

### Added (test infra)

- `scripts/build-platform-packages.js` (producer) and
  `bin/build-platform-packages_test.js` (in-memory suite) cover
  the assemble-and-publish path with synthetic goreleaser
  archives, exercising both the flat and the wrapped layouts.
- The real-artifact gate from #258 builds and inspects all six
  GoReleaser archives, assembles the corresponding npm packages
  with `npm pack`, installs the packed main/native pair with
  lifecycle scripts disabled, compares native binary digests and
  launches the CLI on Linux, Windows and macOS before publication.

## [0.6.3] - 2026-08-05

### Fixed

- fix(npm): restore installs from real GoReleaser archives in
  v0.6.3 (#256). Goreleaser v2 defaults `archives.wrap_in_directory`
  to `false`, so the same `name_template` that historically produced
  `nodered-mcp_<os>_<arch>/nodered-mcp[.exe]` now produces a flat
  archive with the binary at the archive root. The 0.6.2 postinstall
  searched inside the wrapped subdir and 404-promoted nothing on
  every supported platform. 0.6.3 detects both layouts, so a future
  `wrap_in_directory` flip fails safely instead of silently breaking
  npm installs.

- fix(npm): replace the fixed 30s binary download timeout with a
  120s default (#256). The previous ceiling was tight enough that
  otherwise-valid installs on slow, mobile, proxied or VPN
  connections died mid-download. Timeouts and retry count are now
  overridable per-run via `NODERED_MCP_DOWNLOAD_TIMEOUT_MS`,
  `NODERED_MCP_CHECKSUMS_TIMEOUT_MS` and
  `NODERED_MCP_DOWNLOAD_RETRIES`; non-positive-integer values fall
  back to the documented defaults so a typo cannot silently disable
  the ceiling. Retries are bounded with exponential backoff and
  cover transport only — checksum mismatch still fails closed and is
  never retried as a transient.

### Deprecated

- @fgjcarlos/nodered-mcp@0.6.2 is now marked deprecated on the npm
  registry. The tarball remains installable for users with version
  pins, but `npm install` surfaces a one-line warning pointing at
  0.6.3.

## [0.6.2] - 2026-08-05

### Removed

- fix(release): retire `bin/install.js` (#242). The npm wrapper's
  authoritative install code path is `bin/install-impl.js`, which
  reads its version from `package.json` at runtime and is
  goreleaser-independent. The hand-bumped `bin/install.js VERSION`
  constant and the npm.yml grep that gated it were a fiction:
  `.goreleaser.yaml` only runs `go mod tidy` as a before-hook and
  never rewrote `bin/install.js`. Dropping the file, the gate, and
  the `.goreleaser.yaml` comment makes `package.json#version` the
  single source of truth again.

## [0.6.0] - 2026-08-04

Minor bump. Adds the version-detection foundation plus the
runtime-info companion tool the 2026-08-03 QA audit called for;
ships SBOM and provenance on every release artefact; lands
Dependabot, pinned action SHAs, CODEOWNERS, and issue templates.

### Added

- feat(nodered): central Node-RED version detection + min-version
  table (#170). `Client.NodeRedVersion()` (cached, sync.Once)
  backed by `GET /settings`; `nodered_min_version_for` map is the
  single source of truth; `attachMinVersion` appends
  " (requires NR >= X.Y)" to each gated tool's description at
  registration time.
- feat(mcp): add `get_runtime_info` with capability matrix (#168).
  Companion to `get_diagnostics` that reports the MCP server's
  view of the runtime (NR version, runtimeState, debug stream,
  per-tool capability as ok / version_too_low /
  endpoint_not_mounted / setting_disabled / stream_disabled /
  unknown).
- feat(mcp): startup banner reports detected NR version +
  degraded tools (#171). Async probe after boot; follow-up
  `Node-RED version detected` log line carries the version and
  the count of gated tools the running NR cannot serve.
- feat(npm): publish Windows binaries on the npm install channel
  (#192). Drops the goreleaser `format_overrides` that forced
  Windows archives as `.zip`; the existing `bin/tar.js` extractor
  handles `.tar.gz` everywhere. Closes #182.
- chore: enable SBOM and provenance on release artefacts (#179).
  Goreleaser emits a CycloneDX SBOM, Docker image builds with
  `provenance: true` + `sbom: true`, and the npm wrapper publishes
  with `provenance: true` (OIDC attestation).
- chore: enable Dependabot for go modules and github-actions
  (#177). Daily grouping, weekly windows, marked-major ignored for
  `mark3labs/mcp-go` (breaking-change budget lives in our hands,
  not Dependabot's).
- chore: pin github actions to commit SHA (#178). 13 references
  pinned; the renovate-style SHA-only pattern keeps the supply
  chain auditable.
- chore: add CODEOWNERS, issue templates, and PR template (#181).
  CODEOWNERS catch-all is `@fgjcarlos`; `bug.yml` + `feature.yml`
  templates; PR template lists scope, tests, risks.
- docs: add CHANGELOG and GitHub release-notes categories (#180).
  `CHANGELOG.md` (Keep a Changelog 1.1) and `.github/release.yml`
  with conventional-commit categories.
- docs: ship `cleanup-npm-shims.ps1` helper for orphaned Windows
  shims (#184). Idempotent, no admin required.
- chore(deps): bump `docker/build-push-action` 6.19.2 -> 7.3.0
  (#206).
- chore(deps): bump `docker/metadata-action` 5.10.0 -> 6.2.0
  (#205).
- chore(deps): bump `docker/setup-buildx-action` 3.12.0 ->
  4.2.0 (#203).
- chore(deps): bump `docker/setup-qemu-action` 3.7.0 -> 4.2.0
  (#204).
- chore(deps): bump `actions/setup-go` 5.6.0 -> 7.0.0 (#207).

### Changed

- docs: install sections describe npm, go install and docker
  (#194).
- chore: retire shell install scripts in favour of go install
  (#193). `scripts/install.sh` and `scripts/install.ps1` are
  deleted; the standalone-binary channel's update hint now
  points at
  `go install github.com/fgjcarlos/nodered-mcp/cmd/nodered-mcp@latest`.
- docs: document translation policy in CONTRIBUTING.md (#175).

### Fixed

- fix(mcp): translate 403 on /diagnostics to a setting hint
  (#169). When `settings.diagnostics.enabled = false` on NR >=
  3.1 the runtime returns 403; the handler now names the setting
  so the operator can fix it.
- fix(npm): declare `os: [linux, darwin]` so Windows aborts
  before postinstall (#191). Reverted by #192 once the Windows
  `.tar.gz` asset landed.

## [0.5.14] - 2026-07-29

### Added

- chore(release): gate npm publish on `bin/install.js` VERSION
  match (#154). The wrapper's postinstall builds its tarball URL
  from this literal; a stale constant was the bug from #146.
- chore(complexity): bump `synthesizeFlowFromFlat` baseline 15 to
  16 (#155).
- test(coverage): raise cmd/nodered-mcp coverage (issues #69 parts
  6 and 7). Coverage ratchet gate now requires the full suite.

### Changed

- fix(docker): bump Dockerfile base image to `golang:1.26.5-alpine`
  (#157).
- chore(lint): clean 3 staticcheck findings (#151).
- chore(deps): bump `golang.org/x/text` to v0.40.0 (#150).
- docs: refresh tool/test counts in PLAN.md (#153).

### Fixed

- fix(update): correct install.sh URL in standalone-binary channel
  (#148). The URL used to be `.../main/install.sh`, which 404s; the
  script lives under `scripts/`.
- test(cmd): skip `TestExecutablePath_PrefersSymlinkTarget` on
  Windows (#149).

## [0.5.13] - 2026-07-29

### Added

- feat(mcp): `inject_node` optional payload overrides `msg.payload`
  (#54).
- feat(mcp): `validate_flow` dry-run + `disable_flow` /
  `enable_flow` (#53).
- feat(mcp): `get_runtime_logs` + `get_node_status` (#51).
- feat(mcp): `export_flow` / `import_flow` (#50).
- feat(mcp): `set_context` via managed helper inject node (#52).
- docs(plan): add v0.5.12 audit status section (#49).

### Fixed

- fix(nodered): close #44 as wontfix (#57).
- fix(nodered): `inject_node` refuses to fire non-inject nodes (#56).
- fix(mcp): per-tool timeout and bounded retry on hung tools (#55).

## [0.5.12] - 2026-07-28

### Fixed

- fix(mcp): HTTP panic recovery + per-request logging, debug-stream
  onboarding (#39).
- fix(nodered): serialize mutations, reject dangling refs, require
  x/y on `add_node` (#38).
- fix(nodered): fall back to GET /flows when /flow/:id 404s (#37).
- fix(mcp): accept object/array payloads on `add_node` and
  `update_flow` (#36).

## [0.5.11] - 2026-07-28

### Fixed

- fix: address 6 actionable bugs from v0.5.10 testing report (#21).
- fix: make debug stream opt-in and accept array auth replies (#20).
- fix: surface a hint when an admin API endpoint returns 404 (#19).

## [0.5.10] - 2026-07-28

### Added

- docs: add CONTRIBUTING.md.
- docs: bump README version example to 0.5.9.

## [0.5.9] - 2026-07-28

### Added

- feat: `nodered-mcp update` subcommand for the npm wrapper channel
  (#11).
- docs: rewrite PLAN.md against the shipped v0.5.1 codebase (#4).
- docs: add Pi (pi-mono) to Client integration (#10).
- docs: add OpenCode to Client integration (#9).
- docs: surface npm and GHCR install paths in both READMEs (#8).

## [0.5.8] - 2026-07-27

### Changed

- chore(npm): bump wrapper to 0.5.8.

## [0.5.7] - 2026-07-27

### Changed

- chore(npm): bump wrapper to 0.5.7.

## [0.5.6] - 2026-07-27

### Fixed

- fix(npm): write `NPM_SECRET` into `~/.npmrc` instead of relying on
  `NODE_AUTH_TOKEN`. The runner's empty `.npmrc` shadows the env
  variable.

## [0.5.5] - 2026-07-27

### Fixed

- fix(npm): drop the smoke test that races release.yml.

## [0.5.4] - 2026-07-27

### Fixed

- fix(npm): read package.json version inside the step instead of via
  `$GITHUB_OUTPUT`. The Actions output parser rejects semver-shaped
  values with `Invalid format 'X.Y.Z'`.

## [0.5.3] - 2026-07-27

### Fixed

- fix(npm): keep `package.json` version as a string for the workflow
  output.

## [0.5.2] - 2026-07-27

### Added

- feat: npm wrapper — `npm i -g @fgjcarlos/nodered-mcp` downloads
  the binary. The wrapper has zero npm dependencies; tar extraction
  is a hand-rolled POSIX ustar parser using only Node stdlib.

## [0.5.1] - 2026-07-27

### Added

- ci: publish container image to GHCR on every `v*` tag.

## [0.5.0] - 2026-07-26

### Added

- feat: OAuth 2.1 Resource Server (JWT bearer via IdP). The server
  now validates bearer tokens issued by an external Identity
  Provider when `MCP_OAUTH_ISSUER` is set.

[Unreleased]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.8.0...HEAD
[0.8.0]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.7.2...v0.8.0
[0.7.2]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.7.1...v0.7.2
[0.7.1]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.7.0...v0.7.1
[0.7.0]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.6.2...v0.7.0
[0.6.2]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.6.1...v0.6.2
[0.6.0]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.5.15...v0.6.0
[0.5.14]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.5.13...v0.5.14
[0.5.13]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.5.12...v0.5.13
[0.5.12]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.5.11...v0.5.12
[0.5.11]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.5.10...v0.5.11
[0.5.10]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.5.9...v0.5.10
[0.5.9]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.5.8...v0.5.9
[0.5.8]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.5.7...v0.5.8
[0.5.7]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.5.6...v0.5.7
[0.5.6]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.5.5...v0.5.6
[0.5.5]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.5.4...v0.5.5
[0.5.4]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.5.3...v0.5.4
[0.5.3]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.5.2...v0.5.3
[0.5.2]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/fgjcarlos/nodered-mcp/releases/tag/v0.5.0
