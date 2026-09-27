---
worth: later
where: pkg/executor/connector.go:sshClient
added: 2026-09-27
---
# a stalled ssh agent hangs the ssh handshake with no timeout

With `--ssh-agent`, `sshConfig` hands `agent.NewClient(aconn).Signers` to `ssh.PublicKeysCallback`, and
`sshClient` then runs `ssh.NewClientConn` on the dialed TCP connection. Nothing bounds the agent side:
the unix connection to `SSH_AUTH_SOCK` has no deadline, `ClientConfig.Timeout` is unset, and `ctx` does not
reach the handshake. An agent that accepts the connection but never answers (a wedged or SIGSTOPped agent,
a forwarded agent socket over a half-dead session) blocks `Signers()` forever. `run()` catches SIGINT via
`signal.NotifyContext`, so Ctrl-C does not stop it either; the user sees spot stop after the ssh key log
line and has to kill it.

`net.Dialer{Timeout: c.timeout}` bounds only the TCP dial. Every remote run and every `--dry` run goes
through this path.

Surfaced reviewing PR #371, which adds a setup-time `List()` in `resolvePreferredKey` with the same
shape; that one was asked for a `SetDeadline` in the PR. The same fix works here: set a deadline on the
agent connection before the handshake, keeping `--timeout=0` as no deadline. A read deadline fails the
agent client's `readLoop`, which shuts its pipeline down and returns the error to the waiting call.
No reports of this happening in real use.
