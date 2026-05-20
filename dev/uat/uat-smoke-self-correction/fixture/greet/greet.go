// Package greet returns simple greetings. See ../README.md — the
// exported function name here is deliberately wrong relative to the
// caller in main.go; the agent's task is to reconcile them.
package greet

func Helo(name string) string {
	return "Hello, " + name + "!"
}
