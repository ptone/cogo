// Package service is a second consumer of gate.Gate, exercising the
// "rename must update every call site" check.
package service

import "example.com/smoke/gate"

type Runner struct {
	g *gate.Gate
}

func NewRunner(g *gate.Gate) *Runner { return &Runner{g: g} }

func (r *Runner) RunBash(cmd string) error {
	return r.g.CheckBash(cmd)
}

func (r *Runner) RunOther(tool, key string) error {
	return r.g.CheckGeneric(tool, key)
}
