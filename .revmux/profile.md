# Project profile: spot

**What it is.** A single-binary Go CLI for deployment and configuration management. An operator writes
a playbook of tasks and runs it against hosts he owns, over SSH or locally. No server, no daemon, no
long-running process, no multi-tenancy. It can also be used as a library: `pkg/executor`,
`pkg/config`, `pkg/runner` and `pkg/secrets` are exported and importable.

**What a real failure looks like.** A command runs on the wrong host, runs twice, or silently does not
run at all. A copy, sync or delete touches the wrong path on a remote machine. A failing command
reports success, so a broken deploy looks clean. A playbook that used to work stops parsing. Concurrent
execution corrupts shared state or deadlocks. Delete with recursion or a glob removing more than the
playbook named is the worst case, because it is not recoverable from the tool.

**Blast radius.** Whatever the operator points it at, which can be production servers. Changes are
applied over SSH and are not transactional, so a wrong command is already run by the time it is seen.
There is no rollback. The audience for a defect is the operator running the playbook, plus anyone
importing the packages.

**Who runs and maintains it.** Solo maintainer, umputun, with a handful of regular outside
contributors. No on-call, no support rotation. Most changes arrive as pull requests or issues with a
patch attached.

**The reporting bar.**

- Correctness in command execution, targeting, concurrency and file operations is the top concern.
  Report anything that could run the wrong thing, run it in the wrong place, or misreport the result.
- Treat masking gaps as log-hygiene issues unless evidence establishes exposure beyond the secrets'
  intended audience. Before classifying a security finding, trace the reachable path, trust boundary
  and actual exposure, including persisted or shared logs. Operator access to a secret does not
  establish who may read the output, and secrets can come from Vault, AWS Secrets Manager or Ansible
  Vault rather than from the person at the terminal. Prefer small fixes that preserve the existing
  masking rules. Require demonstrated material impact before proposing stateful writers, new
  lifecycles or extensive test matrices. Do not reopen documented limitations in unrelated reviews
  without evidence that the change worsens them or materially changes their impact.
- The exported packages are importable, so a caller-visible regression in `pkg/executor`,
  `pkg/config`, `pkg/runner` or `pkg/secrets` is worth reporting even when nothing in the binary calls
  the changed path. Reachability from the binary is not the only bar, and an intentional compatible
  change is not a regression.
- Log output is a user interface here, not incidental. Wrong or misleading output to the operator is a
  real defect.
- A performance finding has to establish material cost on a workload spot actually runs, such as
  per-line work on command output or anything buffering output without bound. An existing benchmark is
  not the only acceptable evidence, but an unquantified allocation is not a finding.

**Deliberate conventions.** These suppress preference findings, not a demonstrated failure caused by
following one.

- Comments are lowercase except godoc, which starts with the element name. Tests carry no comments at
  all, apart from one lowercase line naming the defect a regression test pins.
- Early returns over nesting. No `else if` chains, no `goto`. `else` is avoided where a rewrite is
  natural, and a `switch` replaces a long chain.
- Interfaces are declared by the consumer. Functions accept interfaces and return concrete types.
- Mocks are generated with moq into a `mocks` subpackage via `go:generate` and are never hand-edited.
- One test file per source file: `foo.go` has `foo_test.go` and nothing else. Table-driven with
  testify, struct fields on one line up to 130 characters.
- Private by default; export only when there is an out-of-package caller.
- The linter config in `.golangci.yml` is the authority on style. Do not report something it is
  configured to allow, and do not propose disabling one of its linters.

**Known limitations already recorded.** `docs/backlog/` holds items that are real and deliberately not
fixed yet, including a secret split across two write boundaries escaping masking. Read that directory
before reporting something as new.
