# syntax=docker/dockerfile:1

# ---- build ----------------------------------------------------------------
FROM golang:1.25-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG VERSION=docker
RUN CGO_ENABLED=0 go build \
	-ldflags "-s -w -X main.version=${VERSION}" \
	-o /out/nodered-mcp ./cmd/nodered-mcp

# ---- runtime --------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/nodered-mcp /usr/local/bin/nodered-mcp

# A container has no MCP client attached to stdin, so the stdio transport is
# useless here — default to HTTP. Every value is overridable at `docker run`.
ENV MCP_TRANSPORT=http \
	MCP_HTTP_ADDR=:8090 \
	NODERED_URL=http://host.docker.internal:1880

EXPOSE 8090
ENTRYPOINT ["nodered-mcp"]
