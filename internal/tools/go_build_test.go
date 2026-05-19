// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/tool"

	"github.com/go-steer/cogo/internal/config"
)

// TestParseGoErrors pins the regex that turns `go build` / `go vet`
// output lines into structured {file, line, col, message} triples.
// The agent feeds these directly into edit_file calls — if the
// parser regresses, the v0.4.0 verification-gate behavior that
// builds on top of this loses its file/line targeting and falls
// back to manual error parsing. DO NOT delete this test to silence
// a compile failure; fix the parser instead.
func TestParseGoErrors(t *testing.T) {
	t.Parallel()
	// A realistic combined output: package path, two build errors
	// (with and without column), and an informational line that
	// should be ignored.
	body := `# github.com/go-steer/cogo/internal/tools
./file.go:198:10: undefined: Model
./other.go:42: undefined: Something
something informational that isn't an error`
	got := parseGoErrors(body)
	if len(got) != 2 {
		t.Fatalf("parseGoErrors returned %d findings, want 2:\n%+v", len(got), got)
	}
	if got[0].File != "./file.go" || got[0].Line != 198 || got[0].Col != 10 || !strings.Contains(got[0].Message, "undefined: Model") {
		t.Errorf("first finding = %+v, want {file=./file.go line=198 col=10 message=undefined: Model}", got[0])
	}
	if got[1].File != "./other.go" || got[1].Line != 42 || got[1].Col != 0 {
		t.Errorf("second finding = %+v; expected col-less form", got[1])
	}
}

// TestParseGoTestPackages pins the per-package roll-up parser:
// `ok|FAIL|?  <pkg>  [duration|cached|no-test-files]`. The result
// drives the agent's "which packages are broken" summary.
func TestParseGoTestPackages(t *testing.T) {
	t.Parallel()
	body := `ok      github.com/x/a  0.012s
FAIL    github.com/x/b  0.234s
ok      github.com/x/c  (cached)
?       github.com/x/d  [no test files]`
	got := parseGoTestPackages(body)
	if len(got) != 4 {
		t.Fatalf("parseGoTestPackages = %d entries, want 4:\n%+v", len(got), got)
	}
	if got[0].Status != "ok" || got[0].Package != "github.com/x/a" || got[0].Seconds < 0.011 {
		t.Errorf("entry 0 = %+v", got[0])
	}
	if got[1].Status != "FAIL" || got[1].Package != "github.com/x/b" {
		t.Errorf("entry 1 = %+v", got[1])
	}
	if got[2].Status != "ok" || !got[2].Cached {
		t.Errorf("entry 2 = %+v; expected cached=true", got[2])
	}
	if got[3].Status != "?" || got[3].Package != "github.com/x/d" {
		t.Errorf("entry 3 = %+v", got[3])
	}
}

// TestParseGoTestFailures pins the per-test failure parser. The
// failure capture (up to 20 lines of context) is what makes go_test
// actually useful — the agent gets the assertion message and stack
// without us shipping the full body.
func TestParseGoTestFailures(t *testing.T) {
	t.Parallel()
	body := `--- FAIL: TestFoo (0.01s)
    foo_test.go:42: expected 5, got 3
    foo_test.go:43: stack trace line 1
    foo_test.go:44: stack trace line 2
FAIL
FAIL	github.com/x/b	0.234s`
	got := parseGoTestFailures(body)
	if len(got) != 1 {
		t.Fatalf("parseGoTestFailures = %d entries, want 1:\n%+v", len(got), got)
	}
	if got[0].Test != "TestFoo" {
		t.Errorf("test name = %q, want TestFoo", got[0].Test)
	}
	if !strings.Contains(got[0].Output, "expected 5, got 3") {
		t.Errorf("Output missing failure context; got %q", got[0].Output)
	}
}

// TestGoBuild_EndToEnd builds an actual tiny module and confirms
// the tool surfaces a Passed=true result on a buildable package.
// Skipped when `go` isn't on PATH.
func TestGoBuild_EndToEnd(t *testing.T) {
	// No t.Parallel — os.Chdir is global, can't race with sibling
	// integration tests that also change cwd.
	requireGoBinary(t)
	dir := makeTinyModule(t, "package main\n\nfunc main() {}\n")
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	fn := goBuildFunc(permissiveGate(t, dir), config.DefaultConfig())
	res, err := fn(tool.Context(nil), goBuildArgs{})
	if err != nil {
		t.Fatalf("go_build: %v", err)
	}
	if !res.Passed {
		t.Errorf("expected Passed=true on a valid module; got Errors=%+v Body=%q", res.Errors, res.Body)
	}
	if len(res.Errors) != 0 {
		t.Errorf("unexpected errors on a valid module: %+v", res.Errors)
	}
}

// TestGoBuild_BrokenModule pins the failure path: an unparseable
// Go file produces Passed=false plus at least one structured error
// with file+line populated.
func TestGoBuild_BrokenModule(t *testing.T) {
	// No t.Parallel — see TestGoBuild_EndToEnd.
	requireGoBinary(t)
	dir := makeTinyModule(t, "package main\n\nfunc main() { THIS IS NOT GO\n")
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	fn := goBuildFunc(permissiveGate(t, dir), config.DefaultConfig())
	res, err := fn(tool.Context(nil), goBuildArgs{})
	if err != nil {
		t.Fatalf("go_build returned a hard error rather than structured Passed=false: %v", err)
	}
	if res.Passed {
		t.Errorf("expected Passed=false on a broken module; got Passed=true")
	}
	if len(res.Errors) == 0 {
		t.Errorf("expected at least one structured error; got none. Body:\n%s", res.Body)
	}
	for _, e := range res.Errors {
		if e.File == "" || e.Line == 0 {
			t.Errorf("structured error missing file/line: %+v", e)
		}
	}
}

// makeTinyModule writes a minimal go.mod + a single main.go with the
// supplied content, in a fresh temp dir. Returns the dir. Used by
// the build/test integration tests so we don't depend on cogo's own
// tree compiling.
func makeTinyModule(t *testing.T, mainGo string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module tiny\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
