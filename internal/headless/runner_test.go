// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

package headless

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/go-steer/cogo/internal/testutil"
	"github.com/go-steer/cogo/internal/usage"
)

// TestFormatTraceArgs pins the wire format of the `→ <tool>` trace
// line's optional arg summary. Existed in response to issue #75
// where UAT-2.1 surfaced 23 bash calls in a trace, and the tool
// names alone weren't enough to tell which were verification work
// (`go build`) vs. structured-tool replacements (`bash grep ...`).
// If you change the format, update consumers — including dev/uat/
// harness scripts that grep for the leading `→` token. DO NOT delete
// this test to silence a compile failure; fix the formatter
// instead.
func TestFormatTraceArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"empty args collapse cleanly", nil, ""},
		{"empty map also collapses", map[string]any{}, ""},
		{"single string field", map[string]any{"command": "go build ./..."}, ` {"command":"go build ./..."}`},
		{
			"long string gets truncated with single ellipsis",
			map[string]any{"command": strings.Repeat("x", 200)},
			// JSON adds {"command":"…"} wrapper = 13 chars; the cap is
			// 120 so the inner string truncates and the ellipsis lands
			// at the end.
			"", // placeholder; assertion below checks length + suffix
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := formatTraceArgs(tc.args)
			if tc.name == "long string gets truncated with single ellipsis" {
				if !strings.HasSuffix(got, "…") {
					t.Errorf("expected trailing ellipsis on truncation; got %q", got)
				}
				// Leading space + truncated payload + ellipsis. The
				// payload should be no longer than traceArgsMaxBytes.
				if got != "" && len(got)-len(" ")-len("…") > traceArgsMaxBytes {
					t.Errorf("truncated payload is longer than cap (%d): %q", traceArgsMaxBytes, got)
				}
				return
			}
			if got != tc.want {
				t.Errorf("formatTraceArgs = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRun_StreamsPartialsToStdout(t *testing.T) {
	t.Parallel()
	model := &testutil.FakeModel{
		ModelName: "fake",
		Script: []testutil.ScriptedResponse{
			{TextChunks: []string{"The ", "answer ", "is 4."}},
		},
	}

	var stdout, stderr bytes.Buffer
	code, err := Run(context.Background(), model, "what is 2+2", &stdout, &stderr, nil, usage.Pricing{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != ExitOK {
		t.Errorf("exit code = %d, want %d", code, ExitOK)
	}
	if got := stdout.String(); got != "The answer is 4.\n" {
		t.Errorf("stdout = %q, want %q", got, "The answer is 4.\n")
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should be empty in Slice 1 (no tool summaries), got %q", stderr.String())
	}
}

func TestRun_EmptyPromptIsConfigError(t *testing.T) {
	t.Parallel()
	model := &testutil.FakeModel{}
	var stdout, stderr bytes.Buffer
	code, err := Run(context.Background(), model, "", &stdout, &stderr, nil, usage.Pricing{})
	if err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("expected prompt-required error, got %v", err)
	}
	if code != ExitConfigError {
		t.Errorf("exit code = %d, want %d", code, ExitConfigError)
	}
}

func TestRun_NoFinalNewlineWhenSilent(t *testing.T) {
	t.Parallel()
	// FakeModel with a script entry that has no text chunks → silent.
	model := &testutil.FakeModel{
		Script: []testutil.ScriptedResponse{{}},
	}
	var stdout, stderr bytes.Buffer
	code, err := Run(context.Background(), model, "ping", &stdout, &stderr, nil, usage.Pricing{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != ExitOK {
		t.Errorf("exit code = %d, want %d", code, ExitOK)
	}
	if stdout.Len() != 0 {
		t.Errorf("expected empty stdout, got %q", stdout.String())
	}
}
