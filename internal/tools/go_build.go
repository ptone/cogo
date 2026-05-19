// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/go-steer/cogo/internal/config"
	"github.com/go-steer/cogo/internal/permissions"
)

// goBuildArgs is what the agent passes when calling go_build.
type goBuildArgs struct {
	Pattern string `json:"pattern,omitempty" jsonschema:"Go build pattern (e.g. ./..., ./internal/tui/..., a single import path). Defaults to ./... when empty."`
}

// goVetArgs / goTestArgs mirror goBuildArgs — same pattern field,
// distinct types so the model can't pass test-only options to a vet
// invocation by mistake.
type goVetArgs struct {
	Pattern string `json:"pattern,omitempty" jsonschema:"Go vet pattern (e.g. ./..., ./internal/permissions/...). Defaults to ./... when empty."`
}

type goTestArgs struct {
	Pattern string `json:"pattern,omitempty" jsonschema:"Go test pattern (e.g. ./..., ./internal/tools/...). Defaults to ./... when empty."`
	Run     string `json:"run,omitempty" jsonschema:"optional -run regex (e.g. TestFoo, TestFoo/subtest). Mirrors 'go test -run'."`
	Verbose bool   `json:"verbose,omitempty" jsonschema:"when true, pass -v so per-test output is captured. Default false."`
	Race    bool   `json:"race,omitempty" jsonschema:"when true, pass -race. Adds significant time on big test suites."`
}

// goError is one parsed compile/vet finding. File + Line drive the
// agent's next edit_file call without having to re-grep the message.
type goError struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col,omitempty"`
	Message string `json:"message"`
}

// goBuildResult is the structured output for go_build and go_vet.
// Passed=true means the underlying `go build`/`go vet` invocation
// exited 0; Errors is the parsed list of findings (potentially empty
// on success, potentially populated on failure with file/line/message
// triples).
type goBuildResult struct {
	Passed    bool      `json:"passed"`
	Errors    []goError `json:"errors,omitempty"`
	Body      string    `json:"body,omitempty"` // raw output for context; may be truncated
	Truncated bool      `json:"truncated,omitempty"`
}

// goTestResult is the same shape as goBuildResult plus a parsed test
// summary. The test runner emits per-package and per-test status; we
// surface the per-package roll-up (which is small and stable) and
// stash the raw body for cases the parser can't handle.
type goTestResult struct {
	Passed    bool         `json:"passed"`
	Packages  []goTestPkg  `json:"packages,omitempty"`
	Failures  []goTestFail `json:"failures,omitempty"`
	Body      string       `json:"body,omitempty"`
	Truncated bool         `json:"truncated,omitempty"`
}

type goTestPkg struct {
	Package string  `json:"package"`
	Status  string  `json:"status"` // ok | FAIL | ?
	Seconds float64 `json:"seconds,omitempty"`
	Cached  bool    `json:"cached,omitempty"`
}

type goTestFail struct {
	Package string `json:"package"`
	Test    string `json:"test,omitempty"`
	Output  string `json:"output,omitempty"` // first ~20 lines of the failure block
}

// goBuildFunc returns the ADK functiontool handler for go_build.
// Shells out to `go build <pattern>` with a 5-minute timeout, parses
// any compile errors into structured findings, returns the lot. The
// agent's next move on Passed=false is typically to read the first
// error's File + Line and edit_file to fix it.
func goBuildFunc(gate *permissions.Gate, cfg *config.Config) functiontool.Func[goBuildArgs, goBuildResult] {
	return func(_ tool.Context, in goBuildArgs) (goBuildResult, error) {
		pat := defaultPattern(in.Pattern)
		if err := gate.CheckGeneric(context.Background(), "go_build", pat); err != nil {
			return goBuildResult{}, err
		}
		return runGoBuildOrVet(cfg, "go_build", "build", []string{pat})
	}
}

// goVetFunc returns the ADK functiontool handler for go_vet. Same
// parser as go_build — vet's output uses the same file:line:col:
// prefix as the compiler.
func goVetFunc(gate *permissions.Gate, cfg *config.Config) functiontool.Func[goVetArgs, goBuildResult] {
	return func(_ tool.Context, in goVetArgs) (goBuildResult, error) {
		pat := defaultPattern(in.Pattern)
		if err := gate.CheckGeneric(context.Background(), "go_vet", pat); err != nil {
			return goBuildResult{}, err
		}
		return runGoBuildOrVet(cfg, "go_vet", "vet", []string{pat})
	}
}

// goTestFunc returns the ADK functiontool handler for go_test.
// Captures structured per-package roll-up and per-test failures.
func goTestFunc(gate *permissions.Gate, cfg *config.Config) functiontool.Func[goTestArgs, goTestResult] {
	return func(_ tool.Context, in goTestArgs) (goTestResult, error) {
		pat := defaultPattern(in.Pattern)
		key := pat
		if in.Run != "" {
			key += " -run=" + in.Run
		}
		if err := gate.CheckGeneric(context.Background(), "go_test", key); err != nil {
			return goTestResult{}, err
		}
		args := []string{"test"}
		if in.Verbose {
			args = append(args, "-v")
		}
		if in.Race {
			args = append(args, "-race")
		}
		if in.Run != "" {
			args = append(args, "-run", in.Run)
		}
		args = append(args, pat)
		body, passed, err := runGo(cfg, "go_test", args)
		if err != nil && body == "" {
			// Hard failure (couldn't even invoke `go`); surface it.
			return goTestResult{Passed: false}, err
		}
		caps := capsFor(cfg, "go_test", 1024*1024, 0)
		truncated := false
		if caps.bytes > 0 && len(body) > caps.bytes {
			body = body[:caps.bytes] + "\n…(truncated)"
			truncated = true
		}
		res := goTestResult{
			Passed:    passed,
			Body:      body,
			Truncated: truncated,
		}
		res.Packages = parseGoTestPackages(body)
		res.Failures = parseGoTestFailures(body)
		return res, nil
	}
}

