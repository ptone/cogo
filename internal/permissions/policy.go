// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

package permissions

import (
	"path/filepath"
	"strings"
	"sync"
)

// Policy interprets the allow/deny pattern lists from
// .agents/config.json's `permissions` block.
//
// Pattern grammar (intentionally minimal for V1):
//
//	<tool>:<glob>     — applies only when the request is for <tool>
//	<glob>            — applies to any tool (matched against request key)
//
// `<glob>` is matched with path/filepath.Match, so it understands `*`,
// `?`, and character classes. The "key" of a request depends on the
// tool: for bash it is the command string, for file tools it is the
// resolved absolute path. Wildcards work the same for both.
type Policy struct {
	// mu guards allow/deny so the /allow and /deny slash commands can
	// extend the live policy mid-session without racing with concurrent
	// Match calls from tool handler goroutines. RWMutex because Match
	// is hot (every gated tool call) and additions are rare.
	mu    sync.RWMutex
	allow []rule
	deny  []rule
}

type rule struct {
	tool string // empty = applies to all tools
	pat  string // glob pattern
}

// NewPolicy parses the configured allow/deny patterns. Bad patterns
// fail fast so misconfigurations surface at startup, not when the
// agent first triggers a tool.
func NewPolicy(allow, deny []string) (*Policy, error) {
	a, err := parseRules(allow)
	if err != nil {
		return nil, err
	}
	d, err := parseRules(deny)
	if err != nil {
		return nil, err
	}
	return &Policy{allow: a, deny: d}, nil
}

func parseRules(patterns []string) ([]rule, error) {
	out := make([]rule, 0, len(patterns))
	for _, p := range patterns {
		r := rule{}
		if i := strings.Index(p, ":"); i >= 0 {
			r.tool = p[:i]
			r.pat = p[i+1:]
		} else {
			r.pat = p
		}
		// Validate pattern early.
		if _, err := filepath.Match(r.pat, ""); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// Outcome is the result of consulting the policy. It is used by the
// gate to decide what to do next; it is not the final say (the gate
// also consults the bash denylist, the path scope, and the mode).
type Outcome int

const (
	OutcomeUnmatched Outcome = iota // no allow/deny rule matched
	OutcomeAllow
	OutcomeDeny
)

// Match returns OutcomeDeny if any deny rule matches the request,
// OutcomeAllow if any allow rule matches and no deny rule matches,
// otherwise OutcomeUnmatched. Deny always wins.
func (p *Policy) Match(tool, key string) Outcome {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if matchAny(p.deny, tool, key) {
		return OutcomeDeny
	}
	if matchAny(p.allow, tool, key) {
		return OutcomeAllow
	}
	return OutcomeUnmatched
}

// AddAllow validates and appends patterns to the allow set. Existing
// patterns are skipped (idempotent — the /allow slash command can be
// retried without growing the policy). Bad patterns abort the whole
// call without partial mutation so the on-disk config and the live
// policy stay in sync after a failed parse.
func (p *Policy) AddAllow(patterns []string) error {
	added, err := parseRules(patterns)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range added {
		if containsRule(p.allow, r) {
			continue
		}
		p.allow = append(p.allow, r)
	}
	return nil
}

// AddDeny is the symmetric extension for deny rules.
func (p *Policy) AddDeny(patterns []string) error {
	added, err := parseRules(patterns)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range added {
		if containsRule(p.deny, r) {
			continue
		}
		p.deny = append(p.deny, r)
	}
	return nil
}

func containsRule(rs []rule, r rule) bool {
	for _, existing := range rs {
		if existing.tool == r.tool && existing.pat == r.pat {
			return true
		}
	}
	return false
}

func matchAny(rules []rule, tool, key string) bool {
	for _, r := range rules {
		if r.tool != "" && r.tool != tool {
			continue
		}
		if matchGlob(r.pat, key) {
			return true
		}
	}
	return false
}

// matchGlob tries an exact match first (so the pattern "git status" only
// matches the literal command, not "git statusabc"), then a path-style
// glob via filepath.Match. A trailing `*` is treated as an open prefix
// match too, which is friendlier for command patterns like "git diff*".
func matchGlob(pattern, s string) bool {
	if pattern == s {
		return true
	}
	if strings.HasSuffix(pattern, "*") && !strings.ContainsAny(pattern[:len(pattern)-1], "*?[") {
		return strings.HasPrefix(s, pattern[:len(pattern)-1])
	}
	if ok, _ := filepath.Match(pattern, s); ok {
		return true
	}
	return false
}
