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

// TestGoSymbolFind_Exact pins the canonical use case: agent asks
// "where is `Gate` defined?" and gets {path, line, kind} pointing
// at the type declaration. If this regresses, the v0.3.2 win over
// `grep -rn "type Gate"` (which catches comments, references, test
// fixtures) is gone. DO NOT delete this test to silence a compile
// failure; fix the symbol finder instead.
func TestGoSymbolFind_Exact(t *testing.T) {
	t.Parallel()
	dir := writeSymbolFixture(t)
	fn := goSymbolFindFunc(permissiveGate(t, dir), config.DefaultConfig())
	res, err := fn(tool.Context(nil), goSymbolFindArgs{Name: "Gate", Path: dir})
	if err != nil {
		t.Fatalf("go_symbol_find: %v", err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("expected 1 hit for type Gate; got %d:\n%+v", len(res.Hits), res.Hits)
	}
	h := res.Hits[0]
	if h.Kind != "type" || h.Name != "Gate" {
		t.Errorf("hit kind/name = %s/%s; want type/Gate", h.Kind, h.Name)
	}
	if !strings.HasSuffix(h.Path, "gate.go") {
		t.Errorf("hit path = %s; want gate.go", h.Path)
	}
}

func TestGoSymbolFind_Method(t *testing.T) {
	t.Parallel()
	dir := writeSymbolFixture(t)
	fn := goSymbolFindFunc(permissiveGate(t, dir), config.DefaultConfig())
	res, err := fn(tool.Context(nil), goSymbolFindArgs{Name: "CheckBash", Path: dir})
	if err != nil {
		t.Fatalf("go_symbol_find: %v", err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("expected 1 hit; got %d:\n%+v", len(res.Hits), res.Hits)
	}
	h := res.Hits[0]
	if h.Kind != "method" {
		t.Errorf("kind = %q, want \"method\"", h.Kind)
	}
	if h.Receiver != "*Gate" {
		t.Errorf("receiver = %q, want \"*Gate\"", h.Receiver)
	}
	if !strings.HasPrefix(h.Signature, "func (*Gate) CheckBash") {
		t.Errorf("signature = %q; expected to start with \"func (*Gate) CheckBash\"", h.Signature)
	}
}

func TestGoSymbolFind_Interface(t *testing.T) {
	t.Parallel()
	dir := writeSymbolFixture(t)
	fn := goSymbolFindFunc(permissiveGate(t, dir), config.DefaultConfig())
	res, err := fn(tool.Context(nil), goSymbolFindArgs{Name: "Prompter", Path: dir})
	if err != nil {
		t.Fatalf("go_symbol_find: %v", err)
	}
	// Should find the interface type + the AskApproval method declared inside.
	var sawType, sawMethod bool
	for _, h := range res.Hits {
		if h.Kind == "interface" && h.Name == "Prompter" {
			sawType = true
		}
	}
	if !sawType {
		t.Errorf("missing interface hit for Prompter; got %+v", res.Hits)
	}
	res2, err := fn(tool.Context(nil), goSymbolFindArgs{Name: "AskApproval", Path: dir})
	if err != nil {
		t.Fatalf("go_symbol_find AskApproval: %v", err)
	}
	for _, h := range res2.Hits {
		if h.Kind == "method" && h.Name == "AskApproval" && strings.Contains(h.Receiver, "Prompter") {
			sawMethod = true
		}
	}
	if !sawMethod {
		t.Errorf("missing interface-method hit for Prompter.AskApproval; got %+v", res2.Hits)
	}
}

func TestGoSymbolFind_PrefixMatch(t *testing.T) {
	t.Parallel()
	dir := writeSymbolFixture(t)
	fn := goSymbolFindFunc(permissiveGate(t, dir), config.DefaultConfig())
	res, err := fn(tool.Context(nil), goSymbolFindArgs{Name: "Check", Match: "prefix", Path: dir})
	if err != nil {
		t.Fatalf("go_symbol_find: %v", err)
	}
	// Should match CheckBash + CheckGeneric (both prefixed "Check").
	names := map[string]bool{}
	for _, h := range res.Hits {
		names[h.Name] = true
	}
	if !names["CheckBash"] || !names["CheckGeneric"] {
		t.Errorf("prefix match should hit both CheckBash and CheckGeneric; got names=%v", names)
	}
}

func TestGoSymbolFind_SkipsTestFiles(t *testing.T) {
	t.Parallel()
	dir := writeSymbolFixture(t)
	// Add a _test.go file declaring a function the search should miss.
	testFile := filepath.Join(dir, "extra_test.go")
	if err := os.WriteFile(testFile, []byte("package fixture\n\nfunc TestOnlyHelper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fn := goSymbolFindFunc(permissiveGate(t, dir), config.DefaultConfig())
	res, err := fn(tool.Context(nil), goSymbolFindArgs{Name: "TestOnlyHelper", Path: dir})
	if err != nil {
		t.Fatalf("go_symbol_find: %v", err)
	}
	if len(res.Hits) != 0 {
		t.Errorf("expected zero hits (_test.go skipped); got %+v", res.Hits)
	}
}

func TestGoSymbolFind_RequiresName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fn := goSymbolFindFunc(permissiveGate(t, dir), config.DefaultConfig())
	_, err := fn(tool.Context(nil), goSymbolFindArgs{Path: dir})
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("expected required-name error, got %v", err)
	}
}

// writeSymbolFixture creates a minimal Go module-like tree with:
//   - gate.go: type Gate + methods CheckBash + CheckGeneric
//   - prompter.go: interface Prompter with AskApproval method
//
// Returns the dir. Used by the multiple finder tests.
func writeSymbolFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gate.go"), []byte(`package fixture

import "context"

type Gate struct {
	mode string
}

func NewGate() *Gate { return &Gate{} }

func (g *Gate) CheckBash(ctx context.Context, cmd string) error {
	return nil
}

func (g *Gate) CheckGeneric(ctx context.Context, tool, key string) error {
	return nil
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prompter.go"), []byte(`package fixture

import "context"

type Prompter interface {
	AskApproval(ctx context.Context, req string) (int, error)
}

type PromptKind int

const (
	KindBash PromptKind = iota
	KindFileWrite
)
`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
