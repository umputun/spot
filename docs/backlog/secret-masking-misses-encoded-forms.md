---
worth: later
where: pkg/config/playbook.go:AllSecretValues
added: 2026-11-19
---
# secret masking misses the encoded spellings of a secret in commands and previews

`AllSecretValues` gives the masker raw values only, but commands carry secrets encoded: on master
`getSecrets` writes them with `%q`, so a multi-line secret such as a PEM key appears in the `-v`
command echo and the dry-run script preview as one line with `\n` escapes, and the `--dbg` `run ssh command %q` line
(`remote.go:sshRun`) prints the `%q` spelling of that already Go-quoted command, so a tab or newline
secret is quoted twice there.
None of those spellings match.

Once #378 lands, commands carry raw newlines instead, and the line-splitting writers
(`colorizedWriter.Write`, `stdOutLogWriter.Write`, and the dry-run `Upload` preview that writes per
line) mask each line on its own, so a multi-line secret still escapes. The `-vv` script copy is a
separate case: `prepScript` inserts ` + ` before each line, and the report masks the formatted text
only after that has broken the secret apart. The fix is to mask a whole command or preview
buffer before splitting it, keep masking the formatted host prefix (#369), redact the `-vv` copy
before adding prefixes, and add the `%q` spelling of each secret as actually emitted regardless of
whether shell escaping changed it.

Deferred until #378 settles: `AllSecretValues` is the function #378 rewrites, and on master the
split-order part alone fixes no visible case. Separate from secret-masking-misses-split-writes.md,
which is about command output split across writes.

The sudo password has one more spelling. `wrapWithSudo` (`pkg/runner/commands.go`) puts it in a
single-quoted `printf` argument with each `'` written as `'\''`, so a password such as `pa'ss`
appears as `pa'\''ss` in the `-v` command echo and in the `%q` spelling of the `--dbg` line, and
neither matches. `AllSecretValues` cannot tell which secret is the sudo password, so add that
spelling and its `%q` form for every secret containing `'`.

The `line` command adds two more spellings (#380). `Line` writes `append` text as
`singleQuoted(v)` (`'` as `'\''`, `\` as `'\\'`) and `replace` text as `singleQuoted` of the
sed-escaped value (`\` as `\\`, `&` as `\&`, `|` as `\|`), so a secret such as `demo&value`
exported by a script and used in `replace` shows as `demo'\\'&value` in the `-v` echo and in the
`%q` form of the `--dbg` line. Add both spellings and their `%q` forms for every secret they change.
