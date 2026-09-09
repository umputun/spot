---
worth: yes
where: README.md:1031
added: 2026-09-09
---
# the runtime variables list leaves out echo and line

README.md:1031 says `{SPOT_*}` substitution applies to "`script`, `copy`, `sync`, `delete`, `wait` and
`env`". `Echo` at pkg/runner/commands.go:565 and `Line` at commands.go:604-607 both run `tmpl.apply` over
their fields, so the list has been two commands short since each was added. `site/docs/llms.txt:476`
carries the same sentence and names `echo` but still omits `line`.

A user reading the list concludes a per-host value cannot go in a `line` match or an `echo` body and works
around it.

Fix: list every command that calls `tmpl.apply` in both files, and regenerate docs-src with
`make prep-site` after the README edit. Same shape as
[[docs-say-cond-works-only-with-script-and-echo]] and cheapest done in the same pass.
