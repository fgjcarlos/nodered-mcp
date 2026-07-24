# 301 — Publish the repo on GitHub

**Labels:** distribution, blocker
**Milestone:** 4 — Distribution / DX

## Context

The whole distribution story depends on this. Right now the project isn't a git repository and isn't on GitHub, so:

- `go install github.com/fgjcarlos/nodered-mcp/...@latest` → 404
- GitHub Releases (prebuilt binaries) → nothing to release
- The release workflow (302) has nothing to run against

This is the one step that can't be automated from here — it needs the maintainer's GitHub account.

## Tasks

- [ ] `git init` + first commit
- [ ] Create the GitHub repo `fgjcarlos/nodered-mcp` and push
- [ ] Push a first tag: `git tag v0.4.0 && git push origin v0.4.0` (triggers the release workflow → binaries + checksums)
- [ ] (optional) Enable GHCR and add a Docker publish step

## Acceptance criteria

- `go install github.com/fgjcarlos/nodered-mcp/cmd/nodered-mcp@latest` works
- A GitHub Release exists with binaries for linux/macOS/windows × amd64/arm64
