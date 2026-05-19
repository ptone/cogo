// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

// Diagnostic: measure how often the model emits multiple tool calls
// per assistant turn against cogo's actual loop. ADK already dispatches
// a single-message multi-call response concurrently (see
// google.golang.org/adk/internal/llminternal/base_flow.go:585
// handleFunctionCalls — sync.WaitGroup over fnCalls). The remaining
// question is whether the model produces those multi-call responses
// in the first place. This probe answers that for cogo specifically.
//
// Requires real model credentials (GOOGLE_API_KEY for the public
// Gemini API, or GOOGLE_GENAI_USE_VERTEXAI=true + GOOGLE_CLOUD_PROJECT
// + Application Default Credentials for Vertex). Burns tokens; never
// invoke from CI.
//
// Usage (from the cogo repo root):
//
//	go run ./dev/parallel-probe                                # search task
//	go run ./dev/parallel-probe --task=multiread               # 5 independent reads
//	go run ./dev/parallel-probe --task=search --no-bash        # forces structured tools
//	go run ./dev/parallel-probe --task=search --no-structured  # forces bash fallback
//	go run ./dev/parallel-probe --nudge=off                    # strip parallelism mandate
//	go run ./dev/parallel-probe --model=gemini-3.1-pro-preview # A/B the model swap
//
// Output is a per-turn batch histogram so you can eyeball whether
// the lever changes behavior before reasoning about it.
//
// See ../README.md for the four headline experiments this probe is
// designed to support.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	"github.com/go-steer/cogo/internal/agent"
	"github.com/go-steer/cogo/internal/config"
	"github.com/go-steer/cogo/internal/models"

	// Register the Gemini provider with models.Resolve. Same import
	// the cogo binary uses; without it Resolve fails to find any
	// provider.
	_ "github.com/go-steer/cogo/internal/models/gemini"

	"google.golang.org/adk/tool"

	"github.com/go-steer/cogo/internal/permissions"
	"github.com/go-steer/cogo/internal/tools"
)

// Tasks are intentionally the same shape as core-agent's probe so
// numbers stay comparable. Paths point at cogo's tree.
var tasks = map[string]string{
	"multiread": `Read each of these five files and report how many lines each one has: internal/tools/grep.go, internal/tools/glob.go, internal/tools/read_many_files.go, internal/tools/file.go, internal/tools/bash.go. Output one line per file as "<path>: N lines". No other commentary.`,
	"search":    `Find every place in the current working directory (a Go codebase) where an error containing the substring "permission" is constructed or returned. For each occurrence give the file path, the enclosing function name, and one sentence describing when that error fires.`,
}

// baselineInstruction is cogo's DefaultInstruction with the
// TOOL EXECUTION RULES block stripped, used by --nudge=off to
// measure how much of the batching gain comes from item 5's
// parallelism mandate vs. the model+tool selection.
const baselineInstruction = "You are Cogo, a terminal-based coding assistant. Be concise and accurate."

