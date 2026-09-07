---
worth: later
where: pkg/executor/local.go:41
added: 2026-09-07
---
# Local.Run un-nests a shell wrapper by trimming one quote, corrupting quoted bodies

`Local.Run` at local.go:41-45 looks for a `<shell> -c ` prefix, where `<shell>` is `$SHELL` or
`/bin/sh`, and when it matches strips the prefix plus one leading and one trailing `'`. The intent is
to avoid a double shell. The trim is blind to what the body contains, so a body that carries its own
quoting comes out unbalanced: a POSIX-escaped `'\''` inside the wrapper is left with a dangling
quote and `/bin/sh` fails with `unexpected EOF while looking for matching '`, and a `%q`-quoted body
loses its outer double quotes and runs as a bare word (`/bin/sh -c '"echo hi && echo there"'` ends in
`command not found`).

Every `%s -c %q` wrapper in `pkg/runner/commands.go` (:125, :336, :684) is sudo-prefixed, so the
prefix never matches there. The one exposed today is the unconditional wait wrap at commands.go:523,
which hits this whenever `--shell` is empty or equal to `$SHELL` on a `--local` run. #349's template
probe hit the same thing and works around it with an `exec ` prefix.

A fix is either to stop un-nesting altogether, since `exec.Command(shell, "-c", cmd)` already runs
the string through a shell and a nested `sh -c '...'` is only cosmetic, or to un-nest only when the
body holds no quote characters. Both need a test that drives a quoted wrapper through `Local.Run` and
compares stdout with the unwrapped form.
