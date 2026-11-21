#!/usr/bin/env bash
# scripts/wait-for-registry.sh — retry a registry read with bounded backoff.
#
# npm's registry is eventually consistent. A successful `npm publish` (or
# `npm dist-tag add`) can be followed by an `npm view` returning E404 or
# an empty value for several seconds. Treating those reads as fatal made
# the v0.7.2 release (#298) require two manual reruns: the npm write had
# succeeded, the next read had not yet propagated.
#
# This helper retries the read up to N times with linear backoff capped
# at 30s per sleep (by default 26 attempts: 3s, 6s, ... 30s, then 30s
# each — total budget 615s, ~10 minutes per call) and exits 0 on the
# first non-empty value. npmjs took ~6 minutes to serve the last of
# seven fresh packages on v0.8.0, past the old ~45s budget (#396).
# After the budget is exhausted it exits 1 so the calling step fails closed, matching the workflow's
# existing invariant: do not move any `latest` tag until the registry
# actually carries the expected state.
#
# `--expect VALUE` tightens that contract: the read is accepted only
# when it returns exactly VALUE. Right after a `npm dist-tag add`, the
# registry can answer with the previous version of the tag — a value
# that is non-empty, so the "first non-empty wins" rule accepted it
# while the caller's own comparison failed without a retry (#344).
# With `--expect`, a stale value is retried like an empty one and the
# budget ends in failure.
#
# The command's stderr is captured and reported whenever the command
# fails, so a real registry error (E404, E403, network) reaches the
# step's log instead of being swallowed by the retry loop (#344).
#
# Usage:
#   wait-for-registry.sh -- npm view "@scope/pkg@version" dist.integrity
#   wait-for-registry.sh --expect 1.2.3 -- npm view "@scope/pkg" dist-tags.latest
#   wait-for-registry.sh --attempts 10 -- npm view "@scope/pkg" dist-tags.latest
#
# Exits 0 on the first accepted value (the first non-empty one, or the
# exact `--expect` value). Exits 1 on retry exhaustion or
# argument error. Stderr carries the attempt log so the CI step's
# failure output names what was waited on.

set -euo pipefail

attempts=26
base_sleep=3
max_sleep=30
expect=""
expect_set=false
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
    --expect)
      expect="$2"
      expect_set=true
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

if [ "$expect_set" = true ] && [ -z "$expect" ]; then
  echo "wait-for-registry.sh: --expect requires a non-empty value" >&2
  exit 2
fi

description="${cmd[*]}"
err_file=$(mktemp)
trap 'rm -f "$err_file"' EXIT

last_value=""
last_error=""

for attempt in $(seq 1 "$attempts"); do
  : >"$err_file"
  value=$("${cmd[@]}" 2>"$err_file" || true)
  last_value="$value"
  last_error=$(cat "$err_file")

  if [ -n "$expect" ]; then
    if [ "$value" = "$expect" ]; then
      printf '%s' "$value"
      exit 0
    fi
    if [ -n "$value" ]; then
      reason="stale value '$value' (expected '$expect')"
    else
      reason="empty (expected '$expect')"
    fi
  else
    if [ -n "$value" ]; then
      printf '%s' "$value"
      exit 0
    fi
    reason="empty"
  fi

  if [ "$attempt" -eq "$attempts" ]; then
    break
  fi
  sleep=$((attempt * base_sleep))
  [ "$sleep" -gt "$max_sleep" ] && sleep=$max_sleep
  echo "wait-for-registry: attempt $attempt/$attempts $reason, sleeping ${sleep}s ($description)" >&2
  if [ -n "$last_error" ]; then
    echo "wait-for-registry: last command error: $last_error" >&2
  fi
  sleep "$sleep"
done

echo "wait-for-registry: gave up after $attempts attempts ($description)" >&2
if [ -n "$expect" ]; then
  echo "wait-for-registry: expected '$expect', last value '$last_value'" >&2
fi
if [ -n "$last_error" ]; then
  echo "wait-for-registry: last command error: $last_error" >&2
fi
exit 1
