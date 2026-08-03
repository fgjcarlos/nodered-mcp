#!/usr/bin/env bash
# scripts/coverage-ratchet.sh — issue #69 coverage floor.
#
# Computes total statement coverage across the production packages and
# fails if the percentage drops below the baseline pinned in
# scripts/coverage-baseline.txt. The script is intentionally small: it
# exists so the CI job and a developer running locally use the same
# arithmetic. Bumping the baseline is a deliberate commit, not an
# accidental side effect of running tests.
#
# Usage:
#   scripts/coverage-ratchet.sh           # check current vs baseline
#   scripts/coverage-ratchet.sh --write   # write the new coverage as the baseline
#
# Exit codes:
#   0  coverage >= baseline
#   1  coverage <  baseline
#   2  tooling error (coverprofile empty, baseline missing, etc.)

set -euo pipefail

cd "$(dirname "$0")/.."

baseline_file="scripts/coverage-baseline.txt"
mode="${1:-check}"

if [[ ! -f "$baseline_file" ]]; then
  echo "coverage-ratchet: $baseline_file missing" >&2
  exit 2
fi

baseline=$(tr -d '[:space:]' < "$baseline_file")
if ! [[ "$baseline" =~ ^[0-9]+(\.[0-9]+)?$ ]]; then
  echo "coverage-ratchet: baseline '$baseline' is not a number" >&2
  exit 2
fi

# -count=1 defeats the Go test cache so coverage reflects the current
# tree, not the last cached run.
cov_out="$(mktemp)"
trap 'rm -f "$cov_out"' EXIT

# -coverpkg=./... aggregates coverage across every production package,
# including those that have no tests of their own. The CI job mirrors
# this so the ratchet sees the same number locally.
go test -count=1 -coverpkg=./... -coverprofile="$cov_out" ./... > /dev/null

if [[ ! -s "$cov_out" ]]; then
  echo "coverage-ratchet: coverprofile is empty; tests did not run?" >&2
  exit 2
fi

# `go tool cover -func` ends with a `total:` line. Extract the
# percentage and trim the trailing `%` and any whitespace.
pct="$(go tool cover -func="$cov_out" | awk '/^total:/ {print $NF}' | tr -d '%')"

if [[ -z "$pct" ]]; then
  echo "coverage-ratchet: could not parse coverage percentage" >&2
  exit 2
fi

# awk compares floats because bash arithmetic is integer-only.
under=$(awk -v p="$pct" -v b="$baseline" 'BEGIN { print (p + 0 < b + 0) ? 1 : 0 }')

printf 'coverage-ratchet: %.2f%% (baseline %s%%)\n' "$pct" "$baseline"

if [[ "$mode" == "--write" ]]; then
  printf '%.2f\n' "$pct" > "$baseline_file"
  echo "coverage-ratchet: wrote $pct to $baseline_file"
  exit 0
fi

if [[ "$under" == "1" ]]; then
  echo "coverage-ratchet: FAIL — coverage $pct% is below baseline $baseline%" >&2
  exit 1
fi

echo "coverage-ratchet: OK"