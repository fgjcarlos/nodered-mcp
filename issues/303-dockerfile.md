# 303 — Dockerfile for the HTTP transport

**Labels:** distribution, docker
**Milestone:** 4 — Distribution / DX
**Status:** ✅ done

## Context

Docker lets someone run the server with zero local install — the natural fit for the HTTP transport, where one instance serves remote MCP clients.

## What was done

- [x] `Dockerfile` — multi-stage: `golang:1.25-alpine` build (`CGO_ENABLED=0`, version via `--build-arg VERSION`), `distroless/static:nonroot` runtime. Defaults to `MCP_TRANSPORT=http` on `:8090` (stdio is useless in a container); every value overridable at `docker run`.
- [x] `.dockerignore` — whitelists only `go.mod`, `go.sum`, `cmd/`, `internal/` so the build context stays tiny.

## Verified locally

- Image builds; final size **~18.7 MB**.
- `docker run ... version` prints the injected version.
- Starts on `:8090`, logs `14 tools`, and `POST /mcp` with an `initialize` handshake returns **200**.

## Remaining

- (optional) Publish to GHCR from CI so users can `docker pull` instead of `docker build`.

## Acceptance criteria

- `docker build -t nodered-mcp . && docker run -p 8090:8090 nodered-mcp` serves MCP over HTTP at `/mcp`
