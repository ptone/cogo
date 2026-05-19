// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"os/exec"
	"strings"
	"testing"

	"google.golang.org/adk/tool"

	"github.com/go-steer/cogo/internal/config"
)

// TestGoDoc_StdlibPackage pins the structured-output contract for
// `go doc fmt`. The classifier must split the body into Package and
// either Signature or Doc — without that split the tool offers no
// value over `bash go doc` (which is the alternative we tell the
// agent not to use in the bash blocklist).
//
// Skipped when `go` isn't on PATH (CI sandboxes without a Go
// toolchain — go_doc shells out to the binary).
func TestGoDoc_StdlibPackage(t *testing.T) {
	t.Parallel()
	requireGoBinary(t)
	dir := t.TempDir()
	fn := goDocFunc(permissiveGate(t, dir), config.DefaultConfig())
	res, err := fn(tool.Context(nil), goDocArgs{Target: "fmt"})
	if err != nil {
		t.Fatalf("go_doc fmt: %v", err)
	}
	if res.Package != "fmt" {
		t.Errorf("Package = %q, want \"fmt\"", res.Package)
	}
	if res.Body == "" {
		t.Errorf("Body should not be empty for package fmt")
	}
}

func TestGoDoc_StdlibSymbol(t *testing.T) {
	t.Parallel()
	requireGoBinary(t)
	dir := t.TempDir()
	fn := goDocFunc(permissiveGate(t, dir), config.DefaultConfig())
	res, err := fn(tool.Context(nil), goDocArgs{Target: "fmt.Println"})
	if err != nil {
		t.Fatalf("go_doc fmt.Println: %v", err)
	}
	if res.Package != "fmt" {
		t.Errorf("Package = %q, want \"fmt\"", res.Package)
	}
	// Signature should mention Println and look like a func declaration.
	if !strings.Contains(res.Signature, "Println") {
		t.Errorf("Signature = %q, expected mention of Println", res.Signature)
	}
	if !strings.HasPrefix(res.Signature, "func ") {
		t.Errorf("Signature = %q, expected to start with \"func \"", res.Signature)
	}
}

// TestGoDoc_UnknownTargetErrors confirms that unknown packages /
// symbols come back as a clear error rather than an empty result —
// without that, the agent might assume the doc lookup succeeded with
// nothing to say.
func TestGoDoc_UnknownTargetErrors(t *testing.T) {
	t.Parallel()
	requireGoBinary(t)
	dir := t.TempDir()
	fn := goDocFunc(permissiveGate(t, dir), config.DefaultConfig())
	_, err := fn(tool.Context(nil), goDocArgs{Target: "this/package/does/not/exist"})
	if err == nil {
		t.Errorf("expected error for unknown target; got nil")
	}
}

func TestGoDoc_RequiresTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fn := goDocFunc(permissiveGate(t, dir), config.DefaultConfig())
	_, err := fn(tool.Context(nil), goDocArgs{Target: ""})
	if err == nil || !strings.Contains(err.Error(), "target is required") {
		t.Errorf("expected required-target error, got %v", err)
	}
}

// TestClassifyGoDoc_FmtPrintln pins the classifier with a synthetic
// `go doc` output so the parsing logic has coverage even when the
// Go toolchain isn't available at test time.
func TestClassifyGoDoc_FmtPrintln(t *testing.T) {
	t.Parallel()
	body := `package fmt // import "fmt"

func Println(a ...any) (n int, err error)
    Println formats using the default formats for its operands and writes to
    standard output. Spaces are always added between operands and a newline is
    appended. It returns the number of bytes written and any write error
    encountered.`
	var res goDocResult
	classifyGoDoc(&res, body)
	if res.Package != "fmt" {
		t.Errorf("Package = %q, want \"fmt\"", res.Package)
	}
	if !strings.HasPrefix(res.Signature, "func Println(") {
		t.Errorf("Signature = %q, expected to start with \"func Println(\"", res.Signature)
	}
	if !strings.Contains(res.Doc, "formats using the default formats") {
		t.Errorf("Doc missing expected sentence; got %q", res.Doc)
	}
}

func requireGoBinary(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go binary not on PATH; skipping go_doc integration test")
	}
}
