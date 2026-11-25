---
worth: later
where: .github/workflows/ci.yml
added: 2026-11-25
---
# CI likely skips the fish quoting tests

`Test_singleQuotedReachesShellUnchanged` (#380) and `TestCmd_secretsReachShellUnchanged` skip any
shell that is not installed, and CI runs on ubuntu-latest with no install step. Only fish tells a
fish-safe escaper apart from a POSIX-only one, since sh, bash, dash and zsh keep backslashes
inside single quotes literal. If fish is absent from the runner image, which a `-v` CI run would
confirm, a change that drops the `'\\'` backslash case passes CI and breaks local runs under a
fish `$SHELL`.

Fix: `sudo apt-get install -y fish` before `go test` in ci.yml. Surfaced reviewing PR #380.
