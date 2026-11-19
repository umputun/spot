---
worth: later
where: pkg/config/command.go:scriptCommand
added: 2026-11-19
---
# a single quote in a single-line command breaks its sh -c '...' wrapper

`scriptCommand` wraps every single-line command as `<shell> -c '<body>'` and escapes no `'` inside
it. Remotely the login shell parses the wrapper first, so `echo 'a  b'` prints `a` (the quotes end
the wrapper and the rest becomes `$0`), `echo "it's"` fails with `unexpected EOF`, and a secret or
`env:` value with an apostrophe either drops its quotes or breaks the command. Script files and
local runs are not affected the same way.

Deferred until #378 settles. Design agreed with codex while reviewing #378:

- escape after templating, because `prepScript` substitutes `env:` values into the wrapped string
  afterwards and a template value can bring its own `'`; keep `GetScript`/`GetWait`/`GetCondition`
  as they are and give the runner an additive way to render the body after templating, then wrap
  it with each `'` written as `'\''`
- leave the sudo copy path and uploaded scripts outside the new wrapping (`commands.go` copy
  passes raw multi-line move text)
- `Local.Run` must decode only the canonical generated wrapper; local-run-strips-quoted-shell-wrapper.md
  stays open for the non-canonical double-quoted `wait` wrapper until that path is verified
- add mask forms for the escaped spelling: a secret `O'Brien` is masked today and would leak as
  `O'\''Brien` in the completion details, the SSH command echo and the `--dbg` `%q` spelling of that
  echo, which is why this waits for #378's `AllSecretValues` rewrite
- test the same bodies through the remote-shaped wrapper and `Local.Run`

Behavior change: valid inner shell syntax survives the outer wrapper after templating, so quoted
single-line remote commands stop being mangled; a playbook that relies on the current mangling (a hand-written `'"'"'`) changes output. `wait` and sudo `cond`
keep their extra `%q` layer until #378 removes it.
