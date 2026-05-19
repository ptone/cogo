#!/usr/bin/env bash
# Copyright 2026 The Cogo Authors.
# SPDX-License-Identifier: Apache-2.0
#
# Umbrella runner for the dev/uat suite. Walks each uat-*/run.sh,
# captures pass/fail + key artifacts, prints a summary table.
#
# Usage:
#   dev/uat/runner.sh                       # run every UAT in sequence
#   dev/uat/runner.sh uat-2.1 uat-3.4       # run a subset (prefix match)
#
# Env:
#   COGO_BIN       path to the cogo binary to drive (default: PATH lookup)
#   UAT_REPO_URL   git URL the fresh clones come from (default: github origin)
#   UAT_REF        git ref / tag to test (default: v0.3.0)
#   UAT_TIMEOUT    seconds per UAT (default: 300)
#
# Each UAT runs in its own throwaway clone in a temp dir — no risk
# to the caller's working tree.

set -u

UAT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$UAT_DIR"

# Source the helpers and fail fast on missing creds — running through
# the whole UAT list to discover at the end that none of them could
# reach the model is the worst path. Each individual run.sh also
# preflights via uat_setup_clone, but this runs once up front so the
# umbrella stops before doing any work.
. "$UAT_DIR/lib/common.sh"
uat_require_creds || exit $?

declare -a all_uats
for d in uat-*/; do
  all_uats+=("${d%/}")
done

selected=()
if [[ $# -eq 0 ]]; then
  selected=("${all_uats[@]}")
else
  for arg in "$@"; do
    for u in "${all_uats[@]}"; do
      if [[ "$u" == "$arg"* ]]; then
        selected+=("$u")
      fi
    done
  done
fi

if [[ ${#selected[@]} -eq 0 ]]; then
  echo "no UATs match args: $*" >&2
  echo "available: ${all_uats[*]}" >&2
  exit 2
fi

echo "=== dev/uat/runner — running ${#selected[@]} UAT(s) ==="
echo

pass=0
fail=0
declare -a results

for u in "${selected[@]}"; do
  echo "─── $u ───"
  start=$(date +%s)
  if bash "$UAT_DIR/$u/run.sh"; then
    rc=0
    pass=$((pass + 1))
  else
    rc=$?
    fail=$((fail + 1))
  fi
  elapsed=$(( $(date +%s) - start ))
  results+=("$u|$rc|${elapsed}s")
  echo
done

echo
echo "=== summary ==="
printf "%-40s  %-6s  %s\n" UAT STATUS ELAPSED
for r in "${results[@]}"; do
  IFS='|' read -r u rc elapsed <<<"$r"
  if [[ "$rc" -eq 0 ]]; then
    printf "%-40s  %-6s  %s\n" "$u" PASS "$elapsed"
  else
    printf "%-40s  %-6s  %s\n" "$u" FAIL "$elapsed"
  fi
done
echo
echo "totals: $pass passed, $fail failed"

# Exit non-zero only when at least one UAT failed AND was expected to
# pass. Tier-3 UATs that fail "by design" pre-v0.4.0 still increment
# the fail count — the runner's exit code is for "did any UAT
# regress?", not "did any UAT find the v0.4.0 gap?". Operators who
# want to gate on "all pass" should treat exit != 0 as the signal.
if [[ $fail -gt 0 ]]; then exit 1; fi
