// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// TestFormatVersion pins the wire format of `cogo --version` output.
// Scripts (and humans) parse this string; reformatting it without
// updating consumers is a silent break. DO NOT delete this test to
// silence a compile failure — update consumers in lockstep instead.
func TestFormatVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, v, c, d string
		dirty         bool
		want          string
	}{
		{
			name: "release ldflags path",
			v:    "v0.3.0", c: "8eaa95f742aa1234567890abcdef", d: "2026-05-19T15:16:39Z",
			want: "cogo v0.3.0 (commit 8eaa95f7, built 2026-05-19T15:16:39Z)",
		},
		{
			name: "short commit (<8 chars) is passed through",
			v:    "v0.3.0", c: "abc", d: "2026-05-19",
			want: "cogo v0.3.0 (commit abc, built 2026-05-19)",
		},
		{
			name: "dev fallback with VCS info",
			v:    "dev", c: "8eaa95f742", d: "2026-05-19T15:16:39Z",
			want: "cogo dev (commit 8eaa95f7, built 2026-05-19T15:16:39Z)",
		},
		{
			name: "dirty worktree gets the modified marker",
			v:    "dev", c: "8eaa95f742", d: "2026-05-19T15:16:39Z", dirty: true,
			want: "cogo dev (commit 8eaa95f7, modified, built 2026-05-19T15:16:39Z)",
		},
		{
			name: "no VCS info available — defaults survive",
			v:    "dev", c: "none", d: "unknown",
			want: "cogo dev (commit none, built unknown)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := formatVersion(tc.v, tc.c, tc.d, tc.dirty); got != tc.want {
				t.Errorf("formatVersion = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveBuildInfo_LdflagsWin pins the contract that an
// explicitly-injected version (from goreleaser's -ldflags) bypasses
// the runtime/debug.ReadBuildInfo fallback. Without this guarantee,
// the release binary's metadata could get silently overwritten by
// stale VCS info from the build tree.
func TestResolveBuildInfo_LdflagsWin(t *testing.T) {
	t.Parallel()
	v, c, d, dirty := resolveBuildInfo("v0.3.0", "abcdef1234", "2026-05-19T15:16:39Z")
	if v != "v0.3.0" || c != "abcdef1234" || d != "2026-05-19T15:16:39Z" || dirty {
		t.Errorf("ldflags should win; got v=%q c=%q d=%q dirty=%v", v, c, d, dirty)
	}
}

// TestResolveBuildInfo_FallsBackToVCSInfo pins the dev-build
// behavior: when ldflags weren't injected, the function consults the
// runtime build info. The test binary itself was built with
// -buildvcs=true (the default), so its info should include a
// vcs.revision. We don't assert exact values (they depend on the
// build environment) — just that SOMETHING got filled in and the
// "(commit none, built unknown)" placeholder text is gone.
//
// If you change the resolution rules, also update versionString and
// keep the dev/build script in sync.
func TestResolveBuildInfo_FallsBackToVCSInfo(t *testing.T) {
	t.Parallel()
	_, c, d, _ := resolveBuildInfo("dev", "none", "unknown")
	// In CI sandboxes without VCS info Go ships empty values; we
	// tolerate that (the placeholders remain). The assertion is
	// "WHEN VCS info exists, the placeholders get replaced."
	if c == "none" && d == "unknown" {
		t.Skip("test binary built without VCS info; skipping VCS-fallback assertion")
	}
	if c == "none" {
		t.Errorf("vcs.revision present but commit stayed %q", c)
	}
	if d == "unknown" {
		t.Errorf("vcs.time present but date stayed %q", d)
	}
}

// TestVersionString_Wired confirms the top-level versionString()
// uses the package-level vars (rather than reaching past them or
// using a different format). The output should start with "cogo "
// and never be empty.
func TestVersionString_Wired(t *testing.T) {
	t.Parallel()
	got := versionString()
	if !strings.HasPrefix(got, "cogo ") {
		t.Errorf("versionString() = %q, want prefix \"cogo \"", got)
	}
	if strings.Contains(got, "()") || strings.Contains(got, "  ") {
		t.Errorf("versionString() = %q has empty fields or double spaces", got)
	}
}