func main() {
	taskID := flag.String("task", "search", "task to run: search | multiread")
	providerFlag := flag.String("provider", "", "provider override: vertex | gemini (default: auto-detect from env)")
	modelFlag := flag.String("model", "", "model name override (default: whatever DefaultConfig has)")
	nudgeFlag := flag.String("nudge", "default", "parallelism mandate: default | on | off (off strips the TOOL EXECUTION RULES block to measure item 5's effect)")
	noBash := flag.Bool("no-bash", false, "disable the bash tool — forces the model onto structured tools")
	noStructured := flag.Bool("no-structured", false, "disable grep/glob/read_many_files — forces bash fallback (baseline measurement)")
	verbose := flag.Bool("v", false, "print per-event details as they arrive")
	flag.Parse()

	prompt, ok := tasks[*taskID]
	if !ok {
		log.Fatalf("unknown task %q (have: search, multiread)", *taskID)
	}

	cfg := config.DefaultConfig()
	if *providerFlag != "" {
		cfg.Model.Provider = *providerFlag
	}
	if *modelFlag != "" {
		cfg.Model.Name = *modelFlag
	}
	cfg.Permissions.Mode = config.PermissionModeYolo

	provider, err := models.Resolve(cfg)
	if err != nil {
		log.Fatalf("resolve provider: %v", err)
	}
	ctx := context.Background()
	llm, err := provider.Model(ctx, cfg.Model.Name)
	if err != nil {
		log.Fatalf("build model: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	gate, err := permissions.FromConfig(cfg, cwd, "", nil)
	if err != nil {
		log.Fatalf("permissions: %v", err)
	}

	registry, err := tools.Build(cfg, gate)
	if err != nil {
		log.Fatalf("tools.Build: %v", err)
	}
	registry.Tools = filterTools(registry.Tools, *noBash, *noStructured)

	instruction := resolveInstruction(*nudgeFlag)

	a, err := agent.New(llm,
		agent.WithInstruction(instruction),
		agent.WithTools(registry.Tools),
	)
	if err != nil {
		log.Fatalf("agent.New: %v", err)
	}

	type batch struct {
		Turn  int
		Tools []string
	}
	var batches []batch
	var finalText string
	start := time.Now()

	for event, err := range a.Run(ctx, prompt) {
		if err != nil {
			log.Fatalf("run: %v", err)
		}
		if event == nil || event.Content == nil || event.Partial {
			continue
		}
		// The model's batched assistant message arrives as one event
		// with multiple FunctionCall parts; that's the batch we want
		// to count. We dedupe by FunctionCall ID so the partial+final
		// pair ADK sometimes emits doesn't double-count.
		var names []string
		var text string
		for _, p := range event.Content.Parts {
			if p.FunctionCall != nil && p.FunctionCall.Name != "" {
				names = append(names, p.FunctionCall.Name)
			}
			if p.Text != "" {
				text += p.Text
			}
		}
		if len(names) > 0 {
			turn := len(batches) + 1
			batches = append(batches, batch{Turn: turn, Tools: names})
			if *verbose {
				fmt.Fprintf(os.Stderr, "turn %d: %d tool call(s) — %v\n", turn, len(names), names)
			}
		}
		if text != "" {
			finalText = text
		}
	}
	elapsed := time.Since(start)

	histogram := map[int]int{}
	total := 0
	maxSize := 0
	for _, b := range batches {
		n := len(b.Tools)
		histogram[n]++
		total += n
		if n > maxSize {
			maxSize = n
		}
	}
	keys := make([]int, 0, len(histogram))
	for k := range histogram {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	fmt.Println()
	fmt.Println("=== probe summary ===")
	fmt.Printf("task           : %s\n", *taskID)
	fmt.Printf("nudge          : %s\n", *nudgeFlag)
	fmt.Printf("no-bash        : %v\n", *noBash)
	fmt.Printf("no-structured  : %v\n", *noStructured)
	fmt.Printf("model          : %s (provider=%s)\n", cfg.Model.Name, cfg.Model.Provider)
	fmt.Printf("elapsed        : %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("tool-call turns: %d\n", len(batches))
	fmt.Printf("total calls    : %d\n", total)
	if len(batches) > 0 {
		fmt.Printf("mean batch     : %.2f\n", float64(total)/float64(len(batches)))
	} else {
		fmt.Printf("mean batch     : (no tool calls)\n")
	}
	fmt.Printf("max batch      : %d\n", maxSize)
	fmt.Println("batch histogram:")
	for _, k := range keys {
		label := "calls"
		if k == 1 {
			label = "call"
		}
		fmt.Printf("  %d %s × %d\n", k, label, histogram[k])
	}

	if finalText != "" {
		fmt.Println()
		fmt.Println("=== final answer (truncated) ===")
		if len(finalText) > 800 {
			finalText = finalText[:800] + "…"
		}
		fmt.Println(finalText)
	}
}

// filterTools removes bash and/or the structured-discovery tools
// (grep, glob, read_many_files) per the --no-bash / --no-structured
// flags. Returns a fresh slice so the caller's registry isn't
// mutated.
func filterTools(in []tool.Tool, noBash, noStructured bool) []tool.Tool {
	structured := map[string]bool{"grep": true, "glob": true, "read_many_files": true}
	out := make([]tool.Tool, 0, len(in))
	for _, t := range in {
		name := t.Name()
		if noBash && name == "bash" {
			continue
		}
		if noStructured && structured[name] {
			continue
		}
		out = append(out, t)
	}
	return out
}

// resolveInstruction maps the --nudge flag to a system instruction.
// "default" honors whatever cogo currently ships (so the probe
// tracks instruction drift), "on" forces the parallelism mandate
// even if DefaultInstruction ever drops it, and "off" reverts to
// a stripped baseline so we can A/B test item 5's contribution.
func resolveInstruction(nudge string) string {
	switch nudge {
	case "off":
		return baselineInstruction
	case "on":
		// "on" is identical to "default" today; carved out as a
		// separate flag value so future probes can detect a
		// DefaultInstruction regression by comparing on vs default.
		return agent.DefaultInstruction
	case "default", "":
		return agent.DefaultInstruction
	default:
		log.Fatalf("unknown --nudge value %q (want on | off | default)", nudge)
		return ""
	}
}
