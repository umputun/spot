---
worth: later
where: pkg/runner/commands.go:219
added: 2026-09-04
---
# copyPush interpolates remote paths into shell commands unquoted

Three shell commands in `copyPush` take the destination straight from the playbook with no
quoting: `mkdir -p %s` in the non-sudo arm at commands.go:219, and `mv -f %s %s` plus
`chmod +x %s` in the sudo arm at commands.go:246 and :266. A `dst` containing a space splits into
two words, so `mv` reports a usage error and the copy fails after the file has already been
staged in the remote temp directory, leaving it behind.

This is a usability defect rather than an injection hole in normal use: the paths come from the
operator's own playbook, and an operator who can write the playbook can already run arbitrary
commands through `script`. It still means spot cannot write to a path with a space, which is
ordinary on macOS targets.

`wrapWithSudo` at commands.go:1071 already contains the POSIX single-quote escaping this needs
(`'` becomes `'\''`), written inline for the sudo password path. A helper carrying that escaping,
used by both, would settle the duplication and the three call sites together.

Surfaced during the review of #349, whose new `template` command quotes its own remote paths and
so reads as if the shared copy path does too.
