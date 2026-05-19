#!/usr/bin/env bash
# Copyright 2026 The Cogo Authors.
# SPDX-License-Identifier: Apache-2.0
#
# UAT-2.2: plant a deliberate bug in tools.Build, ask cogo to fix it,
# check the diff is surgical (one inversion, not a sprawling refactor)
# and the tests pass.

set -u
. "$(dirname "$0")/../lib/common.sh"

UAT_ID="uat-2.2-fix-bug"
PROMPT="$(dirname "$0")/prompt.txt"
PREFIX="/tmp/${UAT_ID}"

trap uat_cleanup EXIT
uat_setup_clone || exit 2

echo "==> $UAT_ID — planting the bug"
# Invert the nil-check guard at the top of tools.Build. Original
# guard refuses nil cfg; inverted, it deref's nil immediately on use.
if ! grep -q 'if cfg == nil {' internal/tools/register.go; then
  uat_fail "expected 'if cfg == nil {' guard in register.go to plant against; codebase shape changed"
  uat_finish "$UAT_ID"; exit 1
fi
sed -i 's/if cfg == nil {/if cfg != nil {/' internal/tools/register.go

# Verify the plant: tests should now fail.
if go test ./internal/tools/... >/dev/null 2>&1; then
  uat_fail "expected planted bug to break tests; they still pass. Plant likely no-op."
  uat_finish "$UAT_ID"; exit 1
fi
echo "  ✓ bug planted; tests fail as expected"

echo "==> $UAT_ID — driving cogo with the canned prompt"
uat_run_cogo "$PROMPT" "$PREFIX" || true

echo "==> $UAT_ID — verifying the fix"
uat_assert_file_contains internal/tools/register.go 'if cfg == nil \{' "guard restored"
uat_assert_tests ./internal/tools/...

echo "==> $UAT_ID — edit-discipline check (the diff should be small)"
# Count lines changed via git diff. A surgical fix is < 5 lines;
# anything bigger suggests the agent went on a refactoring spree.
lines_changed=$(git diff --shortstat | grep -oE '[0-9]+ insertions' | head -1 | grep -oE '[0-9]+' || echo 0)
if [[ "$lines_changed" -le 5 ]]; then
  uat_ok "diff is surgical ($lines_changed insertions)"
else
  uat_fail "diff is sprawling ($lines_changed insertions; expected ≤5). Output:"
  git diff --stat >&2
fi

uat_finish "$UAT_ID"
