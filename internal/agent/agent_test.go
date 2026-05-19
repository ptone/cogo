// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/go-steer/cogo/internal/testutil"
)

func TestNew_NilModel(t *testing.T) {
	t.Parallel()
	if _, err := New(nil); err == nil {
		t.Fatal("expected error for nil model")
	}
}

// TestDefaultInstruction_HasParallelismBlock pins the
// parallel-execution mandate in the default system prompt
// (docs/gemini-tooling-plan.md item 5). Probe data shows Gemini's
// -customtools variant batches independent tool calls when this rule
// is explicit and refuses to batch when it isn't. If you reword the
// instruction, keep the two load-bearing phrases — REQUIRED and
// "single response turn" — or document why the new wording is
// equivalent. DO NOT delete this test to silence a compile failure;
// fix the instruction instead.
func TestDefaultInstruction_HasParallelismBlock(t *testing.T) {
	t.Parallel()
	musts := []string{
		"TOOL EXECUTION RULES",
		"Parallel execution is REQUIRED",
		"single response turn",
		"read_many_files",
	}
	for _, want := range musts {
		if !strings.Contains(DefaultInstruction, want) {
			t.Errorf("DefaultInstruction missing %q", want)
		}
	}
}

// TestDefaultOptions_UsesDefaultInstruction confirms the constant is
// actually what defaultOptions returns — a separate const that
// happens to read like the instruction but isn't wired in would
// regress silently.
func TestDefaultOptions_UsesDefaultInstruction(t *testing.T) {
	t.Parallel()
	if defaultOptions().instruction != DefaultInstruction {
		t.Errorf("defaultOptions().instruction != DefaultInstruction")
	}
}

// TestWithInstruction_Overrides confirms callers can still replace
// the entire instruction (used by tests + custom agent configs).
func TestWithInstruction_Overrides(t *testing.T) {
	t.Parallel()
	o := defaultOptions()
	WithInstruction("custom")(&o)
	if o.instruction != "custom" {
		t.Errorf("WithInstruction did not override; got %q", o.instruction)
	}
}

// TestWithSystemInstructionPrefix_PrependsToDefault confirms memory
// loading prepends without clobbering the parallelism mandate —
// otherwise AGENTS.md content would erase the parallel-execution
// rules every time a project ships memory.
func TestWithSystemInstructionPrefix_PrependsToDefault(t *testing.T) {
	t.Parallel()
	o := defaultOptions()
	WithSystemInstructionPrefix("PROJECT MEMORY HERE")(&o)
	if !strings.HasPrefix(o.instruction, "PROJECT MEMORY HERE\n\n") {
		t.Errorf("prefix did not lead the instruction; got %q", o.instruction)
	}
	if !strings.Contains(o.instruction, "TOOL EXECUTION RULES") {
		t.Errorf("prefix erased the parallelism mandate")
	}
}

func TestRun_ConcatenatesFinalText(t *testing.T) {
	t.Parallel()
	model := &testutil.FakeModel{
		ModelName: "fake-model",
		Script: []testutil.ScriptedResponse{
			{TextChunks: []string{"Hello, ", "world!"}},
		},
	}
	a, err := New(model, WithName("test_agent"), WithInstruction("be brief"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var partials, completes int
	var assembled strings.Builder
	for event, err := range a.Run(context.Background(), "ping") {
		if err != nil {
			t.Fatalf("event err: %v", err)
		}
		if event.Partial {
			partials++
		}
		if event.TurnComplete {
			completes++
		}
		if event.Content != nil && !event.Partial {
			for _, p := range event.Content.Parts {
				if p.Text != "" {
					assembled.WriteString(p.Text)
				}
			}
		}
	}

	if got := assembled.String(); got != "Hello, world!" {
		t.Errorf("final text = %q, want %q", got, "Hello, world!")
	}
	if partials < 1 {
		t.Errorf("expected at least one Partial event with StreamingModeSSE, got 0")
	}
	if completes < 1 {
		t.Errorf("expected at least one TurnComplete event, got 0")
	}
}

func TestRun_FakeModelCallCount(t *testing.T) {
	t.Parallel()
	model := &testutil.FakeModel{
		ModelName: "fake-model",
		Script:    []testutil.ScriptedResponse{{TextChunks: []string{"hi"}}},
	}
	a, err := New(model)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, err := range a.Run(context.Background(), "ping") {
		if err != nil {
			t.Fatalf("event err: %v", err)
		}
	}
	if got := model.Calls(); got < 1 {
		t.Errorf("model.Calls() = %d, want >= 1", got)
	}
}
