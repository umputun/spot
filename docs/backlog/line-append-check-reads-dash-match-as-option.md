---
worth: yes
where: pkg/runner/commands.go:Line
added: 2026-11-25
---
# line append check reads a match starting with "-" as a grep option

The append branch of `Line` checks for the pattern with `grep -q '<match>' <file>`, with no `-e`.
A match that starts with `-`, such as `-Xmx512m` in a jvm.options file, is parsed as grep options:
ugrep and BusyBox grep exit 2 ("invalid argument"), and `Line` takes any non-zero exit as "pattern
not found", so the line is appended again on every run. `grep -q -e '<match>'` matches with exit 0.

Fix: add `-e` to the check, with a `Test_execLine` case that appends a `-Xmx512m` line three times
and expects one copy. Surfaced reviewing PR #380, where the same command was quoted but kept its
missing `-e`.
