---
worth: yes
where: README.md:1254
added: 2026-08-19
---
# README states a stale minimum Go version

The install section says **"pls note that you need to have go 1.16+ installed on your machine"**, directly
under the documented `go install github.com/umputun/spot/cmd/spot@master` and `git clone` + `make build`
paths. `go.mod` has required more than that for a while: the directive moved 1.24 -> 1.25 -> 1.26 without
the README ever being touched.

A reader on Go 1.16-1.20 who follows the stated minimum gets a hard `go.mod requires go >= 1.26.0` instead
of a binary, since toolchain auto-download only starts at 1.21.

Fix is one line, plus `make prep-site` — `site/docs-src/index.md:1254` is a generated copy carrying the
same sentence, so the regen is not optional.

Surfaced by three independent lenses while reviewing PR #361 (the Go 1.26 bump). Deferred there because it
predates that PR and belongs to the maintainer, not to the contributor's dependency branch.
