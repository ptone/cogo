// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"errors"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/mcptoolset"
)

func TestSanitizePrefix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"github", "github"},
		{"my-server", "my_server"},
		{"file.system", "file_system"},
		{"abc 123", "abc_123"},
		{"_alpha", "_alpha"},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := sanitizePrefix(tc.in); got != tc.want {
				t.Errorf("sanitizePrefix(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestWithNamespace_NilSafe(t *testing.T) {
	t.Parallel()
	if got := withNamespace(nil, "x"); got != nil {
		t.Errorf("nil toolset should pass through, got %v", got)
	}
	// Non-nil toolset + empty prefix → unwrapped.
	stub := newInMemoryToolset(t)
	if got := withNamespace(stub, ""); got != stub {
		t.Errorf("empty prefix should pass through")
	}
}

func TestWithNamespace_PrefixesToolNames(t *testing.T) {
	t.Parallel()
	inner := newInMemoryToolset(t)
	wrapped := withNamespace(inner, "demo")

	tools, err := wrapped.Tools(asReadonly(context.Background()))
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected at least one tool from the in-memory MCP server")
	}
	for _, tl := range tools {
		if got := tl.Name(); got == "" {
			t.Errorf("empty tool name")
		} else if got[:5] != "demo_" {
			t.Errorf("tool %q missing demo_ prefix", got)
		}
	}
}

func TestWithNamespace_NameDescriptionLongRunningPassthrough(t *testing.T) {
	t.Parallel()
	inner := newInMemoryToolset(t)
	wrapped := withNamespace(inner, "demo")

	tools, err := wrapped.Tools(asReadonly(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools {
		if tl.Description() == "" {
			t.Errorf("description should pass through (got empty for %s)", tl.Name())
		}
		// IsLongRunning shouldn't panic; the in-memory server returns
		// short-running tools so we expect false here.
		if tl.IsLongRunning() {
			t.Errorf("expected IsLongRunning=false for %s", tl.Name())
		}
	}
}

func TestRenamedTool_DeclarationFromInner(t *testing.T) {
	t.Parallel()
	inner := newInMemoryToolset(t)
	wrapped := withNamespace(inner, "demo")
	tools, err := wrapped.Tools(asReadonly(context.Background()))
	if err != nil {
		t.Fatal(err)
	}

	for _, tl := range tools {
		rn, ok := tl.(interface {
			Declaration() interface{}
		})
		_ = ok
		_ = rn
		// renamedTool's Declaration returns a *genai.FunctionDeclaration.
		// Use the local runnable interface from namespace.go.
		if rt, ok := tl.(runnable); ok {
			d := rt.Declaration()
			if d == nil {
				t.Errorf("nil declaration for %s", tl.Name())
				continue
			}
			if d.Name != tl.Name() {
				t.Errorf("declaration.Name = %q, want %q", d.Name, tl.Name())
			}
		}
	}
}

// TestRenamedTool_ImplementsRequestProcessor pins a real headless bug:
// the ADK's tool-preprocess step type-asserts every tool to
// toolinternal.RequestProcessor and refuses to run the turn otherwise.
// If you remove this method from renamedTool, headless runs that load
// any namespaced MCP toolset fail with
// `tool "<prefixed>" does not implement RequestProcessor() method`
// before the model is ever called. DO NOT delete this test to silence
// a compile failure — restore ProcessRequest on renamedTool instead.
func TestRenamedTool_ImplementsRequestProcessor(t *testing.T) {
	t.Parallel()
	inner := newInMemoryToolset(t)
	wrapped := withNamespace(inner, "demo")
	tools, err := wrapped.Tools(asReadonly(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) == 0 {
		t.Fatal("expected at least one tool from in-memory MCP server")
	}
	for _, tl := range tools {
		rp, ok := tl.(interface {
			ProcessRequest(tool.Context, *model.LLMRequest) error
		})
		if !ok {
			t.Fatalf("renamed tool %q must implement ProcessRequest "+
				"(ADK preprocess requires this; see test comment)", tl.Name())
		}
		req := &model.LLMRequest{}
		if err := rp.ProcessRequest(nil, req); err != nil {
			t.Fatalf("ProcessRequest(%q): %v", tl.Name(), err)
		}
		// PackTool registers under the wrapper's prefixed name so the
		// runner's dispatch map points at the renamed wrapper, not the
		// inner unprefixed tool. If this regresses, MCP calls would be
		// dispatched under the wrong name and 404.
		if _, ok := req.Tools[tl.Name()]; !ok {
			t.Errorf("ProcessRequest must register tool under prefixed name %q", tl.Name())
		}
	}
}

// TestNamespacedToolset_ProcessRequestForwards ensures toolset-level
// request processors survive the wrapper — important when wrapping any
// future ADK toolset that injects system instructions (skilltoolset
// already does this and is gated through a sibling wrapper).
func TestNamespacedToolset_ProcessRequestForwards(t *testing.T) {
	t.Parallel()
	called := false
	inner := &fakeToolsetWithProcessRequest{onProcess: func() { called = true }}
	wrapped := withNamespace(inner, "x")
	rp, ok := wrapped.(interface {
		ProcessRequest(tool.Context, *model.LLMRequest) error
	})
	if !ok {
		t.Fatal("namespacedToolset must implement ProcessRequest")
	}
	if err := rp.ProcessRequest(nil, &model.LLMRequest{}); err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}
	if !called {
		t.Error("expected ProcessRequest to forward to inner toolset")
	}
}

type fakeToolsetWithProcessRequest struct {
	onProcess func()
}

func (f *fakeToolsetWithProcessRequest) Name() string { return "fake" }
func (f *fakeToolsetWithProcessRequest) Tools(agent.ReadonlyContext) ([]tool.Tool, error) {
	return nil, nil
}
func (f *fakeToolsetWithProcessRequest) ProcessRequest(tool.Context, *model.LLMRequest) error {
	f.onProcess()
	return nil
}

func TestSimpleErr(t *testing.T) {
	t.Parallel()
	if got := errNotRunnable.Error(); got == "" {
		t.Errorf("expected non-empty error message")
	}
	var err error = simpleErr("boom")
	if !errors.Is(err, simpleErr("boom")) {
		// errors.Is on non-comparable would fail; this just checks
		// that the type implements error correctly.
		// Don't assert; presence of the method matters.
		_ = err.Error()
	}
}

// newInMemoryToolset spins up an in-memory MCP server with one trivial
// tool and returns a connected mcptoolset for testing the wrapper.
// Mirrors the spike's pattern.
func newInMemoryToolset(t *testing.T) tool.Toolset {
	t.Helper()
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()

	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "v1"}, nil)
	type input struct {
		Msg string `json:"msg" jsonschema:"echo input"`
	}
	type output struct {
		Echo string `json:"echo"`
	}
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "echo",
		Description: "Echoes its input back",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in input) (*mcpsdk.CallToolResult, output, error) {
		return nil, output{Echo: in.Msg}, nil
	})
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}

	ts, err := mcptoolset.New(mcptoolset.Config{Transport: clientTransport})
	if err != nil {
		t.Fatalf("toolset: %v", err)
	}
	return ts
}

// Compile-time check that asReadonly returns a real ReadonlyContext.
var _ agent.ReadonlyContext = asReadonly(context.Background())
