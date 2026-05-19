# dev/uat — scripted user acceptance tests

Mechanically-runnable form of the UATs in `docs/v0.3.0-uat.md`. Each
test sets up its conditions (fresh git clone of cogo, optionally a
planted bug), invokes the cogo binary with a canned prompt, then
verifies the agent's edits against pass criteria via grep, build,
and test checks.

Use this when you want to compare cogo versions ("did v0.3.1 fix the
v0.4.0 gap UAT-3.1 surfaced?") without re-running 11 prompts by hand.

## Requirements

- Gemini/Vertex credentials in the environment:
  - `GOOGLE_API_KEY` for the public Gemini API, **or**
  - `GOOGLE_GENAI_USE_VERTEXAI=true` plus `GOOGLE_CLOUD_PROJECT` and
    Application Default Credentials for Vertex.
- A `cogo` binary on `PATH`, or `COGO_BIN=/path/to/cogo` set.
- `git`, `bash`, `go`, `grep`, `sed`, `timeout`.
- Real network access (the agent makes live API calls). Burns tokens
  on every run — never invoke from CI.

## Usage

```sh
# Run all UATs against whatever cogo is on PATH
dev/uat/runner.sh

# Run a subset (prefix match)
dev/uat/runner.sh uat-2.1 uat-3.4

# Drive a specific binary
COGO_BIN=$(pwd)/cogo dev/uat/runner.sh

# Test a non-default version (or a local branch)
UAT_REF=v0.3.1 dev/uat/runner.sh
UAT_REPO_URL="file://$(pwd)" UAT_REF=feat/my-branch dev/uat/runner.sh

# Per-UAT timeout (default 300 s)
UAT_TIMEOUT=600 dev/uat/runner.sh uat-3.3

# Exercise the permission gate instead of bypassing it (rare;
# UATs aren't gate tests)
UAT_NO_YOLO=1 dev/uat/runner.sh uat-2.1

# Keep the throwaway workdir for post-mortem inspection (useful
# when an assertion fails and you want to see what the agent
# actually did)
UAT_KEEP_WORKDIR=1 dev/uat/runner.sh uat-2.1
# → "uat: keeping workdir for inspection: /tmp/cogo-uat-XXXXXX"
```

Each UAT runs inside a throwaway clone in a temp dir; your working
tree is never modified. Captured stdout / stderr / tool trace land at
`/tmp/<uat-id>.{stdout,stderr,trace}` for post-mortem.

## Why `--yolo` is the default

The harness invokes cogo with `--yolo` by default. Without it, the
throwaway clone has no `.agents/cogo.json`, so the gate runs in
`ask` mode + there's no prompter in headless mode, so
`write_file` / `edit_file` silently deny. The agent then improvises
with `bash awk '...' > file` / `bash cat <<EOF > file` workarounds
that leave orphan files breaking the build (the original failure
mode in #75). UATs are testing the agent's CODE behavior, not the
gate — `internal/permissions/*_test.go` exists for the gate.

If you explicitly need to exercise the gate (e.g. a UAT that
asserts on permission-denied behavior), set `UAT_NO_YOLO=1`.

## What's here

| UAT | Tier | What it tests |
| --- | --- | --- |
| `uat-2.1-add-version` | Targeted edit | Add a `/version` slash command across `commands.go`, `palette.go`, `update.go`. Build + tests must pass. |
| `uat-2.2-fix-bug` | Targeted edit | Plant a deliberate nil-check inversion in `tools.Build`; agent must locate and fix it surgically (≤ 5 line diff). |
| `uat-3.1-rename-withtools` | "Where it embarrasses itself" | Rename `WithTools` → `WithToolList` across all call sites. Headline measurement: did the agent run `go build` after to verify? (Pre-v0.4.0: typically no — this UAT exists to surface that gap.) |
| `uat-3.3-cross-package-field` | "Where it embarrasses itself" | Add a `Description` field to `PromptRequest`; surface in TUI modal; update every constructor (including test fixtures). Easy to miss test-only callers. |
| `uat-3.4-redis-hallucination` | "Where it embarrasses itself" | False-premise prompt ("why does cogo use Redis"). Pass = explicit denial + no fabricated code. |

The UATs from `docs/v0.3.0-uat.md` that aren't scripted yet are the
qualitative ones (Tier 1 explanations, Tier 3.2 multi-turn TUI, Tier
4 long-running) — they're harder to mechanically verify and benefit
from human review. Add them here if your verification needs evolve.

## Adding a new UAT

```
dev/uat/uat-X.Y-short-name/
├── prompt.txt        # the canned prompt sent to cogo
├── plant.sh          # OPTIONAL: pre-step that mutates the clone
│                       (e.g. introducing a deliberate bug)
└── run.sh            # the driver — see the existing ones as templates
```

Each `run.sh` should:

1. `set -u` and source `lib/common.sh`.
2. `trap uat_cleanup EXIT` and call `uat_setup_clone`.
3. Optionally apply the plant.
4. Call `uat_run_cogo <prompt-file> <prefix>`.
5. Call `uat_assert_*` helpers to score the agent's edits.
6. End with `uat_finish "<uat-id>"`.

The umbrella runner picks the new directory up automatically.

## Interpreting results

- A UAT that **passes** says cogo handled the task within v0.3.0's
  envelope.
- A UAT that **fails by design** (UAT-3.1, UAT-3.3) surfaces a
  v0.4.0 gap — each failure is a candidate acceptance criterion for
  the v0.4.0 post-edit verification gate / workspace shadow work.
- A UAT that **fails unexpectedly** (UAT-2.1, UAT-2.2, UAT-3.4) is
  a real regression. Investigate.

Captured traces under `/tmp/<uat-id>.trace` show the exact tool calls
the agent made — grep for `→ grep`, `→ read_many_files`, `→ bash` to
characterize tool-choice quality.
