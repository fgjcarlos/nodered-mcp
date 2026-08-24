# syntax=docker/dockerfile:1

# ---- build ----------------------------------------------------------------
FROM golang:1.26.7-alpine AS build
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
# useless here — default to HTTP. MCP_HTTP_TOKEN and NODERED_URL are supplied
# explicitly at `docker run`; neither has a safe, portable image default.
ENV MCP_TRANSPORT=http \
	MCP_HTTP_ADDR=:8090

EXPOSE 8090
ENTRYPOINT ["nodered-mcp"]
