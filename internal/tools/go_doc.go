// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/go-steer/cogo/internal/config"
	"github.com/go-steer/cogo/internal/permissions"
)

// goDocArgs is what the agent passes when calling go_doc.
type goDocArgs struct {
	Target string `json:"target" jsonschema:"package path, symbol, or pkg.Symbol — same syntax as 'go doc'. Examples: 'fmt', 'fmt.Println', 'net/http.Server', 'os.File.Read'"`
	All    bool   `json:"all,omitempty" jsonschema:"if true, include unexported symbols (mirrors 'go doc -all'). Default false."`
}

// goDocResult is the structured output. Body is the doc text; the
// classified fields are best-effort splits of the first lines into
// {package, signature, doc} so the model can read them as fields
// rather than parsing a single text blob.
type goDocResult struct {
	Target    string `json:"target"`
	Package   string `json:"package,omitempty"`
	Signature string `json:"signature,omitempty"`
	Doc       string `json:"doc,omitempty"`
	Body      string `json:"body"` // full `go doc` output, possibly truncated
	Truncated bool   `json:"truncated,omitempty"`
}

// goDocFunc returns the ADK functiontool handler for go_doc. The
// closure shells out to `go doc -short <target>` (or `-all` when
// requested) with a short timeout, captures stdout, and surfaces a
// structured result with the most useful fields extracted.
//
// Why a structured tool rather than letting the model use bash:
//   - Bash `go doc fmt.Println` returns raw text the model has to
//     re-parse to find the signature vs the description.
//   - We can cap output deterministically (1 MB default) rather than
//     hoping the model summarizes a huge package's docs sensibly.
//   - Failure modes (unknown package, missing module, network
//     errors) come back as a clear Go error rather than mangled
//     bash output.
//
// Gate: routed through CheckGeneric under the "go_doc" key. In yolo
// mode this is a no-op; in allow mode the user can pin
// `go_doc:*` to skip prompts; in ask mode the user is prompted on
// first invocation.
func goDocFunc(gate *permissions.Gate, cfg *config.Config) functiontool.Func[goDocArgs, goDocResult] {
	return func(_ tool.Context, in goDocArgs) (goDocResult, error) {
		if strings.TrimSpace(in.Target) == "" {
			return goDocResult{}, fmt.Errorf("go_doc: target is required (e.g. \"fmt\", \"fmt.Println\")")
		}
		if err := gate.CheckGeneric(context.Background(), "go_doc", in.Target); err != nil {
			return goDocResult{}, err
		}
		caps := capsFor(cfg, "go_doc", 1024*1024, 0)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		args := []string{"doc"}
		if in.All {
			args = append(args, "-all")
		}
		// Note: we deliberately don't pass `-short`. -short strips
		// the leading "package X // import \"X\"" header which is
		// exactly the line we use to classify the structured Package
		// field. The byte cap below keeps large package overviews
		// from blowing the output budget.
		args = append(args, in.Target)
		out, err := exec.CommandContext(ctx, "go", args...).CombinedOutput()
		body := string(out)
		if err != nil {
			// `go doc` exits non-zero on unknown targets and other
			// errors; surface the stderr-style text so the agent can
			// diagnose ("package X not found" etc.) rather than just
			// getting an opaque exit code.
			return goDocResult{Target: in.Target, Body: strings.TrimSpace(body)}, fmt.Errorf("go_doc: %s: %s", err.Error(), strings.TrimSpace(body))
		}
		body = strings.TrimRight(body, "\n")
		truncated := false
		if caps.bytes > 0 && len(body) > caps.bytes {
			body = body[:caps.bytes] + "\n…(truncated)"
			truncated = true
		}
		res := goDocResult{
			Target:    in.Target,
			Body:      body,
			Truncated: truncated,
		}
		classifyGoDoc(&res, body)
		return res, nil
	}
}

// classifyGoDoc populates Package / Signature / Doc from the body
// using `go doc`'s known structure: the first non-empty line carries
// the package declaration ("package fmt // import \"fmt\""); the
// next non-empty group is the symbol signature; what follows is the
// documentation paragraphs. Best-effort — falls through to leaving
// the structured fields empty if the body doesn't match.
func classifyGoDoc(res *goDocResult, body string) {
	lines := strings.Split(body, "\n")
	// First non-empty line.
	var head string
	idx := 0
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			head = l
			idx = i + 1
			break
		}
	}
	if strings.HasPrefix(head, "package ") {
		res.Package = strings.TrimSpace(strings.TrimPrefix(head, "package "))
		// Drop trailing `// import "..."` comment for cleanliness.
		if i := strings.Index(res.Package, "//"); i >= 0 {
			res.Package = strings.TrimSpace(res.Package[:i])
		}
	}
	// Next non-empty line is usually the signature ("func Println(a ...any) (n int, err error)").
	for i := idx; i < len(lines); i++ {
		if t := strings.TrimSpace(lines[i]); t != "" {
			res.Signature = t
			idx = i + 1
			break
		}
	}
	// Remaining body is the doc paragraph(s).
	rest := strings.TrimSpace(strings.Join(lines[idx:], "\n"))
	if rest != "" {
		res.Doc = rest
	}
}
