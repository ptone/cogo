// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/go-steer/cogo/internal/config"
	"github.com/go-steer/cogo/internal/permissions"
)

// goExprFprint renders an ast.Expr to w using a fresh empty fset.
// Used by exprString and signatureOfFunc to format expressions back
// to Go source. Returns the printer's error; callers fall back to
// the type name on failure.
func goExprFprint(w io.Writer, e ast.Expr) error {
	return printer.Fprint(w, token.NewFileSet(), e)
}

// goSymbolFindArgs is what the agent passes when calling
// go_symbol_find.
type goSymbolFindArgs struct {
	Name  string `json:"name" jsonschema:"identifier name to find — e.g. \"Gate\", \"NewPolicy\", \"CheckBash\". Exact match by default; set match:\"prefix\" or \"substring\" to widen. Method receivers are searched too (e.g. \"Check\" matches Gate.CheckBash)."`
	Path  string `json:"path,omitempty" jsonschema:"directory to walk (default: current). The walk respects the same skip set as glob: .git/.svn/.hg/node_modules/vendor."`
	Match string `json:"match,omitempty" jsonschema:"matching mode: \"exact\" (default), \"prefix\", or \"substring\"."`
}

// goSymbolHit is one matched definition site.
type goSymbolHit struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Kind      string `json:"kind"` // "func" | "method" | "type" | "var" | "const" | "interface"
	Name      string `json:"name"`
	Receiver  string `json:"receiver,omitempty"`  // for methods, e.g. "*Gate"
	Signature string `json:"signature,omitempty"` // best-effort one-line summary
}

// goSymbolFindResult is the structured output.
type goSymbolFindResult struct {
	Hits      []goSymbolHit `json:"hits"`
	Truncated bool          `json:"truncated,omitempty"`
}

// goSymbolFindFunc returns the ADK functiontool handler for
// go_symbol_find. AST-based: parses every .go file under Path and
// records top-level declarations (funcs, methods, types, vars,
// consts, interfaces) whose Name matches per the requested mode.
//
// Why structured rather than grep:
//   - `grep -rn "type Foo "` misses multiline declarations and
//     hits Foo references in comments / strings / test fixtures.
//   - The agent gets {path, line, kind, signature} — directly
//     usable to read_file at the matched line, or edit_file using
//     a snippet from the signature.
//   - Distinguishes definitions from references; grep can't.
//   - Knows about method receivers ("Gate.CheckBash") which grep
//     can't express without a complex regex.
//
// Notes:
//   - Skips _test.go files unless Path explicitly ends in _test.go
//     or the directory is named "*_test". Most "where is X defined"
//     queries want the production symbol; tests can be searched
//     explicitly.
//   - Parse errors on individual files are tolerated (skip the file,
//     continue) so a single broken file doesn't abort the walk.
//   - Output capped via cfg.ToolOutput.PerTool["go_symbol_find"];
//     default 500 hits.
func goSymbolFindFunc(gate *permissions.Gate, cfg *config.Config) functiontool.Func[goSymbolFindArgs, goSymbolFindResult] {
	return func(_ tool.Context, in goSymbolFindArgs) (goSymbolFindResult, error) {
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return goSymbolFindResult{}, fmt.Errorf("go_symbol_find: name is required")
		}
		match := in.Match
		if match == "" {
			match = "exact"
		}
		switch match {
		case "exact", "prefix", "substring":
		default:
			return goSymbolFindResult{}, fmt.Errorf("go_symbol_find: unknown match mode %q (want exact|prefix|substring)", match)
		}
		root := in.Path
		if root == "" {
			root = "."
		}
		absRoot, err := absolutize(root)
		if err != nil {
			return goSymbolFindResult{}, err
		}
		if err := gate.CheckFileRead(context.Background(), "go_symbol_find", absRoot); err != nil {
			return goSymbolFindResult{}, err
		}
		caps := capsFor(cfg, "go_symbol_find", 0, 500)
		matcher := goSymbolMatcher(name, match)
		fset := token.NewFileSet()
		var hits []goSymbolHit
		truncated := false
		walkErr := filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if path != absRoot && skippedDirs[d.Name()] {
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			if filepath.Ext(path) != ".go" {
				return nil
			}
			// Skip _test.go unless the user explicitly asks for one.
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if err := gate.CheckFileRead(context.Background(), "go_symbol_find", path); err != nil {
				return nil
			}
			file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if perr != nil {
				return nil // broken file; skip
			}
			for _, decl := range file.Decls {
				more := collectDeclHits(fset, path, decl, matcher)
				hits = append(hits, more...)
				if caps.lines > 0 && len(hits) >= caps.lines {
					hits = hits[:caps.lines]
					truncated = true
					return filepath.SkipAll
				}
			}
			return nil
		})
		if walkErr != nil && walkErr != filepath.SkipAll {
			return goSymbolFindResult{}, fmt.Errorf("go_symbol_find: walk: %w", walkErr)
		}
		return goSymbolFindResult{Hits: hits, Truncated: truncated}, nil
	}
}

