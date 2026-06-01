# Contributing

## Developing

Please install the `pre-commit` to enforce the code conventions and alignment.

```shell
pip install pre-commit
```

Install the required pre-commit hooks.

```shell
pre-commit install -t commit-msg
```

## OpenSpec Workflow

Use this flow for spec-driven changes. The **spec is the single source of truth**: its `## Purpose` section carries the design rationale (intent, goals, non-goals, key decisions) ADR-style, so we don't keep separate ADR/design files.

1. Propose: `openspec new change <name>` and complete `proposal.md`, `design.md`, `tasks.md`, and delta specs.
2. Implement: complete tasks in `openspec/changes/<name>/tasks.md`.
3. Validate: run `openspec validate --all`. This is the structural/drift check — it fails on malformed specs or deltas.
4. Archive: run `openspec archive <name>`. This updates the main specs (applies deltas into `openspec/specs/`) and moves the change under `openspec/changes/archive/`. There is no separate `sync` command; `archive` is the sync.

Commit active changes under `openspec/changes/` so they're reviewed in their PR. The archive (`openspec/changes/archive/`) is gitignored — its rationale already lives in the spec `## Purpose`.

## Spec Writing Policy (Human + Agent)

Keep specs layered so they are readable by humans and usable by agents:

1. `## Purpose` is the **why**: concise context, goals/non-goals, key decisions, and major trade-offs.
2. `## Requirements` is the **what**: normative, testable behavior using `MUST`/`SHALL`/`SHOULD`/`MAY` with scenario blocks.
3. Do not copy proposal/design/tasks verbatim into specs.
4. Keep implementation planning detail in change artifacts (`design.md`, `tasks.md`), not in canonical specs.
