# Contributing to nodered-mcp

`nodered-mcp` is a single-maintainer project. There is no formal triage
process, no PR template, and no human reviewer queue. Issues are the
roadmap and pull requests are the unit of work.

The shape below is what the maintainer actually does. Following it is
the single best way to get a change merged.

## Reporting a bug or requesting a feature

Open a GitHub issue. Use the title prefix convention so the maintainer
can scan the queue:

- `feat:` — new tool, new CLI subcommand, new install channel.
- `fix:` — wrong behaviour you reproduced.
- `docs:` — README, README.es, PLAN, or examples drift.
- `chore:` — tooling, CI, deps.

The body should answer three questions:

1. **What did you do?** The exact command, the URL of the Node-RED
   instance, the version of `nodered-mcp` (`nodered-mcp version`).
2. **What did you expect?** Why this matters — a real flow you were
   trying to build, a real client you were trying to configure.
3. **What happened instead?** The error message, the unexpected output,
   the workaround. If you worked around it, say how.

A reproduction with a small flow JSON beats any prose description.

## Sending a pull request

The maintainer ships by squash-merging `feat|fix|docs|chore` branches
into `main`. There is no release branch and no long-lived integration
branch.

- **One branch per issue.** Branch name: `<type>/issue-<number>-<slug>`.
  Example: `feat/issue-11-update` for issue #11.
- **Conventional commit message.** The squash commit becomes the
  release note, so the headline and body stand on their own.
- **Tests in the same PR.** If your change touches Go, `go test ./...`
  must pass on the PR before the merge. New behaviour gets a test. Run
  `go test ./cmd/nodered-mcp -run <Name>` to scope to your package.
- **No code-review checkpoint has to pass.** The maintainer reads the
  diff and decides. A draft PR is fine if you want feedback before
  merging.
- **Rebase onto `main` if the diff conflicts.** The maintainer
  squash-merges; the contributor's job is to keep the branch current
  when it falls behind.

### What the CI gate covers

`.github/workflows/ci.yml` (runs on every push and PR):

- `gofmt -l .` — formats must be clean on Golangci-lint defaults.
- `go vet ./...`
- `go mod tidy` — working tree must be tidy.
- `go test ./...` on Linux, macOS, and Windows.
- `go test -race ./...` on Linux only — the race detector needs a
  working C toolchain, which is only dependable on the Linux runner.
  Data races are OS-independent; the matrix above exists for path
  handling, not for concurrency.

If your PR adds a new tool, the tool must be wired through `internal/mcp/tools.go`,
show up in the §5 catalog in `PLAN.md`, and not regress the read/write
counts already documented there.

## Release process

The release pipeline is fully tag-driven. The maintainer does the
following by hand once per release:

1. Bump `package.json` (`version`) and `bin/install.js` (`VERSION`).
   Commit: `chore(npm): bump wrapper to X.Y.Z`.
2. Push the commit to `main`.
3. `git tag -a vX.Y.Z` and `git push origin vX.Y.Z`.

Three GitHub Actions fire on the tag and run in parallel:

- `release.yml` — goreleaser publishes the six binaries and
  `checksums.txt` to the GitHub release.
- `docker.yml` — multi-arch image is pushed to `ghcr.io/fgjcarlos/nodered-mcp`.
- `npm.yml` — `@fgjcarlos/nodered-mcp` is published to npmjs; the
  workflow refuses to ship if `package.json` and the tag disagree.

A new release is justified when there is a user-visible change: a new
tool, a new subcommand, a new install channel, a fixed bug. Pure docs
changes do not get a release.

## Communication

Comments on issues and pull requests are the only channel. There is no
Discord, Slack, or mailing list.

A PR that has been silent for a couple of weeks is fair game to be
closed. Reopen it once there is new context.

## What the maintainer will not do

- Accept a PR that edits `README.md` and `README.es.md` together.
  The English version is the source of truth; the Spanish version is
  a translation. They almost always need to land in separate commits
  so the reviewer can see both.
- Accept a PR that bundles multiple unrelated issues. One issue, one
  branch, one PR. If you have two findings, open two pull requests.
- Accept a PR that introduces a new dependency without justifying it
  in the PR body. The project tracks four runtime dependencies; the
  bar for adding a fifth is "no stdlib alternative, no standard
  alternative".
- Accept a PR that bypasses the backup-before-write guardrail. Every
  mutating tool currently snapshots before it ships; the design
  rationale is in `PLAN.md` §3.