// goSymbolMatcher returns the predicate used to test each identifier
// against the user's query. The three modes are exact, prefix, and
// substring; case-sensitive in all cases (Go identifiers are
// case-significant and ambiguity helps no one).
func goSymbolMatcher(name, match string) func(string) bool {
	switch match {
	case "prefix":
		return func(s string) bool { return strings.HasPrefix(s, name) }
	case "substring":
		return func(s string) bool { return strings.Contains(s, name) }
	default:
		return func(s string) bool { return s == name }
	}
}

// collectDeclHits walks one top-level decl and emits a hit for each
// identifier it defines that matches the predicate. Handles funcs
// (with method receivers), type/var/const groups (one hit per spec),
// and interface methods (one hit per method whose name matches).
func collectDeclHits(fset *token.FileSet, path string, decl ast.Decl, match func(string) bool) []goSymbolHit {
	var out []goSymbolHit
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Name == nil {
			return nil
		}
		if !match(d.Name.Name) {
			return nil
		}
		kind := "func"
		recv := ""
		if d.Recv != nil && len(d.Recv.List) > 0 {
			kind = "method"
			recv = exprString(d.Recv.List[0].Type)
		}
		out = append(out, goSymbolHit{
			Path:      path,
			Line:      fset.Position(d.Pos()).Line,
			Kind:      kind,
			Name:      d.Name.Name,
			Receiver:  recv,
			Signature: signatureOfFunc(d, recv),
		})
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				// Emit a hit for the type itself when its name
				// matches.
				if match(s.Name.Name) {
					kind := "type"
					if _, ok := s.Type.(*ast.InterfaceType); ok {
						kind = "interface"
					}
					out = append(out, goSymbolHit{
						Path:      path,
						Line:      fset.Position(s.Pos()).Line,
						Kind:      kind,
						Name:      s.Name.Name,
						Signature: "type " + s.Name.Name + " " + exprString(s.Type),
					})
				}
				// Interface methods can match independently of
				// whether the enclosing type matched — the user
				// might be searching for the method name only.
				if iface, ok := s.Type.(*ast.InterfaceType); ok && iface.Methods != nil {
					for _, m := range iface.Methods.List {
						for _, mname := range m.Names {
							if !match(mname.Name) {
								continue
							}
							out = append(out, goSymbolHit{
								Path:      path,
								Line:      fset.Position(mname.Pos()).Line,
								Kind:      "method",
								Name:      mname.Name,
								Receiver:  s.Name.Name + " (interface)",
								Signature: mname.Name + exprString(m.Type),
							})
						}
					}
				}
			case *ast.ValueSpec:
				for _, n := range s.Names {
					if !match(n.Name) {
						continue
					}
					kind := "var"
					if d.Tok == token.CONST {
						kind = "const"
					}
					out = append(out, goSymbolHit{
						Path: path,
						Line: fset.Position(n.Pos()).Line,
						Kind: kind,
						Name: n.Name,
					})
				}
			}
		}
	}
	return out
}

// signatureOfFunc renders a compact one-line signature for func and
// method declarations: "func Foo(x int) error" or "func (g *Gate)
// CheckBash(ctx context.Context, cmd string) error". Best-effort —
// expressions that printer.Fprint can't render fall back to the raw
// name.
func signatureOfFunc(d *ast.FuncDecl, recv string) string {
	var b strings.Builder
	b.WriteString("func ")
	if recv != "" {
		b.WriteString("(")
		b.WriteString(recv)
		b.WriteString(") ")
	}
	b.WriteString(d.Name.Name)
	if d.Type != nil {
		b.WriteString(exprString(d.Type)[len("func"):]) // strip leading "func" — ast prints "func(...)" for FuncType
	}
	return b.String()
}

// exprString renders an ast.Expr as Go source. The fallback when
// printer fails is the type name; we don't fail the whole walk on a
// formatting hiccup.
func exprString(e ast.Expr) string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	if err := goExprFprint(&b, e); err != nil {
		return fmt.Sprintf("%T", e)
	}
	return b.String()
}
