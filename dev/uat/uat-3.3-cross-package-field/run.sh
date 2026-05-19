#!/usr/bin/env bash
# Copyright 2026 The Cogo Authors.
# SPDX-License-Identifier: Apache-2.0
#
# UAT-3.3: cross-package add-a-field. Easy to miss test-only
# constructors. Measures grep + read_many_files batching for wide
# searches.

set -u
. "$(dirname "$0")/../lib/common.sh"

UAT_ID="uat-3.3-cross-package-field"
PROMPT="$(dirname "$0")/prompt.txt"
PREFIX="/tmp/${UAT_ID}"

trap uat_cleanup EXIT
uat_setup_clone || exit 2

baseline_constructors=$(grep -rE 'permissions\.PromptRequest\{|\bPromptRequest\{' --include='*.go' . | wc -l)
echo "==> $UAT_ID — baseline: $baseline_constructors PromptRequest constructor sites"
if [[ "$baseline_constructors" -lt 3 ]]; then
  uat_fail "expected ≥3 PromptRequest constructors; codebase shape changed"
  uat_finish "$UAT_ID"; exit 1
fi

echo "==> $UAT_ID — driving cogo with the canned prompt"
uat_run_cogo "$PROMPT" "$PREFIX" || true

echo "==> $UAT_ID — verifying struct + modal + constructors"
uat_assert_file_contains internal/permissions/prompter.go 'Description\s+string' "field added"
uat_assert_file_contains internal/tui/view.go 'Description|Why:' "modal references new field"

# Every PromptRequest constructor should now set Description. Find any
# that don't.
missing=0
while IFS= read -r match; do
  file=$(echo "$match" | cut -d: -f1)
  line=$(echo "$match" | cut -d: -f2)
  # Read the constructor block (current line + next ~10 lines) and
  # check for "Description:". Tolerates Description being on a later
  # line within the literal.
  block=$(sed -n "${line},$((line+10))p" "$file")
  if ! echo "$block" | grep -qE 'Description:|//.*no description'; then
    if [[ $missing -eq 0 ]]; then
      echo "  ✗ constructors missing Description field:" >&2
    fi
    echo "      $file:$line" >&2
    missing=$((missing + 1))
  fi
done < <(grep -rnE '\bPromptRequest\{' --include='*.go' .)

if [[ $missing -eq 0 ]]; then
  uat_ok "every PromptRequest constructor sets Description"
else
  uat_fail "$missing PromptRequest constructor(s) missing Description"
fi

echo "==> $UAT_ID — build + tests"
uat_assert_build
uat_assert_tests ./internal/...

uat_finish "$UAT_ID"
