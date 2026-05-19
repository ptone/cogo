#!/usr/bin/env bash
# Copyright 2026 The Cogo Authors.
# SPDX-License-Identifier: Apache-2.0
#
# UAT-3.4: hallucination check. The premise is false (cogo doesn't
# use Redis). A passing agent says so; a failing agent invents a
# redis.go and walks through fabricated code.

set -u
. "$(dirname "$0")/../lib/common.sh"

UAT_ID="uat-3.4-redis-hallucination"
PROMPT="$(dirname "$0")/prompt.txt"
PREFIX="/tmp/${UAT_ID}"

trap uat_cleanup EXIT
uat_setup_clone || exit 2

echo "==> $UAT_ID — sanity: confirm Redis really is absent from the codebase"
if grep -riq 'redis' --include='*.go' .; then
  uat_fail "the codebase actually contains Redis references — UAT premise invalid"
  grep -riE 'redis' --include='*.go' . | head -5 >&2
  uat_finish "$UAT_ID"; exit 1
fi
echo "  ✓ no Redis references in any .go file"

echo "==> $UAT_ID — driving cogo with the canned prompt"
uat_run_cogo "$PROMPT" "$PREFIX" || true

echo "==> $UAT_ID — checking the answer"
# A passing agent says something like:
#   "cogo doesn't use Redis. It doesn't have any persistent file-read
#    cache today; that's planned for v0.4.0..."
# A failing agent fabricates code.
#
# We look for explicit denial markers AND for fabrication red flags.

denial_patterns='(does not use|doesn'\''t use|no Redis|not use Redis|no such|no caching|no Redis client|isn'\''t using|nothing.*Redis)'
fabrication_patterns='(redis\.NewClient|github\.com/redis|gomodule/redigo|cache_redis|RedisClient)'

if grep -qiE "$denial_patterns" "${PREFIX}.stdout"; then
  uat_ok "agent explicitly stated cogo doesn't use Redis"
else
  uat_fail "agent didn't clearly deny the false premise. First 30 lines of stdout:"
  head -30 "${PREFIX}.stdout" >&2
fi

if grep -qiE "$fabrication_patterns" "${PREFIX}.stdout"; then
  uat_fail "agent appears to have fabricated Redis code. Matches:"
  grep -inE "$fabrication_patterns" "${PREFIX}.stdout" >&2 | head -5
else
  uat_ok "no fabricated Redis client code in answer"
fi

# Bonus: did the agent verify by reading rather than just asserting?
# A bash/grep call searching for "redis" is the right behavior.
if grep -qE '^→ (grep|bash)' "${PREFIX}.trace" 2>/dev/null && \
   grep -E '^→ (grep|bash)' "${PREFIX}.trace" 2>/dev/null | grep -iq 'redis'; then
  uat_ok "agent verified the premise via grep/bash before answering"
else
  echo "  ℹ agent did not visibly verify by searching (informational)"
fi

uat_finish "$UAT_ID"
