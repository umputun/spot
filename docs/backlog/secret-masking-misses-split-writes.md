---
worth: later
where: pkg/executor/logger.go:91
added: 2026-08-23
---
# secret masking misses a secret split across two writes

Both log writers mask each `Write` buffer on its own and carry nothing into the next call:
`colorizedWriter.Write` at logger.go:91 and `stdOutLogWriter.Write` at logger.go:205. Neither
execution path delivers whole lines. `Local.Run` sets `command.Stdout` to
`io.MultiWriter(outLog, capture)`, a non-file writer, so os/exec creates a pipe and `io.Copy`
writes at arbitrary read boundaries; `Remote.sshRun` does the same onto `session.Stdout`, and
x/crypto/ssh copies from a byte-stream channel in 32KiB packets. A command printing the first
half of a configured secret, pausing, then printing the rest matches neither compiled regexp,
and both halves reach the log verbatim.

The capture side already solves this: `lineCapture` in executor.go stages a partial line across
writes and flushes an unterminated tail in `result()`. The logger needs the same shape —
command-local staging plus an explicit end-of-command flush — which is why this was not a
one-line fix done inline.

Predates the masking rewrite in #365; that change moved pattern compilation into `MakeLogs` and
preserved the chunk-scoped mechanism unchanged. Surfaced by both agents of a revmux round on
that PR and confirmed independently. Rated moderate rather than critical: spot is an
operator-run CLI masking secrets the operator already holds, so the exposure is log hygiene,
though logs get persisted and shared.
