#!/usr/bin/env bash
# Copyright 2026 The Cogo Authors.
# SPDX-License-Identifier: Apache-2.0
#
# Common helpers for dev/uat/uat-*/run.sh scripts. Source this from
# each UAT driver and the umbrella runner:
#
#   . "$(dirname "$0")/../lib/common.sh"
#
# Conventions:
#   - Drivers report results via uat_pass / uat_fail (one of each).
#   - Drivers exit with the appropriate status; the runner aggregates.
#   - Each UAT operates inside a fresh git clone in a temp dir; the
#     uat_setup_clone helper creates it and cd's there. uat_cleanup
#     removes it. Both should always be paired in a trap.

set -u

# uat_cogo_bin resolves the cogo binary the UAT should drive. Honors
# COGO_BIN env var (set by the umbrella runner so all UATs hit the
# same build); falls back to whatever cogo is on PATH.
uat_cogo_bin() {
  if [[ -n "${COGO_BIN:-}" ]]; then
    echo "$COGO_BIN"
  else
    command -v cogo || {
      echo "uat: cogo binary not found (set COGO_BIN or put cogo on PATH)" >&2
      return 2
    }
  fi
}

# uat_repo_url is the source the throwaway clones come from. Default
# is the GitHub origin; UAT_REPO_URL overrides (useful for testing
# against a local development branch, e.g. file:///path/to/cogo).
uat_repo_url() {
  echo "${UAT_REPO_URL:-https://github.com/go-steer/cogo}"
}

# uat_ref is the git ref to check out in the throwaway clone. Default
# is v0.3.0 (the release these UATs were written against); UAT_REF
# overrides.
uat_ref() {
  echo "${UAT_REF:-v0.3.0}"
}

# uat_require_creds bails out with a useful hint when the env lacks
# the auth needed for cogo to reach Gemini/Vertex. Without this the
# binary aborts at config-resolution and traces end up empty (#75
# follow-up: that's how the gap was first noticed).
#
# Safe to call repeatedly; cheap (just env lookups).
uat_require_creds() {
  if [[ -n "${GOOGLE_API_KEY:-}" ]]; then
    return 0
  fi
  if [[ "${GOOGLE_GENAI_USE_VERTEXAI:-}" == "true" && -n "${GOOGLE_CLOUD_PROJECT:-}" ]]; then
    return 0
  fi
  cat >&2 <<'EOF'
uat: no Gemini/Vertex credentials in env. cogo aborts at
     config-resolution without auth and every trace ends up empty.

cogo accepts one of:
  GOOGLE_API_KEY=<key>
      public Gemini API
  GOOGLE_GENAI_USE_VERTEXAI=true GOOGLE_CLOUD_PROJECT=<project>
      Vertex AI (also requires Application Default Credentials —
      e.g. `gcloud auth application-default login`)

If you have a local helper script that exports these, source it
first. For example:
  source ~/scripts/gemini.sh && unset GEMINI_API_KEY
EOF
  return 2
}

# uat_setup_clone makes a fresh clone in a temp dir and cd's there.
# Exports UAT_WORKDIR so uat_cleanup can find it. Refuses to clone
# (and so refuses to do any further work) when creds are missing —
# the throwaway clone is cheap but pointless without auth.
uat_setup_clone() {
  uat_require_creds || return 2
  UAT_WORKDIR="$(mktemp -d -t cogo-uat-XXXXXX)"
  export UAT_WORKDIR
  git clone --quiet --depth 1 --branch "$(uat_ref)" "$(uat_repo_url)" "$UAT_WORKDIR" 2>&1 \
    || { echo "uat: failed to clone $(uat_repo_url) at $(uat_ref)" >&2; return 1; }
  cd "$UAT_WORKDIR" || return 1
}

# uat_cleanup removes the temp dir. Idempotent; safe in a trap.
uat_cleanup() {
  if [[ -n "${UAT_WORKDIR:-}" && -d "$UAT_WORKDIR" ]]; then
    rm -rf "$UAT_WORKDIR"
    unset UAT_WORKDIR
  fi
}

# uat_run_cogo invokes cogo with a prompt and captures stdout, stderr,
# and the debug trace. Args:
#   $1 — path to a prompt file (or use "-" for stdin)
#   $2 — output prefix (e.g. "/tmp/uat-2.1"); .stdout/.stderr/.trace
#        files are written next to it.
# Honors UAT_TIMEOUT (seconds, default 300).
uat_run_cogo() {
  local prompt="$1"
  local prefix="$2"
  local timeout="${UAT_TIMEOUT:-300}"
  local bin
  bin="$(uat_cogo_bin)" || return 2
  local prompt_text
  if [[ "$prompt" == "-" ]]; then
    prompt_text="$(cat)"
  else
    prompt_text="$(cat "$prompt")"
  fi
  timeout --preserve-status "$timeout" "$bin" -p "$prompt_text" --debug \
    > "${prefix}.stdout" 2> "${prefix}.stderr"
  local rc=$?
  # The --debug trace lands on stderr in cogo's default config; some
  # users redirect it elsewhere via .agents/cogo.json. Either way the
  # tool-call lines (→ name, ← name) end up in stderr.
  grep -E '^[→←] ' "${prefix}.stderr" > "${prefix}.trace" 2>/dev/null || true
  return $rc
}

