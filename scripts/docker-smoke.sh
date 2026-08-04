#!/usr/bin/env bash
set -euo pipefail

image="${1:-nodered-mcp:smoke}"
container="nodered-mcp-smoke-$$"
token="docker-smoke-token"

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker build --tag "$image" .
docker run --detach --name "$container" \
  --publish 127.0.0.1::8090 \
  --env "MCP_HTTP_TOKEN=$token" \
  --env "NODERED_URL=http://127.0.0.1:1880" \
  "$image" >/dev/null

port="$(docker port "$container" 8090/tcp | cut -d: -f2)"
url="http://127.0.0.1:$port/mcp"

for _ in {1..30}; do
  status="$(curl --silent --output /dev/null --write-out '%{http_code}' "$url" || true)"
  if [[ "$status" == "401" ]]; then
    break
  fi
  sleep 1
done

if [[ "$status" != "401" ]]; then
  docker logs "$container" >&2 || true
  printf 'expected an unauthenticated request to return 401, got %s\n' "$status" >&2
  exit 1
fi

status="$(curl --silent --output /dev/null --write-out '%{http_code}' \
  --request POST \
  --header "Authorization: Bearer $token" \
  --header 'Accept: application/json, text/event-stream' \
  --header 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"docker-smoke","version":"1.0"}}}' \
  "$url" || true)"
if [[ "$status" != "200" ]]; then
  docker logs "$container" >&2 || true
  printf 'expected an authenticated initialize request to return 200, got %s\n' "$status" >&2
  exit 1
fi

printf 'Docker smoke test passed (authenticated initialize returned %s).\n' "$status"
