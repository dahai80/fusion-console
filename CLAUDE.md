# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

> Placeholder — written when `fusion-console/` was still empty. Update this file with real
> build/test/lint commands and architecture notes as soon as code lands.

## Status

`fusion-console` is a new sub-project of the Fusion monorepo (`/Users/dahai/fusion`). Directory
currently contains no source code. Like every `fusion-*` sub-project it is an independent git
repository (`git init` here when work starts); commit/branch/PR inside this directory, never at
the repo root.

## Environment

Shared monorepo venv — activate before any Python work:

```bash
cd /Users/dahai/fusion
source .venv/bin/activate
```

## Monorepo conventions (apply from first line of code)

- Python: 3.12 (repo-root `.python-version`), setuptools backend, `pytest tests/` with
  `asyncio_mode=auto`. Dependencies go through root `requirements.lock` via
  `scripts/sync-deps.sh`; internal cross-projects use `pip install -e`.
- If the project exposes an HTTP service: register its port in
  `/Users/dahai/fusion/architecture/port-registry.yaml` and validate with
  `python scripts/check-ports.py`. Fixed core ports: fusion-mlx 11434, fusion-gateway 11432,
  fusion-memory 11435.
- Reuse in-tree foundations instead of rebuilding: `fusion-core` (LLM client / JSON parse /
  config / logging / FastAPI factory), `fusion-harness` (agent kernel), `fusion-identity`
  (auth), `fusion-gateway` routing instead of hitting `:11434` directly.
- Code comments and log messages: English only. GitHub artifacts: English only. User-facing
  product docs: bilingual (README.md English, README_CN.md Chinese).
- Code style: 4-space multiples indentation, no docstrings, logging on all code paths.
- Frontend (if applicable): React/Next.js + Tailwind + shadcn/ui, `lucide-react` icons only,
  Slate/Zinc neutrals + Indigo/Violet/Emerald accents, full dark-mode support.
