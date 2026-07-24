# 302 — Prebuilt binaries via GoReleaser

**Labels:** distribution, ci
**Milestone:** 4 — Distribution / DX
**Status:** ✅ done (waiting on 301 to actually fire)

## Context

A first-time user without the Go toolchain currently can't install anything. Prebuilt binaries per OS/arch remove that barrier. GoReleaser builds them (CGO disabled, cross-compiled) and attaches them to a GitHub Release on every `vX.Y.Z` tag.

## What was done

- [x] `.goreleaser.yaml` — builds linux/darwin/windows × amd64/arm64, `CGO_ENABLED=0`, `-ldflags -s -w -X main.version={{.Version}}`, tar.gz (zip on Windows), checksums. Validated with `goreleaser check` (in a git repo with a remote).
- [x] `.github/workflows/release.yml` — runs `goreleaser release --clean` on tag push, using the built-in `GITHUB_TOKEN`.

## Remaining

- Depends on [301](301-publish-repo.md): needs the repo on GitHub + a pushed tag to produce a real release.
- (optional) Add GHCR Docker publish to the pipeline.

## Acceptance criteria

- Pushing a `vX.Y.Z` tag produces a Release with binaries + `checksums.txt`
