# diff-fidelity fixture

A four-file Go project shaped to exercise three failure modes the
diff-fidelity smoke test cares about:

- **Over-broad replace.** `gate.go` declares both `Gate.CheckBash` (the
  rename target) and `Gate.CheckGeneric` (out of scope). A sed-style
  `s/Check/Authorize/g` would incorrectly rewrite `CheckGeneric` too.
- **Drift.** `main.go` and `service/service.go` both call
  `Gate.CheckBash`; missing either call site leaves an undefined-method
  compile error. `go build` + `go test` catch this.
- **Orphan tmp files.** The harness asserts no `tmp_*.go` siblings
  exist after the agent finishes — those are the signature of failed
  bash `awk` / `sed` redirects from the v0.3.x era.

The fixture comments inside the `.go` files are kept minimal so the
`lacks CheckBash` assertion in the driver is unambiguous after a
clean rename.
