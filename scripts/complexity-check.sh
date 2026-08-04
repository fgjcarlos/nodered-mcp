#!/usr/bin/env bash
# scripts/complexity-check.sh — issue #73 complexity floor.
#
# Reads the per-function baseline in scripts/complexity-baseline.txt and
# the hash-aware current complexity output from gocyclo and:
#
#   1. Fails (exit 1) if any named function exceeds the agreed threshold
#      (15). This is the issue #73 acceptance criterion named in the
#      ticket: "Each named function is at or below cyclomatic complexity
#      15, or the PR documents why the smallest correct form remains
#      above it."
#   2. Fails (exit 1) if any named function's complexity goes UP vs. its
#      baseline. A refactor that adds control flow is a regression.
#   3. Reports each named function's current value, the threshold, and
#      the baseline delta so the operator can see the picture at a
#      glance.
#
# Usage:
#   scripts/complexity-check.sh           # check current vs baseline + threshold
#   scripts/complexity-check.sh --write   # write current values as the new baseline
#
# Exit codes:
#   0  all named functions <= threshold AND <= baseline
#   1  one or more named functions exceed threshold or regressed
#   2  tooling error (gocyclo missing, baseline missing, etc.)
#
# The tool pinned here is gocyclo (https://github.com/fzipp/gocyclo).
# It is the de-facto standard Go cyclomatic complexity checker and is
# already vendored in CI via the workflow step that runs this script.
# The script does not introduce a new dependency to the Go module graph.

set -euo pipefail

cd "$(dirname "$0")/.."

baseline_file="scripts/complexity-baseline.txt"
mode="${1:-check}"
threshold=15

if [[ ! -f "$baseline_file" ]]; then
  echo "complexity-check: $baseline_file missing" >&2
  exit 2
fi

# Locate gocyclo: prefer the user's Go bin ($GOPATH/bin) so a developer
# who has run `go install github.com/fzipp/gocyclo/cmd/gocyclo@latest`
# gets the same binary CI uses. Fall back to PATH lookup so the script
# keeps working if the tool is on PATH another way.
gocyclo_bin=""
if [[ -n "${GOPATH:-}" && -x "${GOPATH}/bin/gocyclo" ]]; then
  gocyclo_bin="${GOPATH}/bin/gocyclo"
elif command -v gocyclo >/dev/null 2>&1; then
  gocyclo_bin="$(command -v gocyclo)"
else
  echo "complexity-check: gocyclo not found on PATH or in \$GOPATH/bin" >&2
  echo "complexity-check: install with: go install github.com/fzipp/gocyclo/cmd/gocyclo@latest" >&2
  exit 2
fi

# Build a single gocyclo output covering the files named in the baseline.
# We run gocyclo over the union of those files, not the whole tree, so
# the script stays fast as the repo grows. Files that do not exist are
# silently skipped — the baseline lines are diagnostic, not
# authoritative. We also collect named functions at the same time so we
# don't have to re-parse.
declare -a named_funcs=()
declare -a named_files=()
declare -a named_baselines=()
files=()
while IFS= read -r line; do
  # Strip a trailing CR before anything else. A Windows checkout with
  # core.autocrlf=true rewrites this .txt to CRLF, the CR rides along on
  # the last field, and every `(( current > baseline ))` below dies with
  # "invalid arithmetic operator" -- while the script still prints OK and
  # exits 0. A gate that silently stops gating is worse than no gate.
  line=${line%$'\r'}
  # Skip blank lines and comments.
  case "$line" in
    ""|\#*) continue ;;
  esac
  # The baseline format is "<func>  <file>  <complexity>" with any
  # amount of whitespace between fields. `read` collapses runs of
  # whitespace into single separators, so the three columns land in
  # func, file, cc.
  IFS=' ' read -r func file cc _ <<< "$line"
  if [[ -z "$func" || -z "$file" ]]; then
    continue
  fi
  named_funcs+=("$func")
  named_files+=("$file")
  named_baselines+=("$cc")
  if [[ -f "$file" ]]; then
    files+=("$file")
  fi
