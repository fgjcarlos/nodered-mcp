# syntax=docker/dockerfile:1

# ---- build ----------------------------------------------------------------
FROM golang:1.26.11-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

# docker.yml passes the tag (v0.8.0); strip its leading v so the image
# reports the same version as the goreleaser binaries (0.8.0).
ARG VERSION=docker
RUN CGO_ENABLED=0 go build \
	-ldflags "-s -w -X main.version=${VERSION#v}" \
	-o /out/nodered-mcp ./cmd/nodered-mcp

# The runtime image has no shell, so the directory the nonroot user owns
# is prepared here and copied in with its ownership. The backup directory
# is created in the image too: a volume mounted over a path the image
# does not have inherits root ownership, and the nonroot user could not
# write a snapshot into it.
RUN mkdir -p /out/data/backups

# ---- runtime --------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/nodered-mcp /usr/local/bin/nodered-mcp

# The backup directory defaults to the relative path "backups", which
# resolves against the working directory the base image happens to set
# (/home/nonroot today) and therefore lands in the container's writable
# layer: the snapshot is lost when the container is recreated. Set a
# working directory the image owns and point the backup at an absolute
# path inside it, so the path does not depend on the base image and a
# volume can be mounted on it.
COPY --from=build --chown=nonroot:nonroot /out/data /var/lib/nodered-mcp
WORKDIR /var/lib/nodered-mcp

# A container has no MCP client attached to stdin, so the stdio transport is
# useless here — default to HTTP. MCP_HTTP_TOKEN and NODERED_URL are supplied
# explicitly at `docker run`; neither has a safe, portable image default.
ENV MCP_TRANSPORT=http \
	MCP_HTTP_ADDR=:8090 \
	NODERED_BACKUP_DIR=/var/lib/nodered-mcp/backups

EXPOSE 8090
ENTRYPOINT ["nodered-mcp"]
