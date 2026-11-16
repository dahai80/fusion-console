# Vendored skills — generated, do not edit by hand

Every directory here is copied verbatim from the upstream commit pinned in
`../catalog.json` by `make skills-sync` (`go run ./cmd/skillsync`). Each
directory carries the upstream license file (`LICENSE.txt`, or `LICENSE`
copied from the repository root for sources that license at repo level).

To add, remove or update a skill: edit `../catalog.json` (name, source,
category; or bump a source's `commit`), run `make skills-sync`, and review
the result with `git diff`. Only MIT and Apache-2.0 sources are accepted
(`skillcatalog.Validate`, enforced by the tests).

| Source | License | Upstream |
|---|---|---|
| `anthropics` | Apache-2.0 | https://github.com/anthropics/skills |
| `superpowers` | MIT | https://github.com/obra/superpowers |
| `openai` | Apache-2.0 | https://github.com/openai/skills |
