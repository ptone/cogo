// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

package permissions

import "testing"

func TestPolicy_Match(t *testing.T) {
	t.Parallel()
	p, err := NewPolicy(
		[]string{"bash:git status", "bash:git diff*", "bash:ls *"},
		[]string{"bash:rm -rf*", "bash:sudo *"},
	)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}

	cases := []struct {
		name string
		tool string
		key  string
		want Outcome
	}{
		{"exact allow", "bash", "git status", OutcomeAllow},
		{"prefix allow", "bash", "git diff main..HEAD", OutcomeAllow},
		{"unrelated bash", "bash", "git push", OutcomeUnmatched},
		{"deny wins over allow", "bash", "rm -rf /tmp/x", OutcomeDeny},
		{"sudo deny", "bash", "sudo apt-get update", OutcomeDeny},
		{"different tool not matched", "read_file", "git status", OutcomeUnmatched},
		{"plain ls glob", "bash", "ls -la /tmp", OutcomeAllow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := p.Match(tc.tool, tc.key)
			if got != tc.want {
				t.Errorf("Match(%q,%q) = %v, want %v", tc.tool, tc.key, got, tc.want)
			}
		})
	}
}

// TestPolicy_AddAllow_LiveExtension pins the contract that backs the
// /allow slash command: appending patterns to an existing Policy must
// take effect for subsequent Match calls without re-constructing the
// Policy. Without this, /allow only takes effect on the next session
// (the original bug behind the proactive permission UX work). DO NOT
// delete this test to silence a compile failure — fix AddAllow.
func TestPolicy_AddAllow_LiveExtension(t *testing.T) {
	t.Parallel()
	p, _ := NewPolicy(nil, nil)
	if got := p.Match("bash", "git status"); got != OutcomeUnmatched {
		t.Fatalf("baseline Match = %v, want unmatched", got)
	}
	if err := p.AddAllow([]string{"bash:git *"}); err != nil {
		t.Fatalf("AddAllow: %v", err)
	}
	if got := p.Match("bash", "git status"); got != OutcomeAllow {
		t.Errorf("after AddAllow, Match = %v, want allow", got)
	}
}

func TestPolicy_AddDeny_LiveExtension(t *testing.T) {
	t.Parallel()
	p, _ := NewPolicy([]string{"bash:curl *"}, nil)
	if got := p.Match("bash", "curl example.com"); got != OutcomeAllow {
		t.Fatalf("baseline = %v, want allow", got)
	}
	if err := p.AddDeny([]string{"bash:curl *"}); err != nil {
		t.Fatalf("AddDeny: %v", err)
	}
	if got := p.Match("bash", "curl example.com"); got != OutcomeDeny {
		t.Errorf("after AddDeny, Match = %v, want deny (deny wins)", got)
	}
}

func TestPolicy_AddAllow_Idempotent(t *testing.T) {
	t.Parallel()
	p, _ := NewPolicy([]string{"bash:git *"}, nil)
	if err := p.AddAllow([]string{"bash:git *", "bash:git *"}); err != nil {
		t.Fatalf("AddAllow: %v", err)
	}
	// One allow rule from constructor + dedup of the two added.
	if got := len(p.allow); got != 1 {
		t.Errorf("expected dedup, got %d rules", got)
	}
}

func TestPolicy_AddAllow_BadPatternErrors(t *testing.T) {
	t.Parallel()
	p, _ := NewPolicy(nil, nil)
	if err := p.AddAllow([]string{"bash:foo[bar"}); err == nil {
		t.Fatal("expected error for malformed glob")
	}
	if len(p.allow) != 0 {
		t.Errorf("failed AddAllow must not partially mutate; got %d rules", len(p.allow))
	}
}

func TestPolicy_AnyToolPattern(t *testing.T) {
	t.Parallel()
	p, _ := NewPolicy([]string{"*foo*"}, nil)
	// Bare patterns use filepath.Match semantics, so they're best for
	// non-path keys (commands). Slash-containing keys typically use the
	// tool: prefix form.
	if p.Match("bash", "echo foobar") != OutcomeAllow {
		t.Errorf("any-tool wildcard did not match bash command")
	}
}
