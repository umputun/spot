---
worth: yes
where: pkg/config/command.go:scriptFile
added: 2026-11-19
---
# multi-line commands fail for secret keys that are not shell names

`scriptFile` writes every non-empty secret as `export KEY="value"`. The README recommends provider
keys like `system-name/service-name/secret-key`, and the key `sudo_password` names must also be listed
in `secrets`, but `export deploy/sudo-password="pw"` is an invalid identifier. The default generated
script puts `set -e` before the exports, so bash stops at that line, and dash exits with status 2 on
it in any case. So a multi-line command listing a path-like secret key fails before its first line.
The exception is a custom bash shebang without errexit or POSIX mode: `set -e` follows the exports
there, so bash reports the invalid export and runs the body. A `#!/bin/bash -e` script still stops.

Single-line commands do not export such a key (`scriptCommand` checks `Cmd.isShellName`), but they
still emit `deploy/sudo-password="pw";`, which is not an assignment: the shell runs it as a command,
it fails with exit 127 and the shell reports the attempted command on stderr (dash prints
`deploy/sudo-password=pw: not found`; spot masks the raw value), and then the user's command runs. The fix is to skip generated assignments whose name
is not a shell name in both paths, using the same predicate; `wrapWithSudo` reads the password from
`cmd.Secrets` directly, so sudo keeps working.
