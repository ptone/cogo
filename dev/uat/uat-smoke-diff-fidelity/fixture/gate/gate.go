// Package gate exposes a tiny permission gate used as a rename
// target in the diff-fidelity smoke test. See ../README.md for the
// fixture's design.
package gate

import "fmt"

type Gate struct {
	allow bool
}

func New(allow bool) *Gate {
	return &Gate{allow: allow}
}

// CheckBash authorizes a bash command.
func (g *Gate) CheckBash(cmd string) error {
	if !g.allow {
		return fmt.Errorf("bash denied: %s", cmd)
	}
	return nil
}

// CheckGeneric authorizes any other tool by key. The diff-fidelity
// test asserts this name survives unchanged.
func (g *Gate) CheckGeneric(tool, key string) error {
	if !g.allow {
		return fmt.Errorf("%s denied: %s", tool, key)
	}
	return nil
}
