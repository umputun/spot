---
worth: yes
where: CLAUDE.md
added: 2026-09-10
---
# both CLAUDE.md command lists leave out line

CLAUDE.md names the command types twice, in the overview feature bullet ("script, copy, sync, delete,
echo, wait") and in the `pkg/runner/commands.go` description ("script, copy, sync, delete, wait, echo").
Neither lists `line`, which has been a command type since 3d543046 (`Line` at
pkg/runner/commands.go:585, dispatched from runner.go). README's two equivalent lists do include it, so
the two files disagree.

CLAUDE.md is read as fact by every agent session, so an agent looking up the command inventory does not
learn `line` exists and reaches for a `script` with sed instead.

Fix: add `line` to both lists. Surfaced reviewing PR #349, which appends `template` to these same two
lines without noticing `line` was already missing, so this is cheapest done when that merges.
