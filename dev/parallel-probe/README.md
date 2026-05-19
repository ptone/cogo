# parallel-probe

Diagnostic tool that runs cogo's actual agent loop against scripted
prompts and counts how many tool calls the model emits per assistant
turn. Used to measure the snappiness levers documented in
`docs/gemini-tooling-plan.md`:

- Model variant (default vs. `-customtools`).
- Tool surface (with/without `grep`+`glob`+`read_many_files`,
  with/without `bash`).
- System-prompt parallelism mandate.

The point: ADK's dispatcher already runs multi-call assistant turns
concurrently (`google.golang.org/adk/internal/llminternal/base_flow.go:585`),
so the only thing left to verify is whether the *model* produces those
multi-call responses in the first place. This probe answers that for
cogo specifically.

## Requirements

Real model credentials:

- `GOOGLE_API_KEY` for the public Gemini API, **or**
- `GOOGLE_GENAI_USE_VERTEXAI=true` plus `GOOGLE_CLOUD_PROJECT` and
  Application Default Credentials for Vertex.

The probe burns tokens on every run. **Never invoke from CI.**

## Tasks

| Task | Prompt shape | Expected good behavior |
| --- | --- | --- |
| `multiread` | "Read these 5 files and report line counts." | Single batched turn with 5 `read_file` calls (or one `read_many_files`). Mean batch ≈ 5. |
| `search` | "Find every place where a 'permission' error is constructed." | A handful of turns each batching `grep`/`read_file` calls. Mean batch > 1. |

The two tasks intentionally target different model behaviors:
`multiread` is a known set the model can batch up front; `search` is
exploratory and naturally serial unless the model has been nudged.

## Flags

| Flag | Default | Effect |
| --- | --- | --- |
| `--task=search\|multiread` | `search` | Which scripted prompt to run. |
| `--provider=vertex\|gemini` | (auto-detect) | Override provider selection. |
| `--model=<id>` | (DefaultConfig — currently `gemini-3.1-pro-preview-customtools`) | Override model ID. |
| `--nudge=default\|on\|off` | `default` | `off` strips the TOOL EXECUTION RULES block from the system prompt — used to measure how much of the batching gain is the mandate (item 5) vs. model + tool selection. `on` and `default` are equivalent today; the separation exists so a future regression in `DefaultInstruction` shows up as `on != default`. |
| `--no-bash` | `false` | Drop the `bash` tool — forces the model onto structured tools. |
| `--no-structured` | `false` | Drop `grep`/`glob`/`read_many_files` — forces bash fallback. The baseline measurement for item 2's value. |
| `-v` | `false` | Per-event trace to stderr. |

## Four headline experiments

These map 1-to-1 to the items in `docs/gemini-tooling-plan.md`. Run
each pair back-to-back so you can compare batch histograms directly.

### Item 1 — Default model swap

```sh
# Baseline: pre-v0.3.0 default
go run ./dev/parallel-probe --task=multiread --model=gemini-3.1-pro-preview

# After: v0.3.0 default
go run ./dev/parallel-probe --task=multiread --model=gemini-3.1-pro-preview-customtools
```

Expected: mean batch goes from ~1.0 to ≥3.0. Wall time roughly
halves.

### Item 2 — Structured tools (grep + glob) availability

```sh
# Baseline: bash only
go run ./dev/parallel-probe --task=search --no-structured

# After: structured tools available
go run ./dev/parallel-probe --task=search
```

Expected: mean batch on `search` rises above 1.0 once `grep` is on
the table. Without `grep`, the model emits one `bash grep …` call per
directory, serially.

### Item 5 — Parallelism mandate

```sh
# Without the mandate
go run ./dev/parallel-probe --task=search --nudge=off

# With the mandate (current default)
go run ./dev/parallel-probe --task=search --nudge=default
```

Expected: mean batch rises noticeably on the search task. On
multiread the model already batches without the mandate (the set is
explicit), so use `search` to isolate the mandate's effect.

### Combined — full v0.3.0 vs. nothing

```sh
# Baseline: pre-v0.3.0 everything
go run ./dev/parallel-probe --task=search \
    --model=gemini-3.1-pro-preview \
    --no-structured \
    --nudge=off

# v0.3.0 in full
go run ./dev/parallel-probe --task=search
```

Headline number for the release notes.

## Output

```
=== probe summary ===
task           : search
nudge          : default
no-bash        : false
no-structured  : false
model          : gemini-3.1-pro-preview-customtools (provider=vertex)
elapsed        : 28.4s
tool-call turns: 6
total calls    : 11
mean batch     : 1.83
max batch      : 4
batch histogram:
  1 call × 3
  2 calls × 2
  4 calls × 1

=== final answer (truncated) ===
…
```

Interpret:

- **mean batch** — primary metric. Higher = more concurrency per
  turn. Claude lands around 1.8; pre-v0.3.0 Gemini sits at 1.0.
- **max batch** — does the model ever batch at all? A `max batch =
  1` means the model is fully serial regardless of what the prompt
  asks for.
- **histogram** — distribution. A bimodal `1 call × N, 5 calls × 1`
  is more useful than the mean suggests.

## Why a separate probe rather than `cogo -p`?

`cogo -p "<prompt>"` works for ad-hoc exploration (and prints tool
call summaries to stderr with `-debug`), but the probe gives you:

1. **Stable scripted prompts** — direct comparability across runs.
2. **Batch counting** built in — no `awk` over debug logs.
3. **Lever flags** — `--no-bash`, `--no-structured`, `--nudge=off`
   let you A/B without editing config.
4. **Histogram output** — better signal than a one-line summary.

For routine "does my prompt work" checks, `cogo -p` is fine. For
"did my change move the snappiness needle," use the probe.
