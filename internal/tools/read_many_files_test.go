// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"strings"
	"testing"

	"google.golang.org/adk/tool"

	"github.com/go-steer/cogo/internal/config"
)

func TestReadManyFiles_RequiresInput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fn := readManyFilesFunc(permissiveGate(t, dir), config.DefaultConfig())
	_, err := fn(tool.Context(nil), readManyFilesArgs{})
	if err == nil || !strings.Contains(err.Error(), "paths or pattern") {
		t.Errorf("expected error about missing paths/pattern, got %v", err)
	}
}

func TestReadManyFiles_ExplicitPaths(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pA := writeFile(t, dir, "a.go", "package a\n")
	pB := writeFile(t, dir, "sub/b.go", "package b\n")
	fn := readManyFilesFunc(permissiveGate(t, dir), config.DefaultConfig())

	res, err := fn(tool.Context(nil), readManyFilesArgs{Paths: []string{pA, pB}})
	if err != nil {
		t.Fatalf("read_many_files: %v", err)
	}
	if len(res.Files) != 2 {
		t.Fatalf("got %d files, want 2", len(res.Files))
	}
	gotContents := map[string]string{}
	for _, f := range res.Files {
		gotContents[f.Path] = f.Content
		if f.Error != "" {
			t.Errorf("unexpected error on %s: %s", f.Path, f.Error)
		}
	}
	if gotContents[pA] != "package a\n" || gotContents[pB] != "package b\n" {
		t.Errorf("unexpected contents: %+v", gotContents)
	}
}

func TestReadManyFiles_PatternWalk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package a\n")
	writeFile(t, dir, "b.go", "package b\n")
	writeFile(t, dir, "c.txt", "not go\n")
	fn := readManyFilesFunc(permissiveGate(t, dir), config.DefaultConfig())

	res, err := fn(tool.Context(nil), readManyFilesArgs{Pattern: "*.go", Root: dir})
	if err != nil {
		t.Fatalf("read_many_files: %v", err)
	}
	if len(res.Files) != 2 {
		t.Fatalf("got %d files, want 2 (.go only): %+v", len(res.Files), res.Files)
	}
	for _, f := range res.Files {
		if !strings.HasSuffix(f.Path, ".go") {
			t.Errorf("expected only .go files; got %s", f.Path)
		}
	}
}

// TestReadManyFiles_PathsAndPatternUnion confirms the two input
// channels combine without duplication when they refer to the same
// file. Important because users (and the model) will sometimes supply
// both when they want belt-and-braces coverage.
func TestReadManyFiles_PathsAndPatternUnion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pA := writeFile(t, dir, "a.go", "package a\n")
	_ = writeFile(t, dir, "b.go", "package b\n") // matched only via the pattern walk
	fn := readManyFilesFunc(permissiveGate(t, dir), config.DefaultConfig())

	res, err := fn(tool.Context(nil), readManyFilesArgs{
		Paths:   []string{pA}, // explicit
		Pattern: "*.go",       // also matches a.go + b.go
		Root:    dir,
	})
	if err != nil {
		t.Fatalf("read_many_files: %v", err)
	}
	if len(res.Files) != 2 {
		t.Fatalf("got %d files, want 2 (dedup expected): %+v", len(res.Files), res.Files)
	}
	seen := make(map[string]int)
	for _, f := range res.Files {
		seen[f.Path]++
	}
	for p, n := range seen {
		if n != 1 {
			t.Errorf("path %s appeared %d times; want 1 (dedup broken)", p, n)
		}
	}
}

// TestReadManyFiles_GateDenyReportedPerEntry confirms a single
// denied path doesn't abort the whole batch — the model needs the
// files the gate allowed plus a clear signal that one was refused.
func TestReadManyFiles_GateDenyReportedPerEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	other := t.TempDir()
	pIn := writeFile(t, dir, "in.go", "package in\n")
	pOut := writeFile(t, other, "out.go", "package out\n")
	// scopedGate is allow-mode, restricted to dir, so out-of-scope
	// reads come back as denials rather than silently succeeding (as
	// they would under yolo).
	fn := readManyFilesFunc(scopedGate(t, dir), config.DefaultConfig())

	res, err := fn(tool.Context(nil), readManyFilesArgs{Paths: []string{pIn, pOut}})
	if err != nil {
		t.Fatalf("read_many_files: %v", err)
	}
	if len(res.Files) != 2 {
		t.Fatalf("got %d entries, want 2", len(res.Files))
	}
	var inEntry, outEntry *readManyFileEntry
	for i := range res.Files {
		if res.Files[i].Path == pIn {
			inEntry = &res.Files[i]
		}
		if res.Files[i].Path == pOut {
			outEntry = &res.Files[i]
		}
	}
	if inEntry == nil || inEntry.Content == "" || inEntry.Error != "" {
		t.Errorf("in-scope file should read cleanly; got %+v", inEntry)
	}
	if outEntry == nil || outEntry.Error == "" {
		t.Errorf("out-of-scope file should report Error; got %+v", outEntry)
	}
}

// TestReadManyFiles_BatchTotalCap verifies the batch-level byte
// cap stops the read once total bytes exceed the budget, marking the
// result Truncated. Per-file cap is tighter (256B) for an easy
// fixture; total is 4× that = 1024B. Four files of 300B each will
// blow the total cap after the third.
func TestReadManyFiles_BatchTotalCap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := strings.Repeat("x", 300)
	paths := []string{
		writeFile(t, dir, "a.txt", body),
		writeFile(t, dir, "b.txt", body),
		writeFile(t, dir, "c.txt", body),
		writeFile(t, dir, "d.txt", body),
	}
	cfg := config.DefaultConfig()
	cfg.ToolOutput.PerTool = map[string]config.ToolOutputPerToolCaps{
		"read_many_files": {MaxBytes: 256, MaxLines: 100},
	}
	fn := readManyFilesFunc(permissiveGate(t, dir), cfg)

	res, err := fn(tool.Context(nil), readManyFilesArgs{Paths: paths})
	if err != nil {
		t.Fatalf("read_many_files: %v", err)
	}
	if !res.Truncated {
		t.Errorf("expected batch Truncated=true; got false (files: %d)", len(res.Files))
	}
	if len(res.Files) < 2 || len(res.Files) >= 4 {
		t.Errorf("expected to read 2-3 files before the cap fired; got %d", len(res.Files))
	}
}
