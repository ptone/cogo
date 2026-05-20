# self-correction fixture

Two-file Go project deliberately wired to a compile error: `main.go`
calls `greet.Hello`, while `greet/greet.go` declares `Helo` (a typo).
`go build` fails with `undefined: greet.Hello`.

The agent's job is to reconcile the names — typically by renaming
`Helo` to `Hello` in `greet/greet.go`. The driver then asserts a
clean build. A bonus diagnostic counts how many times the agent
invoked `go build` (structured `go_build` or bash) — 0 or 1 means
the agent guessed without verifying; ≥2 means it ran the canonical
build → edit → build loop.
