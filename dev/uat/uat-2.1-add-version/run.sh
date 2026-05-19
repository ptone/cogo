#!/usr/bin/env bash
# Copyright 2026 The Cogo Authors.
# SPDX-License-Identifier: Apache-2.0
#
# UAT-2.1: add a /version slash command. The canonical "add a feature
# with N coordinated edits" task. See ../README.md and
# ../../../docs/v0.3.0-uat.md for the rationale.

set -u
. "$(dirname "$0")/../lib/common.sh"

UAT_ID="uat-2.1-add-version"
PROMPT="$(dirname "$0")/prompt.txt"
PREFIX="/tmp/${UAT_ID}"

trap uat_cleanup EXIT
uat_setup_clone || exit 2

echo "==> $UAT_ID — driving cogo with the canned prompt"
uat_run_cogo "$PROMPT" "$PREFIX" || true   # non-zero from cogo is okay; we judge by outcome

echo "==> $UAT_ID — checking the edits"
uat_assert_file_contains internal/tui/commands.go 'SlashVersion'
uat_assert_file_contains internal/tui/commands.go '"version"\s*:\s*SlashVersion'
uat_assert_file_contains internal/tui/palette.go '/version'
uat_assert_file_contains internal/tui/update.go 'SlashVersion'

echo "==> $UAT_ID — build + tests"
uat_assert_build
uat_assert_tests ./internal/tui/...

echo "==> $UAT_ID — tool-choice signal (informational, not gating)"
# The agent should batch reads when discovering the slash-command
# pattern. We don't fail the UAT on this — the build/test signals
# above are the source of truth — but log what we saw.
if grep -qE '^→ (grep|read_many_files)' "${PREFIX}.trace" 2>/dev/null; then
  echo "  ℹ structured tools were used (grep/read_many_files)"
else
  echo "  ℹ no structured-tool calls in trace (agent fell back to read_file/bash)"
fi

uat_finish "$UAT_ID"
