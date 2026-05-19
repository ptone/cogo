// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

// Command cogo is the entry point for the Cogo agentic CLI.
//
// Two modes:
//   - Headless (`cogo -p "prompt"`): run one agent turn, stream the
//     reply to stdout, exit.
//   - Interactive TUI (`cogo` on a TTY): launch the Bubble Tea chat.
//
// When invoked with no -p and no TTY (piped input or CI), prints a hint
// pointing at -p and exits non-zero so callers don't hang waiting for
// a TUI that can't run.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/go-steer/cogo/internal/config"
	"github.com/go-steer/cogo/internal/headless"
	"github.com/go-steer/cogo/internal/initcmd"
	"github.com/go-steer/cogo/internal/tui"

	// Register the Gemini provider with models.Resolve.
	_ "github.com/go-steer/cogo/internal/models/gemini"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable main entry. Keeping it separate from main() lets
// future tests drive flag parsing without forking a subprocess.
func run(args []string, stdout, stderr *os.File) int {
	// Subcommand dispatch first — `cogo init [args]`.
	if len(args) > 0 && args[0] == "init" {
		return initcmd.RunCLI(args[1:], stdout, stderr)
	}

	fs := flag.NewFlagSet("cogo", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		prompt          string
		debug           bool
		help            bool
		showVersion     bool
		yolo            bool
		permissionsMode string
	)
	fs.StringVar(&prompt, "p", "", "Shorthand for -prompt.")
	fs.StringVar(&prompt, "prompt", "", "Run a single prompt non-interactively and stream the reply to stdout, then exit.")
	fs.BoolVar(&debug, "debug", false, "Enable verbose logging to stderr.")
	fs.BoolVar(&yolo, "yolo", false, "Bypass all permission prompts and allowlists (built-in tools, MCP, and skills). Shorthand for -permissions=yolo. The bash destructive-command denylist still applies.")
	fs.StringVar(&permissionsMode, "permissions", "", "Override permissions.mode for this invocation: ask | allow | yolo.")
	fs.BoolVar(&help, "h", false, "Show help and exit.")
	fs.BoolVar(&help, "help", false, "Show help and exit.")
	fs.BoolVar(&showVersion, "version", false, "Show version and exit.")
	fs.BoolVar(&showVersion, "v", false, "Shorthand for -version.")
	fs.Usage = func() { printUsage(stderr) }

	if err := fs.Parse(args); err != nil {
		// flag.ContinueOnError prints its own message to fs.Output() (stderr).
		return headless.ExitConfigError
	}
	if showVersion {
		fmt.Fprintln(stdout, versionString())
		return headless.ExitOK
	}
	if help {
		printUsage(stdout)
		return headless.ExitOK
	}

	setupLogging(debug, stderr)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "cogo: get cwd: %v\n", err)
		return headless.ExitConfigError
	}
	cfg, agentsDir, err := config.LoadOrDefault(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "cogo: %v\n", err)
		return headless.ExitConfigError
	}
	if err := applyPermissionsOverride(cfg, yolo, permissionsMode); err != nil {
		fmt.Fprintf(stderr, "cogo: %v\n", err)
		return headless.ExitConfigError
	}
	if agentsDir != "" {
		slog.Debug("loaded project config", "agentsDir", agentsDir)
	} else {
		slog.Debug("no .agents/ found; using built-in defaults")
	}
	if debug && agentsDir != "" {
		closeLog := setupDebugLogFile(agentsDir, stderr)
		defer closeLog()
	}

	if prompt != "" {
		code, err := headless.RunFromConfig(ctx, cfg, agentsDir, prompt, stdout, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "cogo: %v\n", err)
		}
		return code
	}

	// No -p supplied. Launch interactive TUI when attached to a terminal.
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(stderr, "cogo: interactive TUI requires a terminal. Use -p \"your prompt\" for headless mode.")
		return headless.ExitConfigError
	}
	code, err := tui.Run(ctx, cfg, agentsDir)
	if err != nil {
		fmt.Fprintf(stderr, "cogo: %v\n", err)
	}
	return code
}

// applyPermissionsOverride lets CLI flags override the loaded config's
// permission mode for this invocation only — the on-disk cogo.json is
// untouched. -yolo is a convenience alias for -permissions=yolo;
// passing both is fine when they agree, and an error when they don't.
func applyPermissionsOverride(cfg *config.Config, yolo bool, mode string) error {
	if mode != "" {
		switch mode {
		case config.PermissionModeAsk, config.PermissionModeAllow, config.PermissionModeYolo:
			// ok
		default:
			return fmt.Errorf("invalid -permissions %q (want ask, allow, or yolo)", mode)
		}
		if yolo && mode != config.PermissionModeYolo {
			return fmt.Errorf("conflicting flags: -yolo with -permissions=%q", mode)
		}
		cfg.Permissions.Mode = mode
		return nil
	}
	if yolo {
		cfg.Permissions.Mode = config.PermissionModeYolo
	}
	return nil
}

func setupLogging(debug bool, w *os.File) {
	level := slog.LevelWarn
	if debug {
		level = slog.LevelDebug
	}
	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(h))
}

// setupDebugLogFile installs a JSONL slog handler writing to
// .agents/logs/<timestamp>.jsonl. Called only when --debug is set
// AND we have a project .agents/ to write into. Returns a closer
// the caller should defer to flush the file on exit.
//
// Falls back to text-on-stderr (no JSONL file) when agentsDir is empty
// or the file can't be opened — debug mode still works, just without
// the structured log.
func setupDebugLogFile(agentsDir string, stderr *os.File) func() {
	if agentsDir == "" {
		return func() {}
	}
	logDir := filepath.Join(agentsDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "cogo: debug log dir: %v\n", err)
		return func() {}
	}
	name := time.Now().UTC().Format("2006-01-02T15-04-05") + ".jsonl"
	path := filepath.Join(logDir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(stderr, "cogo: debug log open: %v\n", err)
		return func() {}
	}
	h := slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(h))
	fmt.Fprintf(stderr, "cogo: debug log → %s\n", path)
	return func() { _ = f.Close() }
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, `Cogo — a terminal-native agentic CLI for Go developers.

Usage:
  cogo                 Open the interactive TUI (requires a terminal).
  cogo -p "<prompt>"   Run a single prompt non-interactively and exit.
  cogo -h              Show this help.

Flags:
  -p, -prompt <text>   Run a single prompt non-interactively and stream the
                       assistant reply to stdout, then exit.
  -yolo                Bypass all permission prompts and allowlists (built-in
                       tools, MCP servers, and skills). The bash destructive-
                       command denylist still applies. Works in both headless
                       and interactive modes.
  -permissions <mode>  Override permissions.mode for this run: ask | allow |
                       yolo. Cogo.json is not modified.
  -debug               Enable verbose logging to stderr.
  -v, -version         Print version and exit.
  -h, -help            Show this help.

Authentication:
  Set GOOGLE_API_KEY for the public Gemini API, or
  GOOGLE_GENAI_USE_VERTEXAI=true with GOOGLE_CLOUD_PROJECT (and Application
  Default Credentials) for Vertex AI.

See docs/REQUIREMENTS.md, docs/DESIGN.md, and docs/SLICES.md for the spec.`)
}
