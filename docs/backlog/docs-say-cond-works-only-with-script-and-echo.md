---
worth: yes
where: README.md:634
added: 2026-09-07
---
# docs say cond works with script and echo only, but line honors it too

README.md:634 says "currently conditions can be used with `script` and `echo` command types only",
and `site/docs/llms.txt:456` carries the same note. `Line` at pkg/runner/commands.go:587 calls
`checkCondition` the same way `Script` and `Echo` do, so the list has been short one command since
`line` was added. #349 adds `template` with `cond` support as well, so the line goes staler on merge.

Fix: list every command that calls `checkCondition` in both files, and regenerate docs-src with
`make prep-site` after the README edit.
