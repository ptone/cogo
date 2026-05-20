#!/usr/bin/env bash
# Copyright 2026 The Cogo Authors.
# SPDX-License-Identifier: Apache-2.0
#
# uat-smoke-tool-selection — does the model reach for the structured
# `glob` tool over `bash find`/`bash ls` when asked to enumerate files?
#
# This pins the v0.3.0 tool-routing claim: every shell command in the
# bash blocklist (`ls`, `find -name`, etc.) has a structured
# alternative; a coding-optimized model should pick it. If a model
# fails this smoke test it is structurally unsuited for cogo regardless
# of how fast/cheap it is — the trace will be full of bash workarounds.
#
# DO NOT delete this test to silence a regression: a flaky pass is the
# flaky-pass signal we want. If the model occasionally falls back to
# bash, that's a real characterization datum (probably wants a stronger
# nudge in DefaultInstruction). Fix the prompt/instruction, not the
# assertion.
#
# Pass: trace shows → glob; trace does NOT show → bash for find/ls.
# Bash for other purposes is fine — only the listing verbs matter.

set -u
. "$(dirname "$0")/../lib/common.sh"

UAT_ID="uat-smoke-tool-selection"
FIXTURE="$(cd "$(dirname "$0")/fixture" && pwd)"
PROMPT="$(dirname "$0")/prompt.txt"
PREFIX="/tmp/${UAT_ID}"

trap uat_cleanup EXIT
uat_setup_fixture "$FIXTURE" || exit 2

echo "==> $UAT_ID — driving cogo with the canned prompt"
uat_run_cogo "$PROMPT" "$PREFIX" || true

echo "==> $UAT_ID — checking tool choice"
# Hard pass condition: the structured glob tool was used.
uat_assert_trace_has "$PREFIX" glob

# Hard fail condition: a bash invocation that's clearly a filesystem
# walk. We don't ban bash outright — the model may legitimately reach
# for it for non-listing work — only the listing verbs that have a
# structured replacement.
if grep -qE '"command":"(find|ls)( |\\)' "${PREFIX}.trace" 2>/dev/null; then
  uat_fail "trace contains bash find/ls — model should use glob/list_dir instead:"
  grep -nE '"command":"(find|ls)' "${PREFIX}.trace" >&2 | head -5
else
  uat_ok "trace lacks bash find/ls"
fi

uat_finish "$UAT_ID"
