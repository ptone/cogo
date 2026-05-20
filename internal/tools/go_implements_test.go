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

// TestGoImplements_ConcreteAndPointer pins the canonical use case:
// a small module declares an interface and two implementations
// (one satisfying via value method set, one via pointer). The tool
// must identify both and label them correctly. This is the
// uniquely-Go capability the v0.3.2 batch adds — `grep` literally
// cannot answer "what implements interface X" because Go
// implementations don't declare which interfaces they satisfy.
// DO NOT delete this test to silence a compile failure; fix the
// types-graph walk instead.
func TestGoImplements_ConcreteAndPointer(t *testing.T) {
	// No t.Parallel — uses os.Chdir to point go/packages at the
	// fixture; can't race with other go/packages-based tests.
	requireGoBinary(t)
	dir := writeImplementsFixture(t)
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	fn := goImplementsFunc(permissiveGate(t, dir), config.DefaultConfig())
	res, err := fn(tool.Context(nil), goImplementsArgs{Interface: "Greeter"})
	if err != nil {
		t.Fatalf("go_implements: %v", err)
	}
	if !strings.HasSuffix(res.Interface, ".Greeter") {
		t.Errorf("Interface = %q, expected to end with \".Greeter\"", res.Interface)
	}

	byType := map[string]goImplementsHit{}
	for _, h := range res.Hits {
		byType[h.Type] = h
	}

	english, ok := byType["English"]
	if !ok {
		t.Fatalf("missing hit for English; got %+v", res.Hits)
	}
	if english.Kind != "concrete" {
		t.Errorf("English kind = %q, want \"concrete\" (value method set satisfies)", english.Kind)
	}

	spanish, ok := byType["Spanish"]
	if !ok {
		t.Fatalf("missing hit for Spanish; got %+v", res.Hits)
	}
	if spanish.Kind != "pointer" {
		t.Errorf("Spanish kind = %q, want \"pointer\" (only *Spanish satisfies)", spanish.Kind)
	}

	// Silent is a struct that does NOT implement the interface; it
	// must not appear in the hits.
	if _, present := byType["Silent"]; present {
		t.Errorf("Silent should not satisfy Greeter; got hit %+v", byType["Silent"])
	}
}

func TestGoImplements_QualifiedName(t *testing.T) {
	// No t.Parallel — os.Chdir.
	requireGoBinary(t)
	dir := writeImplementsFixture(t)
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	fn := goImplementsFunc(permissiveGate(t, dir), config.DefaultConfig())
	res, err := fn(tool.Context(nil), goImplementsArgs{Interface: "greetings.Greeter"})
	if err != nil {
		t.Fatalf("go_implements with qualified name: %v", err)
	}
	if len(res.Hits) < 2 {
		t.Errorf("expected at least 2 hits via qualified name; got %d:\n%+v", len(res.Hits), res.Hits)
	}
}

func TestGoImplements_UnknownInterfaceErrors(t *testing.T) {
	// No t.Parallel — os.Chdir.
	requireGoBinary(t)
	dir := writeImplementsFixture(t)
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	fn := goImplementsFunc(permissiveGate(t, dir), config.DefaultConfig())
	_, err := fn(tool.Context(nil), goImplementsArgs{Interface: "DoesNotExist"})
	if err == nil {
		t.Errorf("expected error for unknown interface; got nil")
	}
}

func TestGoImplements_RequiresInterface(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fn := goImplementsFunc(permissiveGate(t, dir), config.DefaultConfig())
	_, err := fn(tool.Context(nil), goImplementsArgs{})
	if err == nil || !strings.Contains(err.Error(), "interface is required") {
		t.Errorf("expected required-interface error, got %v", err)
	}
}

// writeImplementsFixture creates a tiny module:
//
//	greetings/
//	    go.mod    (module greetings)
//	    api.go    (interface Greeter, struct Silent that doesn't implement it)
//	    english.go (English struct + Greet() method — value receiver)
//	    spanish.go (Spanish struct + (*Spanish).Greet() method — pointer receiver)
//
// English satisfies Greeter via value method set (kind=concrete).
// Spanish only satisfies via *Spanish (kind=pointer).
// Silent has no Greet method (no hit).
func writeImplementsFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module greetings\n\ngo 1.21\n",
		"api.go": `package greetings

type Greeter interface {
	Greet() string
}

type Silent struct{}
`,
		"english.go": `package greetings

type English struct{ Name string }

func (e English) Greet() string { return "Hello, " + e.Name }
`,
		"spanish.go": `package greetings

type Spanish struct{ Name string }

func (s *Spanish) Greet() string { return "Hola, " + s.Name }
`,
	}
	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
