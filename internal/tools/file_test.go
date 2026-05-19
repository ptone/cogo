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
	"github.com/go-steer/cogo/internal/permissions"
)

// gateFor builds a permissive (yolo) gate scoped to root for use in
// tool unit tests.
func gateFor(t *testing.T, root string) *permissions.Gate {
	t.Helper()
	scope, err := permissions.NewPathScope(root, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return permissions.New(permissions.Options{
		Mode:  permissions.ModeYolo,
		Scope: scope,
	})
}

func TestReadFile_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hi cogo"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	gate := gateFor(t, dir)
	fn := readFileFunc(gate, cfg)
	res, err := fn(tool.Context(nil), readFileArgs{Path: path})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if res.Content != "hi cogo" {
		t.Errorf("content = %q, want %q", res.Content, "hi cogo")
	}
}

func TestReadFile_OutOfScope_Denied(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	other := t.TempDir()
	outside := filepath.Join(other, "x.txt")
	if err := os.WriteFile(outside, []byte("private"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	scope, _ := permissions.NewPathScope(dir, "", nil)
	gate := permissions.New(permissions.Options{
		Mode:  permissions.ModeAllow, // no prompter, no allowlist match → deny
		Scope: scope,
	})
	fn := readFileFunc(gate, cfg)
	_, err := fn(tool.Context(nil), readFileArgs{Path: outside})
	if err == nil {
		t.Fatalf("expected denial for out-of-scope read")
	}
}

func TestWriteFile_AtomicAndContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "out.txt")
	gate := gateFor(t, dir)
	fn := writeFileFunc(gate)
	res, err := fn(tool.Context(nil), writeFileArgs{Path: path, Content: "abc\n"})
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if res.Bytes != 4 {
		t.Errorf("bytes = %d, want 4", res.Bytes)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "abc\n" {
		t.Errorf("on-disk = %q, want %q", string(got), "abc\n")
	}
}

// TestWriteFile_AutoGofmtOnGoFiles pins the v0.3.2 silent-quality
// hook: when write_file receives a .go file, gofmt is applied
// before the file lands on disk. The agent doesn't have to remember
// to format. If you remove this behavior, every Go file the agent
// writes ships unformatted and we re-introduce a class of trivial
// nit churn. DO NOT delete this test to silence a compile failure;
// fix the writer instead.
func TestWriteFile_AutoGofmtOnGoFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	// Deliberately ugly: extra tabs/spaces, mixed indentation.
	// gofmt should collapse to the canonical form.
	ugly := "package main\n\nfunc   main( ){\nfmt.Println(\"hi\")\n}\n"
	gate := gateFor(t, dir)
	fn := writeFileFunc(gate)
	res, err := fn(tool.Context(nil), writeFileArgs{Path: path, Content: ugly})
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if !res.Formatted {
		t.Errorf("expected Formatted=true on a .go file with valid syntax; got false")
	}
	body, _ := os.ReadFile(path)
	got := string(body)
	if got == ugly {
		t.Errorf("on-disk content matches input verbatim; gofmt did not run\n%s", got)
	}
	if !strings.Contains(got, "func main() {") {
		t.Errorf("expected canonical gofmt'd `func main() {`; got\n%s", got)
	}
}

// TestWriteFile_AutoGofmtPreservesInvalidGo confirms that when the
// content can't be parsed (syntax error), the writer falls back to
// writing the user's content unchanged rather than rejecting the
// write or silently dropping the file. The syntax error will surface
// on the next go build / vet — better than masking it with a failed
// write.
func TestWriteFile_AutoGofmtPreservesInvalidGo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.go")
	broken := "package main\n\nfunc main() { THIS IS NOT VALID GO\n"
	gate := gateFor(t, dir)
	fn := writeFileFunc(gate)
	res, err := fn(tool.Context(nil), writeFileArgs{Path: path, Content: broken})
	if err != nil {
		t.Fatalf("write_file should not error on broken Go; got %v", err)
	}
	if res.Formatted {
		t.Errorf("Formatted=true on un-parseable input; want false (gofmt failed cleanly)")
	}
	body, _ := os.ReadFile(path)
	if string(body) != broken {
		t.Errorf("on-disk content was mutated despite failed format; got %q", string(body))
	}
}

// TestWriteFile_NonGoFilesNotFormatted confirms the hook is scoped
// to .go files only — Markdown, JSON, .txt etc. pass through.
func TestWriteFile_NonGoFilesNotFormatted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "README.md")
	// Heavy indentation that gofmt would canonicalize if it ran.
	body := "#   hello\n\nfunc   world( ){\n}\n"
	gate := gateFor(t, dir)
	fn := writeFileFunc(gate)
	res, err := fn(tool.Context(nil), writeFileArgs{Path: path, Content: body})
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if res.Formatted {
		t.Errorf("Formatted=true on a .md file; want false")
	}
	got, _ := os.ReadFile(path)
	if string(got) != body {
		t.Errorf("non-Go file was mutated; got %q want %q", string(got), body)
	}
}

// TestEditFile_AutoGofmtOnGoFiles confirms the edit path also runs
// gofmt — symmetric to write_file. Important because most agent edits
// to Go files come through edit_file (partial replacements), not
// write_file (full rewrites).
func TestEditFile_AutoGofmtOnGoFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	original := "package main\n\nfunc main() {\n\tfmt.Println(\"old\")\n}\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := gateFor(t, dir)
	fn := editFileFunc(gate)
	// Replacement introduces deliberately bad spacing; gofmt should
	// clean it up before the file lands.
	res, err := fn(tool.Context(nil), editFileArgs{
		Path:      path,
		OldString: "fmt.Println(\"old\")",
		NewString: "fmt.Println(  \"new\"  )",
	})
	if err != nil {
		t.Fatalf("edit_file: %v", err)
	}
	if !res.Formatted {
		t.Errorf("expected Formatted=true on a .go file edit; got false")
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), `fmt.Println("new")`) {
		t.Errorf("expected canonical spacing in result; got\n%s", string(body))
	}
}

func TestEditFile_UniqueReplacement(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(path, []byte("alpha BETA gamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := gateFor(t, dir)
	fn := editFileFunc(gate)
	res, err := fn(tool.Context(nil), editFileArgs{Path: path, OldString: "BETA", NewString: "delta"})
	if err != nil {
		t.Fatalf("edit_file: %v", err)
	}
	if res.Replacements != 1 {
		t.Errorf("replacements = %d, want 1", res.Replacements)
	}
	body, _ := os.ReadFile(path)
	if string(body) != "alpha delta gamma\n" {
		t.Errorf("after edit = %q", string(body))
	}
}

func TestEditFile_AmbiguousMatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(path, []byte("foo foo foo"), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := gateFor(t, dir)
	fn := editFileFunc(gate)
	_, err := fn(tool.Context(nil), editFileArgs{Path: path, OldString: "foo", NewString: "bar"})
	if err == nil || !strings.Contains(err.Error(), "appears 3 times") {
		t.Errorf("expected ambiguity error, got %v", err)
	}
}

func TestListDir_SortedEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"b.txt", "a.txt", "c.txt"} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
	}
	cfg := config.DefaultConfig()
	gate := gateFor(t, dir)
	fn := listDirFunc(gate, cfg)
	res, err := fn(tool.Context(nil), listDirArgs{Path: dir})
	if err != nil {
		t.Fatalf("list_dir: %v", err)
	}
	if len(res.Entries) != 3 || res.Entries[0].Name != "a.txt" {
		t.Errorf("entries = %+v", res.Entries)
	}
}
