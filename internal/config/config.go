// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

// Package config defines the on-disk schema for `.agents/config.json` and
// the rules for discovering, parsing, and merging it with built-in defaults.
//
// See docs/DESIGN.md §4 for the schema sketch and `cogo init`/`.agents/`
// discovery semantics. Slice 1 implements config loading; the slash-command
// driven write path (`cogo init`, runtime mutation via /model) lands later.
package config

import (
	"fmt"
)

// SchemaVersion is the current major version of the on-disk config format.
// Bump when making a breaking change; older versions are rejected at load
// time with a clear error suggesting the upgrade path.
const SchemaVersion = 1

// Config is the in-memory representation of `.agents/config.json`.
//
// All sub-sections except Model have sensible zero-valued defaults, so a
// minimal `config.json` only needs to set what the user wants to override.
type Config struct {
	Version     int               `json:"version"`
	Model       ModelConfig       `json:"model"`
	Permissions PermissionsConfig `json:"permissions,omitempty"`
	PathScope   PathScopeConfig   `json:"path_scope,omitempty"`
	Agent       AgentConfig       `json:"agent,omitempty"`
	ToolOutput  ToolOutputConfig  `json:"tool_output,omitempty"`
	OTEL        OTELConfig        `json:"otel,omitempty"`
	URLScope    URLScopeConfig    `json:"url_scope,omitempty"`
	UI          UIConfig          `json:"ui,omitempty"`
}

// PathScopeConfig holds extra paths that file tools may read/write
// outside the default project + ~/.cogo/ scope. Patterns may be exact
// paths or directory globs (terminating "/...") and are typically
// appended via the "Always allow this path/tree" prompt path.
type PathScopeConfig struct {
	Allow []string `json:"allow,omitempty"`
}

// ModelConfig selects the LLM provider and model.
//
// Provider: "gemini" (public Gemini API, key auth) or "vertex" (Vertex AI,
// ADC auth). When empty, the resolver auto-detects from environment.
// Name: a model ID, e.g. "gemini-3.1-pro-preview-customtools".
// APIKey: optional inline key for Provider="gemini"; usually unset and
// read from GOOGLE_API_KEY at runtime.
// Vertex: required when Provider="vertex"; project + location.
type ModelConfig struct {
	Provider string         `json:"provider,omitempty"`
	Name     string         `json:"name"`
	APIKey   string         `json:"api_key,omitempty"`
	Vertex   *VertexConfig  `json:"vertex,omitempty"`
	Pricing  *PricingConfig `json:"pricing,omitempty"`
}

// VertexConfig holds GCP-specific settings for the vertex provider.
type VertexConfig struct {
	Project  string `json:"project"`
	Location string `json:"location"`
}

// PricingConfig overrides Cogo's built-in price table for cost estimation.
// Slice 1 does not surface costs; this is parsed but unused until Slice 4.
type PricingConfig struct {
	InputPerMTok  float64 `json:"input_per_mtok,omitempty"`
	OutputPerMTok float64 `json:"output_per_mtok,omitempty"`
}

// PermissionsConfig configures the permission gate. Activated in Slice 3.
type PermissionsConfig struct {
	Mode  string   `json:"mode,omitempty"`  // "ask" | "allow" | "yolo"
	Allow []string `json:"allow,omitempty"` // pattern allowlist
	Deny  []string `json:"deny,omitempty"`  // pattern denylist

	// UseBuiltinAllow toggles cogo's built-in conservative read-only
	// allowlist (pwd, ls, cat, head, tail, grep, find, …). nil means
	// "use the default", which is on; explicit false disables every
	// built-in bundle including any opt-ins in BuiltinAllowExtras.
	// Pointer-bool so a missing field defaults to true while explicit
	// false stays false through JSON merge.
	UseBuiltinAllow *bool `json:"use_builtin_allow,omitempty"`

	// BuiltinAllowExtras names additional built-in bundles to merge on
	// top of the conservative read-only baseline. Recognized values are
	// listed by permissions.KnownBundles() (currently "dev_tools" and
	// "cogo_tools"). Unknown names error at Validate() time.
	BuiltinAllowExtras []string `json:"builtin_allow_extras,omitempty"`
}

// AgentConfig tunes runtime agent behavior.
type AgentConfig struct {
	MaxSteps int `json:"max_steps,omitempty"`
}

// ToolOutputConfig caps tool result size before it enters model context.
// Activated in Slice 3.
type ToolOutputConfig struct {
	MaxBytes int                              `json:"max_bytes,omitempty"`
	MaxLines int                              `json:"max_lines,omitempty"`
	PerTool  map[string]ToolOutputPerToolCaps `json:"per_tool,omitempty"`
}

// ToolOutputPerToolCaps overrides global tool-output limits for one tool.
type ToolOutputPerToolCaps struct {
	MaxBytes int `json:"max_bytes,omitempty"`
	MaxLines int `json:"max_lines,omitempty"`
}

// OTELConfig configures the OpenTelemetry exporter. Activated in Slice 5.
type OTELConfig struct {
	Exporter string `json:"exporter,omitempty"` // "none" | "console" | "otlp"
	Endpoint string `json:"endpoint,omitempty"`
}

