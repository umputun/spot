---
worth: later
where: pkg/runner/commands.go
added: 2026-11-25
---
# the "__SQ__:" marker is spelled out in three places

The single-quoted-value marker is written as a literal where `Script` adds it to a setvar value,
in `templater.apply` (which also strips it with a bare `v[7:]`), and in `Line` (#380), which
trims it for `replace` and `append`. The `Test_execLine` case for it hardcodes the literal too, so
renaming the marker in only some places would pass that test while `Line` writes `\$` again.

Fix: one unexported constant used at all three sites and in the test, with `v[len(marker):]`.
Fold it in when templater-rewrites-secret-values.md touches the marker handling.
