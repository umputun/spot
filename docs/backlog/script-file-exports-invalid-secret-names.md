---
worth: yes
where: pkg/config/command.go:scriptFile
added: 2026-11-19
---
# multi-line commands fail for secret keys that are not shell names

`scriptFile` writes every non-empty secret as `export KEY="value"`. The README recommends provider
keys like `system-name/service-name/secret-key`, and the key `sudo_password` names must also be listed
in `secrets`, but `export deploy/sudo-password="pw"` is an invalid identifier. In the default
generated script `set -e` comes before the exports, so bash stops at that line, and dash exits with
status 2 on it in any case. So a multi-line command listing a path-like secret key fails before its
first line on master. With an explicit shebang the exports come before the shebang's `set -e`. Single-line commands keep such keys as a
bare assignment and run (`scriptCommand` exports only valid names). The fix is to skip generated
assignments whose name is not a shell name, consistent with the single-line guard (`Cmd.isShellName`); `wrapWithSudo` reads the password
from `cmd.Secrets` directly, so sudo keeps working.
