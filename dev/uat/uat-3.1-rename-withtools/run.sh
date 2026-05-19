#!/usr/bin/env bash
# Copyright 2026 The Cogo Authors.
# SPDX-License-Identifier: Apache-2.0
#
# UAT-3.1: cross-package rename. The interesting question isn't
# whether the agent completes the rename — it's whether the agent
# notices when its rename broke the build. v0.3.0 has no post-edit
# verification gate (that lands in v0.4.0), so this UAT typically
# records the gap rather than passing cleanly.
#
# We report the rename's completeness (did all call sites update?)
# AND whether the agent noticed the build status. Both are useful
# signals; only the second is the v0.4.0 acceptance criterion.

set -u
. "$(dirname "$0")/../lib/common.sh"

UAT_ID="uat-3.1-rename-withtools"
PROMPT="$(dirname "$0")/prompt.txt"
PREFIX="/tmp/${UAT_ID}"

trap uat_cleanup EXIT
uat_setup_clone || exit 2

# Baseline: how many call sites reference WithTools today? We use this
# to judge "did the agent get all of them?"
baseline_calls=$(grep -rE '\bWithTools\(' --include='*.go' . | wc -l)
echo "==> $UAT_ID — baseline: $baseline_calls call sites to WithTools"
if [[ "$baseline_calls" -lt 5 ]]; then
  uat_fail "expected ≥5 call sites to WithTools; codebase shape changed"
  uat_finish "$UAT_ID"; exit 1
fi

echo "==> $UAT_ID — driving cogo with the canned prompt"
uat_run_cogo "$PROMPT" "$PREFIX" || true

echo "==> $UAT_ID — verifying the rename"
uat_assert_file_contains internal/agent/agent.go 'func WithToolList\(' "definition renamed"
uat_assert_file_lacks internal/agent/agent.go 'func WithTools\(' "old definition removed"

# Count remaining references — should be zero across the tree.
remaining=$(grep -rE '\bWithTools\(' --include='*.go' . | wc -l)
if [[ "$remaining" -eq 0 ]]; then
  uat_ok "no remaining references to WithTools across the tree"
else
  uat_fail "$remaining call site(s) still reference WithTools:"
  grep -rnE '\bWithTools\(' --include='*.go' . | head -10 >&2
fi

echo "==> $UAT_ID — build status (the v0.4.0 acceptance signal)"
if go build ./... 2>/tmp/uat-build.err; then
  echo "  ✓ build passes — agent's rename was complete OR it self-verified"
else
  echo "  ℹ build FAILS:"
  head -10 /tmp/uat-build.err | sed 's/^/      /'
  echo "  ℹ this is the v0.4.0 post-edit-verification gap. Pre-v0.4.0,"
  echo "    the agent typically doesn't run go build after edits and"
  echo "    ships broken renames. Capture the trace for review:"
  echo "      ${PREFIX}.trace"
  # NOTE: We do NOT call uat_fail here for the build failure on its own.
  # The "did all call sites update?" check above is the rename test;
  # the build failure is the gap finding. Together they characterize
  # behavior, and the umbrella runner reports either way.
fi

# Did the agent itself attempt a build/test verification? Look for
# "→ bash" lines that look like go build/test invocations.
if grep -qE '^→ bash.*go (build|test|vet)' "${PREFIX}.trace" 2>/dev/null; then
  uat_ok "agent ran go build/test/vet during the edit (self-verification observed)"
else
  uat_fail "agent did not run go build/test/vet — v0.4.0 gap confirmed"
fi

uat_finish "$UAT_ID"
