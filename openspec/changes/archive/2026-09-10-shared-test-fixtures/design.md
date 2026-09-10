# Design: Shared Integration Test Fixtures

## Context

Phase 1 left per-test container startup as the cost model. This design restores the Phase 2 shared-fixture mechanism descoped from PR #346, incorporating the two defects the review identified so they are fixed by construction rather than reintroduced.

## Goals / Non-Goals

**Goals:**
- One container per (package binary, dependency) instead of one per test.
- Full per-test database isolation preserved (no shared mutable schema).
- Fail-fast semantics consistent with Phase 1 (no skips anywhere).
- `-run TestPureUnit` and `go test -short` pay zero container cost.

**Non-Goals:**
- Repo-wide adoption (internal/db is the reference only).
- Template-database cloning (fallback only if migrations-per-test is too slow).
- Changing the public `Setup*` per-test API (it stays; lazy fixtures are additive).

## Decisions

### S1 — `sync.Once` + stored outcome, replayed via `t.Fatalf`

Each `Lazy*` type starts its container inside `sync.Once`. The outcome (success state or `startErr`) is recorded; every call — first or later — fails `t.Fatalf` with the recorded error if startup failed.

*Rationale*: consistent with the Phase 1 no-skip decision (review-established). The first caller's `t` may have returned by the time a later test runs, so `t.Fatalf` inside the `Once` is unsafe for the *first* caller too — hence record-and-replay through `ensure(t)`.

### S2 — Panic safety inside `start` (review item 12)

`sync.Once` marks done even if `start` panics, which would leave `startErr` nil and every later caller nil-dereferencing. Fix: `start` is wrapped in a `defer recover()` that converts a panic into the recorded `startErr`. The doc comment's promise ("recorded error is replayed") then holds in all paths.

### S3 — Database naming: random suffix + `pgx.Identifier` quoting (review item 2)

`IsolatedDB` builds names as `test_<6 random alnum>` — no `t.Name()` at all. The name is quoted with `pgx.Identifier{dbName}.Sanitize()` in `CREATE DATABASE`/`DROP DATABASE`. This is immune to:
- `/` and spaces (no sanitization needed — random alphabet only),
- the 63-char identifier truncation (fixed short length),
- collisions between long test names (random, not name-derived),
- the 5-digit nano collision the review flagged.

Trade-off accepted: database names are no longer human-mappable to tests. Debuggability is preserved via a `t.Logf` line mapping test → database name.

### S4 — `IsolatedDB`: fresh DB + migrations, not template cloning

Per-test `CREATE DATABASE` + `RunMigrations` + `DROP DATABASE` (in `t.Cleanup`, after terminating lingering connections via `pg_terminate_backend`). Template cloning is the documented fallback; its active-connections constraint (template must have zero sessions) makes it the second choice, not the first.

### S5 — Reference adopter: internal/db, single-container tests only

`internal/db` gains `var shared testhelpers.SharedContainers` + `TestMain` with `shared.Close()`. `TestIntegration_ReplicaUnconfigured` and `TestIntegration_MetricsValidation` (one container each) switch to `shared.Postgres.IsolatedDB(t)`. `TestIntegration_ReplicaLagFallback` (two containers) keeps per-test `SetupPostgres` — a shared fixture is a single container by design and a second `IsolatedDB` in the same container would not test replica routing across instances.

### S6 — `SharedContainers.Close()` is idempotent and no-op-safe

`Close()` terminates only fixtures that actually started. `TestMain` calls it unconditionally; packages that run only unit tests never start anything and `Close` costs nothing.

## Risks / Trade-offs

- **Risk**: parallel tests sharing one Postgres container contend on connections.
  → *Mitigation*: the container's default pool is large relative to test concurrency; measured during #346 with no flakes. `MaxConns` in test configs stays small (5).
- **Risk**: a hard test crash bypasses `t.Cleanup`, leaking databases in the shared container.
  → *Mitigation*: ephemeral per binary run; Ryuk reaps the container at process end regardless.
- **Trade-off**: `LazyHydra`/`LazyOpenFGA` are delivered but unadopted — completeness for future packages, matching the issue's original API sketch. If reviewers prefer strict minimalism they can be cut; they cost ~60 lines.

## Verification Plan

1. `go build ./... && go vet ./...` clean.
2. `go test -race -count=1 ./internal/db/` — both adopter tests pass on one shared container.
3. `go test -race -count=2 ./internal/db/` — parallel `IsolatedDB` under race detector (the Phase 1 goose-race class of bug must stay dead).
4. `go test -short ./...` — zero containers started.
5. `go test -run TestIntegration_MetricsValidation ./internal/db/` — container starts exactly once; `go test -run TestOffset ./internal/db/` — zero containers (TestMain Close is a no-op).
6. Time comparison recorded in the PR description (before/after internal/db runtime).
