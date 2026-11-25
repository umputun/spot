---
worth: yes
where: pkg/runner/commands.go:wrapWithSudo
added: 2026-11-25
---
# sudo password escaping breaks under fish

`wrapWithSudo` puts the sudo password in `printf '%s\n' '<password>'` and escapes only `'` as
`'\''`. Under fish (a local `$SHELL` or the remote login shell) `\\` and `\'` are escapes even
inside single quotes, so a password with `\\` reaches sudo with one backslash and a password
ending in `\` makes the command fail to parse. Probe: `fish -c "printf '%s\n' 'a\\\\b'"` prints
`a\b`, where sh prints `a\\b`.

Fix: `escapedPassword := singleQuoted(password)`, the fish-safe helper from #380, with a
`wrapWithSudo` test for a password holding `\\` and a trailing `\`. The masking side of the new
spelling is in secret-masking-misses-encoded-forms.md. Surfaced reviewing PR #380.