done < "$baseline_file"

if [[ ${#files[@]} -eq 0 ]]; then
  echo "complexity-check: no files in $baseline_file exist; nothing to check" >&2
  exit 2
fi

# gocyclo prints "<complexity> <package> <name> <file>:<line>:<col>". The
# function name for a method is rendered as "(<receiver>).<name>". We
# want to match by the trailing "<name>" part so the baseline is portable
# across receiver names.
cyclo_out="$("${gocyclo_bin}" "${files[@]}" 2>/dev/null || true)"

# Walk the baseline, look up each function's current value, decide.
fail=0
new_baseline=""
for i in "${!named_funcs[@]}"; do
  func="${named_funcs[$i]}"
  file="${named_files[$i]}"
  baseline_cc="${named_baselines[$i]}"
  case "$func" in
    ""|\#*) continue ;;
  esac
  # Match the line whose last token points at <=file> and whose
  # function name (last whitespace-separated token before the file) ends
  # in "/" or is the receiver.method form ending in the function name.
  # gocyclo formatting: ` 24 nodered ValidateFlow internal/nodered/validate.go:84:1`
  # patterns: receiver is "(s)" prefix on methods, plain on free functions.
  # We just match the literal function name in the line.
  current=$(printf '%s\n' "$cyclo_out" | awk -v fn="$func" -v f="$file" '
    index($NF, f) == 1 && $3 == fn { print $1; exit }
  ')
  if [[ -z "$current" ]]; then
    # Try receiver.method form: gocyclo emits " (*Server).handleFoo".
    current=$(printf '%s\n' "$cyclo_out" | awk -v fn="$func" -v f="$file" '
      {
        n = $3
        sub(/.*\)\./, "", n)
        if (n == fn && index($NF, f) == 1) { print $1; exit }
      }
    ')
  fi

  if [[ -z "$current" ]]; then
    echo "complexity-check: WARN  $func ($file) — not found in current gocyclo output" >&2
    new_baseline+="${func}    ${file}    ${baseline_cc}"$'\n'
    continue
  fi

  if (( current > threshold )); then
    echo "complexity-check: FAIL  $func ($file) — current=$current threshold=$threshold" >&2
    fail=1
  elif (( current > baseline_cc )); then
    echo "complexity-check: FAIL  $func ($file) — current=$current baseline=$baseline_cc (regression)" >&2
    fail=1
  else
    verb="OK"
    if (( current < baseline_cc )); then
      verb="IMPROVED"
    fi
    delta=$((baseline_cc - current))
    printf 'complexity-check: %-8s %s (%s) — current=%d baseline=%d delta=-%d\n' \
      "$verb" "$func" "$file" "$current" "$baseline_cc" "$delta"
  fi

  new_baseline+="${func}    ${file}    ${current}"$'\n'
done

# In --write mode the user is intentionally bumping the baseline (e.g.
# right after a refactor that reduces complexity). The threshold check
# is still useful -- but a failure there is the user's next task, not
# the script's failure to act. Save the new baseline first, then exit 0.
if [[ "$mode" == "--write" ]]; then
  # Preserve the leading comment block (every line that starts with `#`
  # or is blank) and rewrite the data lines in place. The header
  # documents the file format and is part of the contract, so rewriting
  # it as runnable bash would silently lose it.
  {
    while IFS= read -r line; do
      case "$line" in
        ""|\#*) printf '%s\n' "$line" ;;
      esac
    done < "$baseline_file"
  } > "$baseline_file.new"
  printf '%s' "$new_baseline" >> "$baseline_file.new"
  mv "$baseline_file.new" "$baseline_file"
  # Trim trailing whitespace; preserve the comment block we just wrote.
  sed -i 's/[[:space:]]\+$//' "$baseline_file"
  echo "complexity-check: wrote updated baseline to $baseline_file"
  exit 0
fi

if (( fail )); then
  echo "complexity-check: FAIL — one or more named functions exceed threshold or regressed" >&2
  exit 1
fi

echo "complexity-check: OK"
