---
worth: yes
where: cmd/spot/main_test.go:startTestContainer
added: 2026-11-19
---
# cmd/spot container tests fail intermittently with port "2222/tcp" not found

In one `go test -race ./...` run, `Test_runCompletedSeveralTasks` and `TestRunWithCustomTempDir` failed
inside `startTestContainer` at the `container.MappedPort(ctx, "2222")` check with `port "2222/tcp" not
found`, after the container was created and its wait strategy (`done.` log line plus
`wait.ForMappedPort("2222/tcp")`) returned. Both passed on rerun:
`go test -race -count=1 -run 'Test_runCompletedSeveralTasks|TestRunWithCustomTempDir' ./cmd/spot`.
No spot code runs before the failure.

Surfaced by the test run while reviewing #378, on a tree where `cmd/spot` was untouched. The cause is not
established yet; a fix needs a repro first, for example a loop of `-count=N` runs of those two tests.