// runGoBuildOrVet is the shared body for go_build / go_vet — both
// run a `go <sub> <pattern>` invocation, capture combined output,
// and parse the standard file:line:col: error format.
func runGoBuildOrVet(cfg *config.Config, toolName, sub string, args []string) (goBuildResult, error) {
	body, passed, err := runGo(cfg, toolName, append([]string{sub}, args...))
	if err != nil && body == "" {
		return goBuildResult{Passed: false}, err
	}
	caps := capsFor(cfg, toolName, 1024*1024, 0)
	truncated := false
	if caps.bytes > 0 && len(body) > caps.bytes {
		body = body[:caps.bytes] + "\n…(truncated)"
		truncated = true
	}
	return goBuildResult{
		Passed:    passed,
		Errors:    parseGoErrors(body),
		Body:      body,
		Truncated: truncated,
	}, nil
}

// runGo invokes `go <args...>` with a 5-minute timeout, returns
// (combined-output, exit-zero?, fork/exec-error). The exit-zero bool
// is meaningful only when err == nil.
func runGo(_ *config.Config, _ string, args []string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	out, err := cmd.CombinedOutput()
	body := strings.TrimRight(string(out), "\n")
	if err == nil {
		return body, true, nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		// Non-zero exit but the binary ran; body holds the diagnostic.
		return body, false, nil
	}
	// Fork/exec error (e.g., `go` not on PATH).
	return body, false, fmt.Errorf("invoking go: %w", err)
}

// goErrorRE matches the standard Go compile/vet error prefix:
//
//	path/to/file.go:LINE:COL: message
//
// or the column-less form some tools emit:
//
//	path/to/file.go:LINE: message
var goErrorRE = regexp.MustCompile(`^(\S+\.go):(\d+)(?::(\d+))?:\s+(.*)$`)

// parseGoErrors walks the combined output of `go build` / `go vet`
// and pulls out every line that matches the file:line[:col]: message
// shape. Lines that don't match (informational, "FAIL" lines,
// package paths) are skipped. The order in the result matches the
// order in the output so the agent can fix top-to-bottom.
func parseGoErrors(body string) []goError {
	var out []goError
	for _, line := range strings.Split(body, "\n") {
		m := goErrorRE.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		lineNo, _ := strconv.Atoi(m[2])
		col := 0
		if m[3] != "" {
			col, _ = strconv.Atoi(m[3])
		}
		out = append(out, goError{
			File:    m[1],
			Line:    lineNo,
			Col:     col,
			Message: m[4],
		})
	}
	return out
}

// goTestPkgRE matches a per-package status line from `go test`:
//
//	ok   github.com/example/pkg   0.123s
//	FAIL github.com/example/pkg   0.456s
//	ok   github.com/example/pkg   (cached)
//	?    github.com/example/pkg   [no test files]
var goTestPkgRE = regexp.MustCompile(`^(ok|FAIL|\?)\s+(\S+)(?:\s+(?:(\d+\.\d+)s|\(cached\)|\[no test files\]))?`)

func parseGoTestPackages(body string) []goTestPkg {
	var out []goTestPkg
	for _, line := range strings.Split(body, "\n") {
		m := goTestPkgRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		pkg := goTestPkg{Status: m[1], Package: m[2]}
		if m[3] != "" {
			pkg.Seconds, _ = strconv.ParseFloat(m[3], 64)
		}
		if strings.Contains(line, "(cached)") {
			pkg.Cached = true
		}
		out = append(out, pkg)
	}
	return out
}

// goTestFailLineRE matches the per-test failure marker:
//
//	--- FAIL: TestFoo (0.01s)
var goTestFailLineRE = regexp.MustCompile(`^\s*--- FAIL: (\S+)`)

// parseGoTestFailures walks the body, captures every `--- FAIL: …`
// line, and gathers the following indented output until the next
// non-indented or `--- FAIL:` line. Captures up to ~20 lines of
// failure context per failure so the agent has enough to act on
// without dragging the full output into the response.
func parseGoTestFailures(body string) []goTestFail {
	var out []goTestFail
	lines := strings.Split(body, "\n")
	currentPkg := ""
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "FAIL\t") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				currentPkg = parts[1]
			}
			continue
		}
		m := goTestFailLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		fail := goTestFail{Package: currentPkg, Test: m[1]}
		var ctx []string
		for j := i + 1; j < len(lines) && len(ctx) < 20; j++ {
			next := lines[j]
			if next == "" {
				ctx = append(ctx, next)
				continue
			}
			if goTestFailLineRE.MatchString(next) || strings.HasPrefix(next, "FAIL\t") || strings.HasPrefix(next, "PASS") || strings.HasPrefix(next, "ok  ") {
				break
			}
			ctx = append(ctx, next)
		}
		if len(ctx) > 0 {
			fail.Output = strings.Join(ctx, "\n")
		}
		out = append(out, fail)
	}
	return out
}

// defaultPattern returns "./..." when the caller didn't supply one.
// Centralized so the three tools agree on the same default.
func defaultPattern(in string) string {
	in = strings.TrimSpace(in)
	if in == "" {
		return "./..."
	}
	return in
}
