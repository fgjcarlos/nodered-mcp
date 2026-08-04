# Changelog

All notable changes to this project are documented here. The format
follows [Keep a Changelog 1.1](https://keepachangelog.com/en/1.1.0/);
this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- feat(npm): publish Windows binaries on the npm install channel
  (#192). Drops the goreleaser `format_overrides` that forced Windows
  archives as `.zip`; the existing `bin/tar.js` extractor handles
  `.tar.gz` everywhere. Closes #182.
- chore: enable SBOM and provenance on release artefacts (#179).
  Goreleaser emits a CycloneDX SBOM, Docker image builds with
  `provenance: true` + `sbom: true`, and the npm wrapper publishes
  with `provenance: true` (OIDC attestation).
- chore: enable Dependabot for go modules and github-actions (#177).
- chore: pin github actions to commit SHA (#178).
- chore: add CODEOWNERS, issue templates, and PR template (#181).

### Changed

- docs: install sections describe npm, go install and docker (#194).
- chore: retire shell install scripts in favour of go install (#193).
  `scripts/install.sh` and `scripts/install.ps1` are deleted; the
  standalone-binary channel's update hint now points at
  `go install github.com/fgjcarlos/nodered-mcp/cmd/nodered-mcp@latest`.
- docs: document translation policy in CONTRIBUTING.md (#175).

### Fixed

- fix(npm): declare `os: [linux, darwin]` so Windows aborts before
  postinstall (#191). Reverted by #192 once the Windows `.tar.gz`
  asset landed.

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

[Unreleased]: https://github.com/fgjcarlos/nodered-mcp/compare/v0.5.14...HEAD
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