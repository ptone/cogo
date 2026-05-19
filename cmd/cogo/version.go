// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"runtime/debug"
)

// Build-time metadata. Populated via -ldflags by goreleaser:
//
//	-X main.version={{.Version}}
//	-X main.commit={{.Commit}}
//	-X main.date={{.Date}}
//
// Defaults below apply when building with plain `go build` (no
// ldflags) and get partially filled in at runtime from the VCS info
// Go embeds automatically when -buildvcs=true (the default since
// Go 1.18). The dev/build script in the repo ships an equivalent
// -ldflags injection so locally-built binaries can also report a
// real git-describe-style version.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// versionString renders the build identity for the --version flag.
//
// Format: "cogo <semver> (commit <8-char-sha>[, modified], built <date>)".
// Stable enough that scripts can grep the leading word, the second
// token is always the version.
func versionString() string {
	v, c, d, dirty := resolveBuildInfo(version, commit, date)
	return formatVersion(v, c, d, dirty)
}

// resolveBuildInfo returns the version/commit/date/dirty tuple cogo
// should report. When ldflags injected real values, those win. When
// the defaults are still in place (plain `go build`), we fall back
// to the VCS metadata Go embeds via -buildvcs=true so the binary at
// least surfaces the SHA + commit time + dirty marker. The release
// path is unchanged.
func resolveBuildInfo(ldVersion, ldCommit, ldDate string) (v, c, d string, dirty bool) {
	v, c, d = ldVersion, ldCommit, ldDate
	// Only consult ReadBuildInfo when the defaults are still in
	// place; ldflags-injected values are authoritative.
	if v != "dev" || c != "none" {
		return v, c, d, false
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return v, c, d, false
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if s.Value != "" {
				c = s.Value
			}
		case "vcs.time":
			if s.Value != "" {
				d = s.Value
			}
		case "vcs.modified":
			if s.Value == "true" {
				dirty = true
			}
		}
	}
	return v, c, d, dirty
}

// formatVersion is the deterministic string-building half, split out
// so it can be tested without juggling build-info state.
func formatVersion(v, c, d string, dirty bool) string {
	short := c
	if len(short) > 8 {
		short = short[:8]
	}
	suffix := ""
	if dirty {
		suffix = ", modified"
	}
	return fmt.Sprintf("cogo %s (commit %s%s, built %s)", v, short, suffix, d)
}
