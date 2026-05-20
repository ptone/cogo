// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"fmt"
	"go/types"
	"strings"
	"time"

	"golang.org/x/tools/go/packages"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/go-steer/cogo/internal/config"
	"github.com/go-steer/cogo/internal/permissions"
)

// goImplementsArgs is what the agent passes when calling
// go_implements.
type goImplementsArgs struct {
	Interface string `json:"interface" jsonschema:"interface name (e.g. \"Reader\") or qualified name (e.g. \"io.Reader\"). When unqualified, the loaded packages are searched for the first matching interface type."`
	Path      string `json:"path,omitempty" jsonschema:"directory to load packages from (default: current). The loaded set is determined by 'pattern'."`
	Pattern   string `json:"pattern,omitempty" jsonschema:"package pattern (default: ./...). Tighten this when looking inside a large module to keep load time bounded."`
}

// goImplementsHit is one type that satisfies the target interface.
// Kind distinguishes types that satisfy via value method set
// ("concrete") from types that only satisfy via pointer receiver
// ("pointer" — i.e. *T implements but T doesn't).
type goImplementsHit struct {
	Package string `json:"package"`
	Type    string `json:"type"`
	Kind    string `json:"kind"`
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
}

// goImplementsResult is the structured output.
type goImplementsResult struct {
	Interface string            `json:"interface"`
	Hits      []goImplementsHit `json:"hits"`
	Truncated bool              `json:"truncated,omitempty"`
}

// goImplementsFunc returns the ADK functiontool handler for
// go_implements. Loads packages with `go/packages` (type-check
// graph), finds the target interface, then walks every named type
// in the loaded packages and reports those whose method set
// satisfies the interface.
//
// Why structured (and not grep): impossible to compute with text
// search. Implementations in Go don't declare which interfaces they
// satisfy — the relationship is inferred at type-check time.
// Without this tool the agent can't reliably answer "what
// implements io.Reader" — it has to guess from naming conventions.
//
// Costs to know:
//   - Loading the type graph for a medium module takes a few
//     seconds. The 60s timeout is generous; tune via PerTool cfg.
//   - go/packages depends on `go` being on PATH (uses it for
//     module resolution).
//   - Tests are not loaded by default — `Tests: false` — so only
//     production code is searched.
func goImplementsFunc(gate *permissions.Gate, cfg *config.Config) functiontool.Func[goImplementsArgs, goImplementsResult] {
	return func(_ tool.Context, in goImplementsArgs) (goImplementsResult, error) {
		ifaceQuery := strings.TrimSpace(in.Interface)
		if ifaceQuery == "" {
			return goImplementsResult{}, fmt.Errorf("go_implements: interface is required")
		}
		if err := gate.CheckGeneric(context.Background(), "go_implements", ifaceQuery); err != nil {
			return goImplementsResult{}, err
		}
		root := in.Path
		if root == "" {
			root = "."
		}
		absRoot, err := absolutize(root)
		if err != nil {
			return goImplementsResult{}, err
		}
		if err := gate.CheckFileRead(context.Background(), "go_implements", absRoot); err != nil {
			return goImplementsResult{}, err
		}
		pattern := in.Pattern
		if pattern == "" {
			pattern = "./..."
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		loadCfg := &packages.Config{
			Context: ctx,
			Mode:    packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedFiles | packages.NeedSyntax,
			Dir:     absRoot,
			Tests:   false,
		}
		pkgs, err := packages.Load(loadCfg, pattern)
		if err != nil {
			return goImplementsResult{}, fmt.Errorf("go_implements: load %q: %w", pattern, err)
		}

		iface, ifaceFQN, err := findInterface(pkgs, ifaceQuery)
		if err != nil {
			return goImplementsResult{Interface: ifaceQuery}, err
		}

		caps := capsFor(cfg, "go_implements", 0, 100)
		var hits []goImplementsHit
		truncated := false
		for _, pkg := range pkgs {
			if pkg.Types == nil {
				continue
			}
			scope := pkg.Types.Scope()
			for _, name := range scope.Names() {
				obj := scope.Lookup(name)
				tn, ok := obj.(*types.TypeName)
				if !ok || tn.IsAlias() {
					continue
				}
				t := tn.Type()
				if t == nil {
					continue
				}
				// Skip the interface type itself; also skip other
				// interfaces (the question is "what concrete types
				// implement this").
				if _, isIface := t.Underlying().(*types.Interface); isIface {
					continue
				}
				kind := ""
				if types.Implements(t, iface) {
					kind = "concrete"
				} else if types.Implements(types.NewPointer(t), iface) {
					kind = "pointer"
				} else {
					continue
				}
				line := 0
				path := ""
				if pos := obj.Pos(); pos.IsValid() {
					fp := pkg.Fset.Position(pos)
					path = fp.Filename
					line = fp.Line
				}
				hits = append(hits, goImplementsHit{
					Package: pkg.PkgPath,
					Type:    name,
					Kind:    kind,
					Path:    path,
					Line:    line,
				})
				if caps.lines > 0 && len(hits) >= caps.lines {
					truncated = true
					break
				}
			}
			if truncated {
				break
			}
		}
		return goImplementsResult{
			Interface: ifaceFQN,
			Hits:      hits,
			Truncated: truncated,
		}, nil
	}
}

// findInterface resolves the user's query string to the *types.Interface
// to test against, plus a fully-qualified name for the result.
//
// "io.Reader" → packages must include "io"; lookup Reader there.
// "Reader"    → first loaded package whose Scope has a Reader interface wins.
// Returns an error when the query doesn't resolve to an interface type.
func findInterface(pkgs []*packages.Package, query string) (*types.Interface, string, error) {
	dotIdx := strings.LastIndex(query, ".")
	if dotIdx > 0 {
		qualPkg := query[:dotIdx]
		qualName := query[dotIdx+1:]
		for _, pkg := range pkgs {
			if pkg.Types == nil {
				continue
			}
			if pkg.Name != qualPkg && !strings.HasSuffix(pkg.PkgPath, "/"+qualPkg) && pkg.PkgPath != qualPkg {
				continue
			}
			if obj := pkg.Types.Scope().Lookup(qualName); obj != nil {
				if i, ok := obj.Type().Underlying().(*types.Interface); ok {
					return i, pkg.PkgPath + "." + qualName, nil
				}
				return nil, "", fmt.Errorf("go_implements: %s is not an interface (kind=%T)", query, obj.Type().Underlying())
			}
		}
		return nil, "", fmt.Errorf("go_implements: %q not found in loaded packages (looked for package %q with symbol %q)", query, qualPkg, qualName)
	}
	for _, pkg := range pkgs {
		if pkg.Types == nil {
			continue
		}
		if obj := pkg.Types.Scope().Lookup(query); obj != nil {
			if i, ok := obj.Type().Underlying().(*types.Interface); ok {
				return i, pkg.PkgPath + "." + query, nil
			}
		}
	}
	return nil, "", fmt.Errorf("go_implements: interface %q not found in loaded packages", query)
}
