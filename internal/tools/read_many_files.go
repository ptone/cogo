// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/go-steer/cogo/internal/config"
	"github.com/go-steer/cogo/internal/permissions"
)

// readManyFilesArgs is the input the model sends. The agent supplies
// EITHER an explicit list of paths OR a glob pattern (or both — the
// union is read, deduplicated). The whole tool exists so Gemini-
// customtools can hand back N files in a single tool call rather than
// emitting N separate read_file calls (see docs/gemini-tooling-plan.md
// item 3 — Google's gemini-cli ships this exact tool for the same
// reason).
type readManyFilesArgs struct {
	Paths   []string `json:"paths,omitempty" jsonschema:"explicit list of files to read. Combined with pattern matches if both are set. At least one of paths/pattern is required"`
	Pattern string   `json:"pattern,omitempty" jsonschema:"optional shell-style basename pattern (e.g. *.go, README.*). Matched against files under root via filepath.Match — no ** recursion"`
	Root    string   `json:"root,omitempty" jsonschema:"root directory for the pattern walk. Defaults to current directory. Ignored when pattern is empty"`
}

// readManyFileEntry is one returned file. Per-file Error lets the tool
// report partial failure (e.g. one file out of scope) without aborting
// the whole batch — Files for which the gate refused or read failed
// arrive with a non-empty Error and empty Content.
type readManyFileEntry struct {
	Path      string `json:"path"`
	Content   string `json:"content,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
}

// readManyFilesResult is the structured output. Truncated is the
// batch-level flag — set when the total-bytes cap fired and we
// stopped reading more files (the model can decide whether to ask
// for the rest in a follow-up call).
type readManyFilesResult struct {
	Files     []readManyFileEntry `json:"files"`
	Truncated bool                `json:"truncated,omitempty"`
}

// readManyFilesFunc returns the handler. Caps are read from
// cfg.ToolOutput.PerTool["read_many_files"] with sensible defaults:
//   - per-file: same as read_file (256KB / 5000 lines)
//   - batch-total: 4× the per-file byte cap, so a 5-file read can
//     return all five even if one is near its individual cap.
func readManyFilesFunc(gate *permissions.Gate, cfg *config.Config) functiontool.Func[readManyFilesArgs, readManyFilesResult] {
	return func(_ tool.Context, in readManyFilesArgs) (readManyFilesResult, error) {
		if len(in.Paths) == 0 && in.Pattern == "" {
			return readManyFilesResult{}, fmt.Errorf("read_many_files: at least one of paths or pattern is required")
		}
		caps := capsFor(cfg, "read_many_files", 256*1024, 5000)
		totalBudget := 4 * caps.bytes

		// Build the unique, ordered file list. Explicit paths first,
		// then glob matches, deduplicated by absolute path.
		var targets []string
		seen := make(map[string]bool)
		addTarget := func(p string) {
			abs, err := absolutize(p)
			if err != nil || seen[abs] {
				return
			}
			seen[abs] = true
			targets = append(targets, abs)
		}
		for _, p := range in.Paths {
			addTarget(p)
		}
		if in.Pattern != "" {
			// Validate the pattern up-front for a clean error rather
			// than a silently-empty result.
			if _, err := filepath.Match(in.Pattern, ""); err != nil {
				return readManyFilesResult{}, fmt.Errorf("read_many_files: invalid pattern %q: %w", in.Pattern, err)
			}
			root := in.Root
			if root == "" {
				root = "."
			}
			absRoot, err := absolutize(root)
			if err != nil {
				return readManyFilesResult{}, err
			}
			if err := gate.CheckFileRead(context.Background(), "read_many_files", absRoot); err != nil {
				return readManyFilesResult{}, err
			}
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
				matched, mErr := filepath.Match(in.Pattern, d.Name())
				if mErr != nil || !matched {
					return nil
				}
				addTarget(path)
				return nil
			})
			if walkErr != nil {
				return readManyFilesResult{}, fmt.Errorf("read_many_files: walk: %w", walkErr)
			}
		}

		out := readManyFilesResult{Files: make([]readManyFileEntry, 0, len(targets))}
		totalBytes := 0
		for _, path := range targets {
			entry := readManyFileEntry{Path: path}
			if err := gate.CheckFileRead(context.Background(), "read_many_files", path); err != nil {
				// Surface the denial in the entry rather than aborting
				// the whole batch — the model can still use the files
				// the gate allowed through.
				entry.Error = err.Error()
				out.Files = append(out.Files, entry)
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				entry.Error = err.Error()
				out.Files = append(out.Files, entry)
				continue
			}
			text := Truncate(string(data), caps.bytes, caps.lines)
			entry.Truncated = len(text) < len(data)
			entry.Content = text
			out.Files = append(out.Files, entry)
			totalBytes += len(text)
			if totalBudget > 0 && totalBytes >= totalBudget {
				out.Truncated = true
				break
			}
		}
		return out, nil
	}
}
