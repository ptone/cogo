// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/go-steer/cogo/internal/config"
)

// TestApplyPermissionsOverride pins the CLI-flag → cfg.Permissions.Mode
// mapping that lets `cogo -p "..." --yolo` and `cogo --yolo` bypass the
// permission gate for built-in tools, MCP, and skills uniformly in
// headless and interactive modes. If the override ever silently no-ops
// (e.g. cfg replaced with a fresh copy downstream), permission prompts
// reappear in headless runs and the cogo.json edit is the only escape
// hatch. DO NOT delete this test to silence a compile failure — fix
// the override wiring instead.
func TestApplyPermissionsOverride(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		yolo     bool
		mode     string
		startCfg string
		want     string
		wantErr  bool
	}{
		{"no flags keeps config", false, "", "ask", "ask", false},
		{"-yolo overrides ask", true, "", "ask", "yolo", false},
		{"-yolo overrides allow", true, "", "allow", "yolo", false},
		{"-permissions=allow", false, "allow", "ask", "allow", false},
		{"-permissions=yolo", false, "yolo", "ask", "yolo", false},
		{"-permissions=ask", false, "ask", "yolo", "ask", false},
		{"-yolo and -permissions=yolo agree", true, "yolo", "ask", "yolo", false},
		{"-yolo and -permissions=ask conflict", true, "ask", "ask", "", true},
		{"unknown -permissions value", false, "loose", "ask", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.DefaultConfig()
			cfg.Permissions.Mode = tc.startCfg
			err := applyPermissionsOverride(cfg, tc.yolo, tc.mode)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if cfg.Permissions.Mode != tc.want {
				t.Errorf("cfg.Permissions.Mode = %q, want %q", cfg.Permissions.Mode, tc.want)
			}
		})
	}
}
