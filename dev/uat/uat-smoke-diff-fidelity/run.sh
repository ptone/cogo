#!/usr/bin/env bash
# Copyright 2026 The Cogo Authors.
# SPDX-License-Identifier: Apache-2.0
#
# uat-smoke-diff-fidelity — does the agent rename a method across
# multiple files surgically? Pins three observed v0.3.x failure modes:
#
#   1. Over-broad replace — sed-style s/Check/Authorize/g would also
#      rewrite CheckGeneric, which is out of scope. The test asserts
#      CheckGeneric is still spelled CheckGeneric afterward.
#   2. Orphan tmp_*.go files — bash awk/sed workarounds (now
#      blocklisted) leave scratch files behind when redirects misfire.
#      The test asserts no tmp_*.go siblings.
#   3. Drift — the agent renames the declaration but misses a call
#      site, leaving an undefined-method compile error. `go build` +
#      `go test` cover this.
#
# DO NOT delete this test to silence a regression. If the model
# repeatedly trips assertion (1), look at the trace for `bash sed`
# or wide `edit_file` old_strings — that's a v0.4.0 motivating case
# for a dedicated rename tool, not a flake.

set -u
. "$(dirname "$0")/../lib/common.sh"

UAT_ID="uat-smoke-diff-fidelity"
FIXTURE="$(cd "$(dirname "$0")/fixture" && pwd)"
PROMPT="$(dirname "$0")/prompt.txt"
PREFIX="/tmp/${UAT_ID}"

trap uat_cleanup EXIT
uat_setup_fixture "$FIXTURE" || exit 2

echo "==> $UAT_ID — confirming fixture starts green"
if ! go build ./... 2>/dev/null; then
  uat_fail "fixture should build initially; it doesn't — fixture is broken"
  uat_finish "$UAT_ID"
  exit 1
fi
uat_ok "fixture starts green"

echo "==> $UAT_ID — driving cogo with the canned prompt"
uat_run_cogo "$PROMPT" "$PREFIX" || true

echo "==> $UAT_ID — checking the rename"
# The rename must have happened in the declaration + every call site.
uat_assert_file_contains gate/gate.go 'func \(g \*Gate\) AuthorizeBash'
uat_assert_file_contains main.go 'AuthorizeBash'
uat_assert_file_contains service/service.go 'AuthorizeBash'
uat_assert_file_contains gate/gate_test.go 'AuthorizeBash'

# The OLD name must be gone everywhere (no lingering CheckBash).
for f in gate/gate.go gate/gate_test.go service/service.go main.go; do
  uat_assert_file_lacks "$f" 'CheckBash'
done

# CheckGeneric must NOT have been renamed (over-broad-replace regression).
uat_assert_file_contains gate/gate.go 'func \(g \*Gate\) CheckGeneric'
uat_assert_file_contains service/service.go 'CheckGeneric'
uat_assert_file_lacks gate/gate.go 'AuthorizeGeneric'
uat_assert_file_lacks service/service.go 'AuthorizeGeneric'

echo "==> $UAT_ID — build + tests"
uat_assert_build
uat_assert_tests ./...

echo "==> $UAT_ID — no orphan tmp files"
if ls tmp_*.go tmp_*.go.* 2>/dev/null | grep -q .; then
  uat_fail "agent left orphan tmp files:"
  ls tmp_*.go tmp_*.go.* >&2
else
  uat_ok "no orphan tmp_*.go files at repo root"
fi

uat_finish "$UAT_ID"
