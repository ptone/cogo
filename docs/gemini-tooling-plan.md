# Gemini tool-calling: actions cogo can take immediately

We've validated in `../core-agent/dev/parallel-probe/` (probe code) and `../core-agent/TODO.md` ("Gemini tool-calling optimization" section) that the snappiness gap between cogo-on-Gemini and Claude Code is **not** in ADK's dispatcher — ADK already runs multi-call assistant turns concurrently via `sync.WaitGroup` in `internal/llminternal/base_flow.go:585`. The gap is entirely in **model behavior**, governed by:

- Which Gemini model variant we ask for
- What tools we expose
- How we describe those tools
- What our default system instruction says about parallelism

This doc is the cogo-side action plan, ordered by ROI. It runs in parallel with the upcoming core-agent migration (`docs/cogo-core-agent-integration.md`) — we are not blocked by that work.

## Background data (from core-agent probes)

Same probe code (`../core-agent/dev/parallel-probe/main.go`), same machine, same Vertex region:

| Model | Task | Turns | Mean batch | Wall | Correct |
|---|---|---:|---:|---:|---|
| `gemini-3.1-pro-preview`              | search    | 19 | 1.00 | 1m37s  | ❌ |
| `gemini-3.1-pro-preview-customtools`  | multiread |  2 | **3.00** | 17.8s | ✅ |
| `gemini-3.1-pro-preview-customtools`  | search    | 27 | 1.00 | 3m22s  | ❌ |
| `claude-opus-4-7`                     | search    | 17 | **1.76** | 1m29s | ✅ |

Headline: Claude batches natively (~1.8 calls/turn) without prompting. Vanilla Gemini never batches (0/65 turns). The `-customtools` Vertex variant batches on known-set workflows (5 parallel `read_file` calls in one turn) but not on open-ended search. Three load-bearing patterns we lifted from `google-gemini/gemini-cli` source:

1. Parallelism mandate in the system prompt (`packages/core/src/prompts/snippets.ts`)
2. Tool descriptions that explicitly say "PREFERRED over `run_shell_command`"
3. A `read_many_files` batch tool — Gemini handles one tool taking a list better than N parallel calls

## cogo-specific deltas vs core-agent

cogo's situation on Gemini is **worse than core-agent's** for two reasons that aren't visible in the probe data above:

1. **cogo has no `grep` or `glob`** in `internal/tools/`. core-agent shipped both in `tools/grep.go` and `tools/glob.go`. Without them, cogo's only code-investigation primitive is `bash`, which means the bash-preference problem isn't a model bias to fight — it's forced. Every grep on Gemini will be a separate `bash` turn.
2. **cogo's default-model knowledge is duplicated** across `internal/config/config.go:161`, `internal/initcmd/wizard.go:83-84` (placeholder + initial value), `internal/initcmd/silent_test.go`, and `internal/config/discovery_test.go:140`. Any default-model change has to touch all four. core-agent's was 5 files; cogo's is at least 4 in the same repo.

## Action items, ordered by ship-by-itself ROI

### 1. Switch default Gemini model to `gemini-3.1-pro-preview-customtools`

**Files:** `internal/config/config.go:161`, `internal/initcmd/wizard.go:83-84`, `internal/initcmd/silent_test.go:61,66`, `internal/config/discovery_test.go:140`, plus the model field in any `.agents/config.json` examples and the equivalent rows in `docs/site/content/docs/` if cogo has user-visible docs that pin a default.

**What:** Same change shape as core-agent's already-shipped one — see `../core-agent/CHANGELOG.md` "Unreleased / Changed" and the diff at `git -C ../core-agent log -1 -- config/config.go docs/site/content/docs/`.

**Why immediate:** One-line behavioral change per file, same price, same context window. Unlocks parallel `read_file` batching on the customtools variant for every cogo-on-Gemini user.

**Effort:** ~30 minutes including doc updates and rerunning `dev/ci/presubmits/`.

**Verification:** After shipping, point `../core-agent/dev/parallel-probe/` at the cogo binary path (probe needs `--cwd=../cogo` flag added — see item 6) and confirm `multiread` shows mean batch ≥ 3.

### 2. Add `grep` and `glob` tools to `internal/tools/`

**Files:** new `internal/tools/grep.go`, `internal/tools/glob.go`, `internal/tools/grep_test.go`, `internal/tools/glob_test.go`; modify `internal/tools/register.go` to wire them in.

**What:** Port from `../core-agent/tools/grep.go` and `../core-agent/tools/glob.go` — both are stdlib-only (RE2 regex + `filepath.WalkDir`), no new dependencies. Use cogo's existing `gate_wrap.go` and `truncate.go` instead of core-agent's. The design rationale is documented in `../core-agent/docs/tools-plan.md` ("Glob + grep built-in tools") and applies unchanged here.

**Why critical for cogo:** Without these, cogo's only investigation primitive is `bash`, and every grep on Gemini becomes a separate single-tool turn. The probe data above understates how slow cogo is today because core-agent at least had `grep` available — cogo doesn't.

**Effort:** ~half a day including tests. The implementation files are ~200-250 lines each, mostly mirroring core-agent.

**Verification:** Run the equivalent of `../core-agent/dev/parallel-probe/ --task=search` against the cogo loop and check whether mean batch exceeds 1.0 once grep is on the table.

