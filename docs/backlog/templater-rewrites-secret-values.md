---
worth: later
where: pkg/runner/commands.go:prepScript
added: 2026-11-19
---
# the templater rewrites variable references inside secret values

`prepScript` applies the templater to the whole single-line command and to the uploaded script
after secrets are already written into them, so a secret containing `$SPOT_REMOTE_HOST`, `{SPOT_TASK}`
or `${VAR}` for a configured `env:` key gets that text replaced with the host, task or env value. The
rewritten value can also slip past masking. Surfaced reviewing #378; unlikely in real secrets, which
is why it waits.
