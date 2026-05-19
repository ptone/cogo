// Copyright 2026 The Cogo Authors.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"testing"

	"google.golang.org/adk/agent"
	adkmodel "google.golang.org/adk/model"
	adktool "google.golang.org/adk/tool"
	"google.golang.org/genai"

	"github.com/go-steer/cogo/internal/permissions"
)

// TestGatedTool_ImplementsRequestProcessor pins a real headless bug:
// the ADK tool-preprocess step type-asserts every tool to
// toolinternal.RequestProcessor and aborts the turn otherwise. Without
// ProcessRequest on gatedTool, `cogo -p "..."` with any skills bundle
// fails before the model is ever called, with
// `tool "list_skills" does not implement RequestProcessor() method`.
// DO NOT delete this test to silence a compile failure — restore
// ProcessRequest on gatedTool instead.
func TestGatedTool_ImplementsRequestProcessor(t *testing.T) {
	t.Parallel()
	gate := permissions.New(permissions.Options{Mode: permissions.ModeYolo})
	ts := GateToolset(&fakeToolset{}, gate, "skill")
	tools, err := ts.Tools(nil)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	rp, ok := tools[0].(interface {
		ProcessRequest(adktool.Context, *adkmodel.LLMRequest) error
	})
	if !ok {
		t.Fatal("gatedTool must implement ProcessRequest (see test comment)")
	}
	req := &adkmodel.LLMRequest{}
	if err := rp.ProcessRequest(nil, req); err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}
	// PackTool must register the gated wrapper (not the inner tool) so
	// the runner's dispatch map routes every Run call through the gate.
	got, ok := req.Tools["echo"]
	if !ok {
		t.Fatal("expected tool to be registered under inner name")
	}
	if _, isGated := got.(*gatedTool); !isGated {
		t.Errorf("ProcessRequest must pack the gated wrapper, got %T", got)
	}
}

// TestGatedToolset_ProcessRequestForwards ensures the wrapper doesn't
// swallow toolset-level system-instruction injection (skilltoolset's
// "use load_skill to read full instructions" prompt depends on this).
func TestGatedToolset_ProcessRequestForwards(t *testing.T) {
	t.Parallel()
	called := false
	inner := &fakeToolset{processed: func() { called = true }}
	gate := permissions.New(permissions.Options{Mode: permissions.ModeYolo})
	ts := GateToolset(inner, gate, "skill")
	rp, ok := ts.(interface {
		ProcessRequest(adktool.Context, *adkmodel.LLMRequest) error
	})
	if !ok {
		t.Fatal("gatedToolset must implement ProcessRequest")
	}
	if err := rp.ProcessRequest(nil, &adkmodel.LLMRequest{}); err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}
	if !called {
		t.Error("expected ProcessRequest to forward to inner toolset")
	}
}

// fakeToolset stands in for a toolset that exposes a single runnable
// tool and an optional ProcessRequest hook for the forwarding test.
type fakeToolset struct {
	processed func()
}

func (f *fakeToolset) Name() string { return "fake" }
func (f *fakeToolset) Tools(agent.ReadonlyContext) ([]adktool.Tool, error) {
	return []adktool.Tool{fakeTool{}}, nil
}
func (f *fakeToolset) ProcessRequest(adktool.Context, *adkmodel.LLMRequest) error {
	if f.processed != nil {
		f.processed()
	}
	return nil
}

type fakeTool struct{}

func (fakeTool) Name() string        { return "echo" }
func (fakeTool) Description() string { return "echo" }
func (fakeTool) IsLongRunning() bool { return false }
func (fakeTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{Name: "echo"}
}
func (fakeTool) Run(adktool.Context, any) (map[string]any, error) {
	return map[string]any{"ok": true}, nil
}
