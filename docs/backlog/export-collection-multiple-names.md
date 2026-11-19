---
worth: maybe
where: pkg/config/command.go:exportKeyWithQuote
added: 2026-11-19
---
# export collection takes a multi-name export as one variable

`exportKeyWithQuote` treats everything between `export` and the first `=` as the key, so a valid
line such as `export FIRST SECOND=two` yields the key `FIRST SECOND`. `scriptFile` then appends
`echo setvar FIRST SECOND=${FIRST SECOND}`, which the shell rejects as a bad substitution after
the user's script body has run: the command is reported as failed and none of its exports reach
the next command. With `export FIRST=one SECOND=two` only `FIRST` is collected. Splitting the arguments into separate names fixes the common case; values with
spaces or quotes make a full parse harder, which is the open question. Surfaced by the revmux
review of the secrets export branch.
