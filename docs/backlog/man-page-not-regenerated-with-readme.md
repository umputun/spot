---
worth: later
where: Makefile:51
added: 2026-08-24
---
# spot.1 goes stale because only make prep-site is documented

`spot.1` is generated from README by the `man` target and ships in every release archive
(`.goreleaser.yml:74`), but `make man` is a separate target from `make prep-site` and nothing tells a
contributor to run it. CLAUDE.md says only that doc site generation should be run with `make prep-site`
when README.md is updated.

The result is a man page that drifts from README without anyone noticing. PR #349 shows the shape: the
contributor added a `template` command with full README coverage and ran `make prep-site` (commit
d83ca523), so `site/docs-src/index.md` and `site/docs/llms.txt` are current while `spot.1` documents no
`template` command at all. The `line` command PR (3d543046) did regenerate it, so the step is real and
just undocumented.

Two parts to this. Name `make man` beside `make prep-site` in CLAUDE.md so the next public-doc change
cannot miss it, and regenerate `spot.1` whenever a README change with new commands merges. A CI check
that fails when README is newer than spot.1 would remove the need to remember at all.