// UIConfig holds Bubble Tea presentation choices. Activated in Slice 2.
type UIConfig struct {
	Theme string `json:"theme,omitempty"` // "auto" | "light" | "dark"

	// Mouse enables terminal mouse capture so the wheel scrolls the
	// chat viewport. When enabled, plain click-drag no longer selects
	// text — terminals route around the capture when Shift is held
	// (Shift-drag to select, copy as usual). Pointer so unset means
	// "use the default" (true). Toggle at runtime with /mouse.
	Mouse *bool `json:"mouse,omitempty"`
}

// MouseEnabled reports whether mouse capture should be on at startup.
// Defaults to true when the field is unset.
func (u UIConfig) MouseEnabled() bool {
	if u.Mouse == nil {
		return true
	}
	return *u.Mouse
}

// Permission modes.
const (
	PermissionModeAsk   = "ask"
	PermissionModeAllow = "allow"
	PermissionModeYolo  = "yolo"
)

// Provider names recognized by the resolver.
const (
	ProviderGemini = "gemini"
	ProviderVertex = "vertex"
)

// DefaultConfig returns a Config with all fields populated by the defaults
// documented in REQUIREMENTS.md and DESIGN.md. Override-then-merge happens
// at Load time.
func DefaultConfig() *Config {
	return &Config{
		Version: SchemaVersion,
		Model: ModelConfig{
			// Provider intentionally empty — resolver auto-detects from env.
			// The -customtools variant prefers registered tools over raw
			// bash. Same price, same context, same reasoning — better
			// behavior for an agent that ships structured tools (grep,
			// read_file, etc.). See docs/gemini-tooling-plan.md item 1.
			Name: "gemini-3.1-pro-preview-customtools",
		},
		Permissions: PermissionsConfig{
			Mode: PermissionModeAsk,
		},
		Agent: AgentConfig{
			MaxSteps: 50,
		},
		ToolOutput: ToolOutputConfig{
			MaxBytes: 32 * 1024,
			MaxLines: 500,
			PerTool: map[string]ToolOutputPerToolCaps{
				"bash":      {MaxBytes: 64 * 1024, MaxLines: 2000},
				"read_file": {MaxBytes: 256 * 1024, MaxLines: 5000},
				"grep":      {MaxBytes: 16 * 1024, MaxLines: 200},
			},
		},
		OTEL: OTELConfig{
			Exporter: "none",
		},
		UI: UIConfig{
			Theme: "auto",
		},
	}
}

// Validate returns an error if the config is internally inconsistent.
// Validation here is structural; environmental concerns (is GOOGLE_API_KEY
// set? does the GCP project exist?) are checked at provider-construction
// time so test fixtures don't need real creds.
func (c *Config) Validate() error {
	if c.Version != 0 && c.Version != SchemaVersion {
		return fmt.Errorf("config: unsupported schema version %d (expected %d); upgrade your .agents/config.json", c.Version, SchemaVersion)
	}
	if c.Model.Name == "" {
		return fmt.Errorf("config: model.name is required")
	}
	switch c.Model.Provider {
	case "", ProviderGemini, ProviderVertex:
		// ok; "" means auto-detect at resolve time.
	default:
		return fmt.Errorf("config: unknown model.provider %q (want %q or %q)", c.Model.Provider, ProviderGemini, ProviderVertex)
	}
	if c.Model.Provider == ProviderVertex && c.Model.Vertex != nil {
		if c.Model.Vertex.Project == "" || c.Model.Vertex.Location == "" {
			return fmt.Errorf("config: model.vertex.project and model.vertex.location are required when provider is %q (or set GOOGLE_CLOUD_PROJECT / GOOGLE_CLOUD_LOCATION)", ProviderVertex)
		}
	}
	switch c.Permissions.Mode {
	case "", PermissionModeAsk, PermissionModeAllow, PermissionModeYolo:
		// ok
	default:
		return fmt.Errorf("config: unknown permissions.mode %q", c.Permissions.Mode)
	}
	// Bundle-name validation lives in the permissions package
	// (permissions.ResolveBuiltinAllow returns an error for unknown
	// names) because the bundle catalog lives there. Keeping the check
	// at gate-construction time avoids a circular import here.
	return nil
}

// URLScopeConfig governs which URLs the fetch_url built-in is allowed
// to reach. Same Allow/Deny grammar + precedence as PathScopeConfig:
// Deny wins on overlap; an empty Allow list with the tool registered
// is treated as default-deny (the tool refuses every fetch and returns
// a clear error pointing at this config field).
//
// Patterns are host-only globs (e.g. "github.com", "*.googleapis.com",
// "*.svc.cluster.local"). HTTPS is assumed unless the pattern is
// prefixed with "http://", in which case plain HTTP is allowed for
// that pattern only (intentionally awkward — operators have to type
// the prefix to opt out of TLS).
//
// MaxBodyBytes caps the response body the tool returns to the model;
// zero means use the built-in default (64 KiB).
// TimeoutSeconds caps the HTTP timeout; zero means 30s.
//
// Headers maps host patterns to header bundles. Header values pass
// through os.ExpandEnv at request time, so values like
// "Bearer ${GITHUB_TOKEN}" pick up rotated env vars without a
// restart. The model never sets headers directly — keeps credential
// exfiltration off the tool argument surface.
type URLScopeConfig struct {
	Allow          []string                     `json:"allow,omitempty"`
	Deny           []string                     `json:"deny,omitempty"`
	MaxBodyBytes   int                          `json:"max_body_bytes,omitempty"`
	TimeoutSeconds int                          `json:"timeout_seconds,omitempty"`
	Headers        map[string]map[string]string `json:"headers,omitempty"`
}
