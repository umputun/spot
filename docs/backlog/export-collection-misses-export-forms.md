---
worth: maybe
where: pkg/config/command.go:exportKeyWithQuote
added: 2026-11-19
---
# export collection handles only a single `export NAME=value`

README.md ("Passing variables from one script command to another") says every variable a script
exports reaches the following commands. `exportKeyWithQuote` recognizes only one inline assignment
per line, so other valid forms are lost or break the command:

- `export FIRST SECOND=two` yields the key `FIRST SECOND`, because everything between `export`
  and the first `=` is taken as the name. `scriptFile` then appends
  `echo setvar FIRST SECOND=${FIRST SECOND}`, which the shell rejects as a bad substitution after
  the user's script body has run: the command is reported as failed and none of its exports reach
  the next command.
- `export FIRST=one SECOND=two` collects only `FIRST`.
- `FILE_NAME=/tmp/report` followed by `export FILE_NAME` collects nothing, since the line has no
  `=`, so the variable is not carried forward automatically. An explicit `register` or the next
  command's own `env` can still supply it. The table case "multiline command with shebang
  on non-first line" in `command_test.go` pins this: its `export BAR` gets no setvar line.

Splitting the arguments into separate names fixes the common cases; values with spaces or quotes
make a full parse harder, which is the open question. Surfaced by the revmux review of the secrets
export branch.
