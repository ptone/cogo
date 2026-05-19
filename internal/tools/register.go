// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"fmt"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/go-steer/cogo/internal/config"
	"github.com/go-steer/cogo/internal/permissions"
)

// Registry is the assembled built-in tool set returned to the agent.
//
// Slice 3 ships file I/O, bash, and todo. v0.3.0 added glob + grep
// (item 2 of docs/gemini-tooling-plan.md) so Gemini agents don't fall
// back to `bash grep` for every code search. Web tools and the
// subagent tool follow in later slices.
type Registry struct {
	Tools []tool.Tool
	Todo  *TodoStore // exposed so callers can inspect plan progress
}

// Build constructs the registry. cfg supplies output-truncation caps;
// gate gates every mutating call.
//
// We deliberately do NOT set ADK's functiontool.Config.RequireConfirmation
// even when the gate is in "ask" mode. Cogo's gate handles approval
// itself by calling its Prompter from inside each tool handler — the
// handler blocks until the user responds. Going through ADK's HITL
// flow (LongRunningToolIDs + state injection) would be a second
// approval round-trip on top of ours.
func Build(cfg *config.Config, gate *permissions.Gate) (*Registry, error) {
	if cfg == nil {
		return nil, fmt.Errorf("tools: cfg is required")
	}
	if gate == nil {
		return nil, fmt.Errorf("tools: gate is required")
	}
	store := NewTodoStore()

	specs := []struct {
		name string
		desc string
		ctor func() (tool.Tool, error)
	}{
		{"read_file", "Read a file from disk and return its contents.", func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "read_file",
				Description: "Read a file from disk. Honors offset/limit for large files. PREFERRED over `bash cat`: enforces the permission gate and applies output truncation automatically.",
			}, readFileFunc(gate, cfg))
		}},
		{"write_file", "Write or overwrite a file with the given content.", func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "write_file",
				Description: "Create or overwrite a file atomically with the full content provided. The ONLY sanctioned full-file write path — do not use `bash` with redirects (`cat <<EOF > file`, `echo > file`, `awk '...' > file`) to write files; those are unreliable and leave orphan files when intermediate steps fail. For partial / targeted edits use edit_file instead. Asks for confirmation in 'ask' mode. For .go files, gofmt is applied automatically before writing (the response's `formatted` field reports whether it ran) — so you don't need to format the content yourself; submit functional Go and the file lands gofmt-clean.",
			}, writeFileFunc(gate))
		}},
		{"edit_file", "Replace one occurrence of an exact string in a file.", func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "edit_file",
				Description: "Replace exactly one occurrence of old_string with new_string in path. The ONLY sanctioned in-place edit path — do not use `bash sed -i`, `bash awk '...' > file`, or similar shell rewrites; those leave orphan files when redirects misfire. If old_string isn't unique, either grep for a more specific snippet OR read the full file and use write_file with the new content. If you need to insert content at a pattern without an exact-string anchor, read + write_file is the safe path until cogo ships dedicated insert tools. For .go files, gofmt is applied to the resulting file automatically (the response's `formatted` field reports whether it ran).",
			}, editFileFunc(gate))
		}},
		{"list_dir", "List entries of a directory.", func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "list_dir",
				Description: "List the entries (files and subdirectories) of a directory. PREFERRED over `bash ls`: gate-enforced and output-capped.",
			}, listDirFunc(gate, cfg))
		}},
		{"glob", "Find files by basename pattern.", func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "glob",
				Description: "Walk a directory and return paths whose basename matches a shell-style pattern (e.g. *.go, README.*). PREFERRED over `bash find` for filename discovery: gate-enforced, output-capped, skips .git/.svn/.hg/node_modules/vendor.",
			}, globFunc(gate, cfg))
		}},
		{"grep", "Search file contents with a regex.", func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "grep",
				Description: "Walk a directory and return every line matching an RE2 regular expression. Single-file mode when the path is a regular file. PREFERRED over `bash grep` (or rg / ag): gate-enforced, automatic output truncation, skips .git/.svn/.hg/node_modules/vendor.",
			}, grepFunc(gate, cfg))
		}},
		{"read_many_files", "Read multiple files in one call.", func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "read_many_files",
				Description: "Read several files in a single tool call. Takes an explicit `paths` list and/or a `pattern` glob (walked from `root`, default cwd). Returns {path, content} per file with per-file truncation and a batch-level cap. PREFERRED over multiple read_file calls when you need to read N files at once — one batched call is more token-efficient and lets the runner serve them in parallel.",
			}, readManyFilesFunc(gate, cfg))
		}},
		{"bash", "Run a shell command and return its output.", func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name: "bash",
				Description: "Execute a shell command via /bin/sh -c with a timeout. Reserved for actions the structured tools cannot perform: builds, tests, linters, package managers, git operations, mkdir/mv/cp/rm. " +
					"\n\n" +
					"DO NOT use bash for reading, writing, or editing files. The following patterns are forbidden because they bypass the structured tools and produce unreliable results (orphan files when intermediate steps fail, no truncation, no consistent gate enforcement):\n" +
					"  - `cat file`, `head file`, `tail file`        → use read_file\n" +
					"  - `cat <<EOF > file`, `echo ... > file`       → use write_file\n" +
					"  - `awk '...' > file`, `sed -i 's/.../.../'`   → use edit_file (or write_file for a full rewrite)\n" +
					"  - `python3 -c \"open(...)\"`                    → use write_file / edit_file\n" +
					"  - `ls`, `find -name`                          → use list_dir / glob\n" +
					"  - `grep -r`, `rg`, `ag`                       → use grep\n" +
					"\n" +
					"For code investigation always reach for the structured tools first. If you find yourself wanting to chain bash with pipes/redirects to modify files, that is a signal you should be using read_file + write_file / edit_file instead.",
			}, bashFunc(gate, cfg))
		}},
		{"todo", "Maintain an agent-facing todo list (list/add/set_status/clear).", func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name: "todo", Description: "Maintain a short todo list visible to the user. Actions: list, add, set_status, clear.",
			}, todoFunc(store))
		}},
	}

	out := &Registry{Todo: store}
	for _, s := range specs {
		t, err := s.ctor()
		if err != nil {
			return nil, fmt.Errorf("tools: build %s: %w", s.name, err)
		}
		out.Tools = append(out.Tools, t)
	}
	return out, nil
}
