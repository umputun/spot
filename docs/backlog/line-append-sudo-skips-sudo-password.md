---
worth: later
where: pkg/runner/commands.go:Line
added: 2026-11-25
---
# line append with sudo never passes sudo_password to the write

With `sudo: true`, the append branch of `Line` builds `... | sudo tee -a <file>` itself and skips
`wrapWithSudo`, so `sudo_password` is never piped to `sudo -S` for the write. Only the `grep -q` check
before it goes through `wrapWithSudo` and gets the password. The `sudo tee -a` shape comes from #322,
which came after sudo_password support (0a8cad34).

Expected symptom, not yet reproduced: on a host whose sudo needs a password (no NOPASSWD), a `line`
append with `sudo: true` and `sudo_password` fails with "a terminal is required to read the password".
The grep and the write run as separate commands with no tty, so sudo's cached credentials should not
carry over. Confirm with a password-sudo container test first.

The obvious fix, `printf '%s\n' '<text>' | ` followed by `wrapWithSudo("tee -a <file>")`, does not work:
`wrapWithSudo` adds its own `printf '<password>' | sudo -S` in front of `tee`, so the text never reaches
`tee`, and when sudo's credentials are already cached it skips reading stdin and `tee` appends the
password to the file. How the password and the text share sudo's stdin is the design call. Surfaced
reviewing PR #380.
