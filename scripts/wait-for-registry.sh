#!/usr/bin/env bash
# scripts/wait-for-registry.sh — retry a registry read with bounded backoff.
#
# npm's registry is eventually consistent. A successful `npm publish` (or
# `npm dist-tag add`) can be followed by an `npm view` returning E404 or
# an empty value for several seconds. Treating those reads as fatal made
# the v0.7.2 release (#298) require two manual reruns: the npm write had
# succeeded, the next read had not yet propagated.
#
# This helper retries the read up to N times with linear backoff
# (3s, 6s, 9s, 12s, 15s by default — total budget ~45s) and exits
# 0 on the first non-empty value. After the budget is exhausted it
# exits 1 so the calling step fails closed, matching the workflow's
# existing invariant: do not move any `latest` tag until the registry
# actually carries the expected state.
#
# Usage:
#   wait-for-registry.sh -- npm view "@scope/pkg@version" dist.integrity
#   wait-for-registry.sh --attempts 10 -- npm view "@scope/pkg" dist-tags.latest
#
# Exits 0 on first non-empty stdout. Exits 1 on retry exhaustion or
# argument error. Stderr carries the attempt log so the CI step's
# failure output names what was waited on.
#
# ponytail: linear backoff is fine for npm's documented propagation
# window (~30s); switch if a future cycle sees real exhaustion.

set -euo pipefail

attempts=5
base_sleep=3
cmd=()

while [ $# -gt 0 ]; do
  case "$1" in
    --attempts)
      attempts="$2"
      shift 2
      ;;
    --base-sleep)
      base_sleep="$2"
      shift 2
      ;;
    --)
      shift
      cmd=("$@")
      break
      ;;
    *)
      echo "wait-for-registry.sh: unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

if [ "${#cmd[@]}" -eq 0 ]; then
  echo "wait-for-registry.sh: missing command after --" >&2
  exit 2
fi

if ! [[ "$attempts" =~ ^[1-9][0-9]*$ ]]; then
  echo "wait-for-registry.sh: --attempts must be a positive integer, got '$attempts'" >&2
  exit 2
fi

if ! [[ "$base_sleep" =~ ^[1-9][0-9]*$ ]]; then
  echo "wait-for-registry.sh: --base-sleep must be a positive integer, got '$base_sleep'" >&2
  exit 2
fi

description="${cmd[*]}"

for attempt in $(seq 1 "$attempts"); do
  value=$("${cmd[@]}" 2>/dev/null || true)
  if [ -n "$value" ]; then
    printf '%s' "$value"
    exit 0
  fi
  if [ "$attempt" -eq "$attempts" ]; then
    break
  fi
  sleep=$((attempt * base_sleep))
  echo "wait-for-registry: attempt $attempt/$attempts empty, sleeping ${sleep}s ($description)" >&2
  sleep "$sleep"
done

echo "wait-for-registry: gave up after $attempts attempts ($description)" >&2
exit 1