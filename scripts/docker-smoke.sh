#!/usr/bin/env bash
set -euo pipefail

# Issue #346: the runtime image ran distroless nonroot with no writable
# working directory, so the pre-write snapshot every mutating call takes
# failed on its own backup directory and the mutation was aborted. This
# smoke drives a real deployment through the MCP HTTP transport, checks
# that the snapshot landed in the mounted backup directory, and checks
# that it is still there after the container is recreated.

image="${1:-nodered-mcp:smoke}"
container="nodered-mcp-smoke-$$"
stub="nodered-stub-smoke-$$"
network="nodered-smoke-net-$$"
volume="nodered-backups-smoke-$$"
# Pinned by digest: the stub is a Node-RED stand-in, not a dependency of
# the image under test.
stub_image="node:22-alpine@sha256:1314fb30ec37592d5845e5a160d522c0e680972aa534549940b8d73bde88481a"
token="docker-smoke-token"
workdir="$(mktemp -d)"

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  docker rm -f "$stub" >/dev/null 2>&1 || true
  docker volume rm -f "$volume" >/dev/null 2>&1 || true
  docker network rm -f "$network" >/dev/null 2>&1 || true
  rm -rf "$workdir"
}
trap cleanup EXIT

docker build --tag "$image" .

# A Node-RED stand-in: the MCP server talks to the admin API over HTTP,
# and the smoke only needs the endpoints a deployment touches.
docker network create "$network" >/dev/null
docker run --detach --name "$stub" \
  --network "$network" \
  --publish 127.0.0.1::1880 \
  "$stub_image" node -e '
const http = require("http");
const flows = [{id: "smoke-tab", type: "tab", label: "Smoke"}];
http.createServer((req, res) => {
  req.on("data", () => {});
  req.on("end", () => {
    const payload = req.url.startsWith("/settings") ? {version: "3.1.0"} : flows;
    res.writeHead(200, {"Content-Type": "application/json"});
    res.end(JSON.stringify(payload));
  });
}).listen(1880);
' >/dev/null

stub_port="$(docker port "$stub" 1880/tcp | cut -d: -f2)"
for _ in {1..30}; do
  if curl --silent --output /dev/null "http://127.0.0.1:$stub_port/settings"; then
    break
  fi
  sleep 1
done

docker volume create "$volume" >/dev/null

start_container() {
  docker run --detach --name "$container" \
    --network "$network" \
    --publish 127.0.0.1::8090 \
    --volume "$volume:/var/lib/nodered-mcp/backups" \
    --env "MCP_HTTP_TOKEN=$token" \
    --env "NODERED_URL=http://$stub:1880" \
    "$image" >/dev/null
  port="$(docker port "$container" 8090/tcp | cut -d: -f2)"
  url="http://127.0.0.1:$port/mcp"
}

# post <output file> <session id|-> <json body> -> HTTP status on stdout
post() {
  local out="$1" session="$2" body="$3"
  local args=(--silent --show-error --output "$out" --write-out '%{http_code}'
    --dump-header "$workdir/headers"
    --request POST
    --header "Authorization: Bearer $token"
    --header 'Accept: application/json, text/event-stream'
    --header 'Content-Type: application/json'
    --data "$body")
  if [[ "$session" != "-" ]]; then
    args+=(--header "Mcp-Session-Id: $session")
  fi
  curl "${args[@]}" "$url"
}

# initialize -> session id on stdout; the body lands in $workdir/init.json
initialize() {
  local status
  status="$(post "$workdir/init.json" - '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"docker-smoke","version":"1.0"}}}')"
  if [[ "$status" != "200" ]]; then
    docker logs "$container" >&2 || true
    printf 'expected an authenticated initialize request to return 200, got %s\n' "$status" >&2
    exit 1
  fi
  awk 'tolower($1) == "mcp-session-id:" {gsub(/\r/, "", $2); print $2}' "$workdir/headers" | tail -1
}

# call_tool <session> <id> <name> <arguments> <output file> -> HTTP status
call_tool() {
  local session="$1" id="$2" name="$3" args="$4" out="$5"
  post "$out" "$session" "{\"jsonrpc\":\"2.0\",\"id\":$id,\"method\":\"tools/call\",\"params\":{\"name\":\"$name\",\"arguments\":$args}}"
}

start_container

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

session="$(initialize)"
if [[ -z "$session" ]]; then
  docker logs "$container" >&2 || true
  printf 'expected initialize to return an Mcp-Session-Id header\n' >&2
  exit 1
fi

# A real deployment: the server takes its pre-write snapshot first, so
# this fails when the backup directory is not writable.
status="$(call_tool "$session" 2 set_flows \
  '{"flows":"[{\"id\":\"smoke-tab\",\"type\":\"tab\",\"label\":\"Smoke\"}]"}' \
  "$workdir/deploy.json")"
if [[ "$status" != "200" ]] || grep -q '"isError":true' "$workdir/deploy.json" || ! grep -q 'Flows deployed' "$workdir/deploy.json"; then
  docker logs "$container" >&2 || true
  printf 'expected set_flows to deploy, got HTTP %s: %s\n' "$status" "$(cat "$workdir/deploy.json")" >&2
  exit 1
fi

status="$(call_tool "$session" 3 list_backups '{}' "$workdir/backups.json")"
if [[ "$status" != "200" ]] || grep -q '"isError":true' "$workdir/backups.json" || ! grep -q 'flows-' "$workdir/backups.json"; then
  docker logs "$container" >&2 || true
  printf 'expected the deployment to leave a snapshot behind, got HTTP %s: %s\n' "$status" "$(cat "$workdir/backups.json")" >&2
  exit 1
fi

backup="$(grep -o 'flows-[0-9]\{8\}-[0-9]\{6\}\.[0-9]\{9\}-[0-9]\{6\}\.json' "$workdir/backups.json" | head -1 || true)"
if [[ -z "$backup" ]]; then
  printf 'expected a backup file name in %s\n' "$(cat "$workdir/backups.json")" >&2
  exit 1
fi

# Recreating the container must not take the snapshot with it: the
# backup lives in the mounted volume, not in the container's layer.
docker rm -f "$container" >/dev/null
start_container
session="$(initialize)"
status="$(call_tool "$session" 4 list_backups '{}' "$workdir/backups2.json")"
if [[ "$status" != "200" ]] || grep -q '"isError":true' "$workdir/backups2.json" || ! grep -q "$backup" "$workdir/backups2.json"; then
  docker logs "$container" >&2 || true
  printf 'expected %s to survive the container recreation, got HTTP %s: %s\n' "$backup" "$status" "$(cat "$workdir/backups2.json")" >&2
  exit 1
fi

printf 'Docker smoke test passed (authenticated initialize returned 200, deployment wrote %s and it survived the container recreation).\n' "$backup"
