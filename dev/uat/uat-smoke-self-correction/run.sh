#!/usr/bin/env bash
# Copyright 2026 The Cogo Authors.
# SPDX-License-Identifier: Apache-2.0
#
# uat-smoke-self-correction — does the model run a build, read the
# error, fix it, and re-verify? This is the agentic-loop primitive.
#
# Fixture: main.go calls greet.Hello, greet/greet.go declares Helo
# (typo). `go build` fails with "undefined: greet.Hello". Either side
# of the rename is acceptable; the only thing that matters is that the
# project compiles after the agent is done.
#
# Diagnostic (not gating): we also count how many go_build (or bash
# `go build`) invocations show up in the trace. A coding-optimized
# model should call it at least twice — once to discover the error,
# once to verify the fix. Calling it zero or one time means the agent
# guessed instead of verifying, even if the guess happened to land.
#
# DO NOT delete this test to silence a regression. If the agent
# repeatedly drifts off the build → edit → build loop, that's a
# v0.4.0 verification-gate signal worth investigating, not a flaky
# test to suppress.

set -u
. "$(dirname "$0")/../lib/common.sh"

UAT_ID="uat-smoke-self-correction"
FIXTURE="$(cd "$(dirname "$0")/fixture" && pwd)"
PROMPT="$(dirname "$0")/prompt.txt"
PREFIX="/tmp/${UAT_ID}"

trap uat_cleanup EXIT
uat_setup_fixture "$FIXTURE" || exit 2

echo "==> $UAT_ID — confirming fixture starts broken"
if go build ./... 2>/dev/null; then
  uat_fail "fixture should not build initially; it does — test is meaningless"
  uat_finish "$UAT_ID"
  exit 1
fi
uat_ok "fixture starts broken (expected)"

echo "==> $UAT_ID — driving cogo with the canned prompt"
uat_run_cogo "$PROMPT" "$PREFIX" || true

echo "==> $UAT_ID — checking outcome"
uat_assert_build

echo "==> $UAT_ID — diagnostic: build-loop iterations"
# Count both structured (go_build) and bash-go-build invocations.
# Trailing `|| true` so a no-match exit (1) doesn't blow up under
# `set -u`; default to 0 when the trace file is missing.
build_calls=$(grep -cE '^→ go_build( |$)|"command":"go build' "${PREFIX}.trace" 2>/dev/null || true)
build_calls=${build_calls:-0}
echo "  ℹ build invocations in trace: $build_calls"
if (( build_calls >= 2 )); then
  uat_ok "agent verified its fix (≥2 build calls)"
else
  echo "  ℹ <2 build calls — agent guessed without re-verifying (informational only)"
fi

# No orphan files: the agent should not have created scratch files
# while iterating.
if ls tmp_*.go 2>/dev/null | grep -q .; then
  uat_fail "agent left orphan tmp_*.go files:"
  ls tmp_*.go >&2
else
  uat_ok "no orphan tmp_*.go files"
fi

uat_finish "$UAT_ID"