# uat_assert_file_contains <path> <regex> — exits 1 (via uat_fail)
# when the file doesn't contain the regex.
uat_assert_file_contains() {
  local path="$1"; shift
  local pattern="$1"; shift
  local label="${1:-$path contains $pattern}"
  if [[ ! -f "$path" ]]; then
    uat_fail "expected file $path to exist; it doesn't"
    return 1
  fi
  if grep -qE "$pattern" "$path"; then
    uat_ok "$label"
    return 0
  fi
  uat_fail "expected $path to match /$pattern/; it doesn't. First 20 lines:"
  head -20 "$path" >&2
  return 1
}

# uat_assert_file_lacks <path> <regex> — symmetric.
uat_assert_file_lacks() {
  local path="$1"; shift
  local pattern="$1"; shift
  local label="${1:-$path lacks $pattern}"
  if [[ ! -f "$path" ]]; then
    uat_ok "$label (file absent)"
    return 0
  fi
  if grep -qE "$pattern" "$path"; then
    uat_fail "expected $path to NOT match /$pattern/; matches found:"
    grep -nE "$pattern" "$path" >&2 | head -5
    return 1
  fi
  uat_ok "$label"
}

# uat_assert_build — `go build ./...` returns 0 in the cwd.
uat_assert_build() {
  if go build ./... 2>/tmp/uat-build.err; then
    uat_ok "go build ./... passes"
    return 0
  fi
  uat_fail "go build ./... failed:"
  cat /tmp/uat-build.err >&2
  return 1
}

# uat_assert_tests <package-pattern> — `go test <pat>` returns 0.
uat_assert_tests() {
  local pat="${1:-./...}"
  if go test "$pat" 2>/tmp/uat-test.err >/tmp/uat-test.out; then
    uat_ok "go test $pat passes"
    return 0
  fi
  uat_fail "go test $pat failed:"
  tail -40 /tmp/uat-test.err /tmp/uat-test.out >&2
  return 1
}

# uat_assert_trace_has <tool-name> — the trace file shows a → call to
# the named tool at least once.
uat_assert_trace_has() {
  local prefix="$1"; shift
  local tool="$1"; shift
  if grep -qE "^→ ${tool}( |$)" "${prefix}.trace"; then
    uat_ok "trace shows → $tool"
    return 0
  fi
  uat_fail "expected trace to show → $tool; got:"
  cat "${prefix}.trace" >&2 | head -10
  return 1
}

# uat_assert_trace_lacks <tool-name> — the trace file shows NO → call
# to the named tool. Used to confirm the model picked the structured
# alternative.
uat_assert_trace_lacks() {
  local prefix="$1"; shift
  local tool="$1"; shift
  if grep -qE "^→ ${tool}( |$)" "${prefix}.trace"; then
    uat_fail "trace shouldn't show → $tool; matches:"
    grep -E "^→ ${tool}( |$)" "${prefix}.trace" >&2
    return 1
  fi
  uat_ok "trace lacks → $tool"
}

# Result tracking — uat_ok and uat_fail accumulate into _UAT_PASS_COUNT
# and _UAT_FAIL_COUNT. Drivers call uat_finish to emit the summary
# line and exit with the right code.
_UAT_PASS_COUNT=0
_UAT_FAIL_COUNT=0
_UAT_FAILURES=()

uat_ok() {
  _UAT_PASS_COUNT=$((_UAT_PASS_COUNT + 1))
  echo "  ✓ $*"
}

uat_fail() {
  _UAT_FAIL_COUNT=$((_UAT_FAIL_COUNT + 1))
  _UAT_FAILURES+=("$*")
  echo "  ✗ $*" >&2
}

uat_pass_count() { echo "$_UAT_PASS_COUNT"; }
uat_fail_count() { echo "$_UAT_FAIL_COUNT"; }

# uat_finish <uat-id> — emits summary and returns the appropriate
# exit code (0 = all passed, 1 = at least one failed).
uat_finish() {
  local uid="${1:-uat}"
  echo
  if (( _UAT_FAIL_COUNT == 0 )); then
    echo "PASS  $uid  ($_UAT_PASS_COUNT checks)"
    return 0
  fi
  echo "FAIL  $uid  ($_UAT_PASS_COUNT passed, $_UAT_FAIL_COUNT failed)"
  for f in "${_UAT_FAILURES[@]}"; do
    echo "      - $f"
  done
  return 1
}