### 3. Add a parallelism mandate to cogo's default system instruction

**Files:** find cogo's default system-instruction string (likely `internal/agent/agent.go` or a `prompt` constant near it — grep for `DefaultInstruction` or the existing baseline prompt).

**What:** Steal the gemini-cli phrasing roughly verbatim:

> Tools execute in parallel by default. Execute multiple independent tool calls in parallel when feasible (searching, reading files, independent shell commands, or editing different files). When investigating code, if you need to read multiple files or grep multiple directories, issue all the tool calls in a single response — do not execute them one by one.

**Why:** Helps both Claude (small marginal effect, ~1.76 → 1.82 in our probes) and Gemini-customtools meaningfully on edge cases. The user-shared gist hammered this point: explicit parallelism rules outperform implicit hints.

**Effort:** ~30 minutes including a unit test that asserts the substring appears in the assembled instruction.

### 4. Add a `read_many_files` tool

**Files:** new `internal/tools/read_many_files.go` and test.

**What:** Takes `paths: []string` (explicit list) **and/or** `pattern: string` (glob), returns `{path → content}` with per-file truncation via existing `truncate.go`. Honors `gate_wrap.go` per path. Gemini-cli's description is at `packages/core/src/tools/definitions/read_many_files.ts` if you want to mirror the exact tool-description wording — they explicitly emphasize *"useful when the user's query implies needing the content of several files simultaneously for context, analysis, or summarization."*

**Why:** The probe data shows Gemini-customtools batches `read_file × N` in a single turn on known-set tasks, but a native batch tool is preferable for context-window efficiency and matches the pattern gemini-cli has invested in. Belt-and-braces.

**Effort:** ~half a day.

### 5. Rewrite `grep` and `read_file` tool descriptions to demote `bash`

**Files:** wherever tool descriptions live (in cogo today, just `internal/tools/bash.go`, `file.go`; after item 2, also `grep.go`, `glob.go`).

**What:** Mirror gemini-cli's phrasing: append *"PREFERRED over `bash grep` due to honoring the permission gate, better performance, and automatic output limiting"* to the `grep` description, and *"PREFERRED over `bash cat`; honors output truncation and the permission gate"* to `read_file`. Also worth tightening bash's description to discourage it for read-only investigation — e.g., add *"Prefer the structured `grep`, `read_file`, and `list_dir` tools for code investigation; this tool is for actions the structured tools cannot perform."*

**Why:** Probe data: customtools variant still picked bash 15/27 times on search. Even with the right model, tool descriptions matter.

**Effort:** ~15 minutes.

### 6. Optional: `GEMINI_SYSTEM_MD`-style override

**Files:** cogo's instruction-loading path (whatever assembles AGENTS.md / GEMINI.md / CLAUDE.md today).

**What:** Honor `GEMINI_SYSTEM_MD=<path>` as an env-var override for the default system instruction, with the file's contents replacing (not appending to) the assembled prompt. The reverse pattern — file in `.agents/system.md` overriding the binary's default — also has value.

**Why:** Lets per-project consumers tune cogo without forking. Verified to exist in gemini-cli at `docs/cli/system-prompt.md`. Lower priority than 1-5 because the AGENTS.md hierarchy already covers most cases.

**Effort:** ~half a day if the loader has a clean injection point; longer if not.

## Deferred to v0.4.0

These come up in `../core-agent/tighten-the-loop.md`. They are deliberately out of v0.3.0 scope (which is narrowly about closing the snappiness gap on Gemini, evidenced by probe data) but they ARE committed for the v0.4.0 batch. The framing shifts from "Gemini optimization" to "coding-assistant quality floor."

Ordered by ROI:

- **Post-edit verification gate** (highest priority) — After any code edit, automatically run `go vet ./...` plus targeted tests; if either fails, inject the error back into the loop before yielding control. Self-healing edit cycle. Not a Gemini optimization — it's a correctness floor for any coding assistant. Without it, agents push broken code and don't notice until the next compile. Reference impl sketch in `../core-agent/tighten-the-loop.md:1432` (`PostEditVerification`).

- **Workspace shadow / file journal** — Stateful in-memory cache of file contents, SHA-keyed so external changes are detected. Tool reads route through it; "currently-open files" gets injected into the system prompt. Eliminates redundant re-reads on multi-turn sessions. Reference impl sketch at `../core-agent/tighten-the-loop.md:1155` (`WorkspaceShadow` / `ManagedFile`).

- **`view_file_outline`** — Uses `go/parser` (or tree-sitter for non-Go) to return only struct/interface/method/function names without bodies. Token-efficiency win on large files; lets the agent navigate structure cheaply. Reference impl sketch at `../core-agent/tighten-the-loop.md:83` and the language-agnostic fallback at `:372`.

## Explicitly not building

- **Parallel dispatcher** — ADK already does this; verified at `internal/llminternal/base_flow.go:585`. Do not reimplement.

## Cross-project note

When cogo migrates to core-agent (per `docs/cogo-core-agent-integration.md`), items 1, 2, 3, 4 of this plan will already be in core-agent (1 is shipped; 2-4 are tracked at `../core-agent/TODO.md`). Items 5 and 6 are independent and should ship to whichever loop owns the tool descriptions / instruction loader at the time. Keep the two TODO lists in sync until the migration completes.
