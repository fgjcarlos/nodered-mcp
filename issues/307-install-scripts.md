# 307 — One-line install scripts

**Labels:** distribution, dx
**Milestone:** 4 — Distribution / DX
**Status:** ⛔ superseded by #193 (2026-08)

The shell install scripts were the documented install channel at the
time of writing. After #192 ships Windows binaries on the npm channel,
#193 retires both scripts in favour of `npm install` and `go install`.
This file is preserved as repo history — closing actions live on
[#193](https://github.com/fgjcarlos/nodered-mcp/issues/193).

## Context

The "download + configure in one command" flow for non-Claude-Desktop clients: a bootstrap script fetches the right prebuilt binary from Releases, puts it on PATH, then hands off to `nodered-mcp init --write`.

## What was done

- [x] `scripts/install.sh` (Linux/macOS): detects OS/arch, downloads `nodered-mcp_<os>_<arch>.tar.gz` from `releases/latest/download`, installs to `~/.local/bin`.
- [x] `scripts/install.ps1` (Windows): detects arch, downloads the `.zip`, installs to `%LOCALAPPDATA%\Programs\nodered-mcp`, adds it to the user PATH.
- [x] `.goreleaser.yaml` archive `name_template` pinned to `{{.ProjectName}}_{{.Os}}_{{.Arch}}` (no version) so the `latest/download/<asset>` URL is stable across releases.

## Remaining

- Depends on [301](301-publish-repo.md): the download URLs 404 until a Release exists. Can't be tested headless.

## Acceptance criteria

- After 301, `curl -fsSL .../install.sh | sh` installs a working binary and prints the `init --write` next step
