# Shared Integration Test Fixtures

## Why

Phase 1 (`centralize-test-helpers`, PR #346) centralized container setup into `internal/testhelpers`, but every test still pays full container startup cost: each `SetupPostgres` call boots a fresh Postgres container (~5–10s each), and a package with N container tests boots N containers. This was the accepted Phase 1 trade-off — correctness and consolidation first.

This change delivers the Phase 2 goal deferred from #287: **package-level container reuse with per-test database isolation**, cutting integration suite runtime substantially.

The Phase 2 implementation was originally built, then descoped during #346 review for two reasons:

1. Its only adopter (`internal/db`) was lost during the rebase onto `main`, leaving dead code in the tree.
2. `IsolatedDB` built invalid Postgres identifiers from test names (`SanitizeName` maps `/` → `-`, producing `CREATE DATABASE test_testfoo-bar_123` — a syntax error) and used `t.Name()` with a 5-digit nano suffix that could collide.

Both issues are addressed by design in this change rather than by restoring the deleted code verbatim.

## What Changes

- Add `internal/testhelpers/shared.go` with `SharedContainers` holding `LazyPostgres`/`LazyHydra`/`LazyOpenFGA` fixtures, each `sync.Once`-initialized on first use within a test binary.
- `LazyPostgres.IsolatedDB(t)` returns a fresh database in the shared container with migrations applied, dropped via `t.Cleanup` — full per-test isolation without per-test container cost, safe under `t.Parallel()`.
- `SharedContainers.Close()` is a no-op when nothing started; called from `TestMain` in adopting packages so `-run TestPureUnit` runs never pay container cost.
- Failed container startup is recorded and replayed to every dependent test via `t.Fatalf` (fail-fast, consistent with the Phase 1 no-skip decision). A panicking `start` function is recovered and recorded as the startup error, so later callers get the error rather than a nil-deref.
- Adopt the pattern in `internal/db` (the single-container replica tests) as the reference package, with `TestMain` + `Close()`.
- Database names are built from a short random suffix (not `t.Name()`), quoted via `pgx.Identifier{...}.Sanitize()` — immune to `/`, spaces, length truncation, and collisions.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. Test infrastructure only; no runtime behavior, API contracts, or schema affected.

## Non-goals

- **Mandating adoption across all packages.** Only `internal/db` adopts here as the reference. Wider adoption is a later, mechanical follow-up per package.
- **Template-database cloning** (`CREATE DATABASE ... TEMPLATE`). The simple approach (fresh DB + migrations per test) is implemented first; template cloning is the documented fallback only if migration-per-test proves too slow.
- **Shared Hydra/OpenFGA adoption.** `LazyHydra`/`LazyOpenFGA` exist for completeness but no package is forced onto them; per-test `SetupHydra`/`SetupOpenFGA` remain correct defaults.

## Impact

### Affected Code

| File | Change |
|------|--------|
| `internal/testhelpers/shared.go` | **New** — `SharedContainers`, `LazyPostgres`/`LazyHydra`/`LazyOpenFGA`, `IsolatedDB` |
| `internal/db/replica_integration_test.go` | Adopt `SharedContainers` + `TestMain`; single-container tests use `IsolatedDB`; the two-container lag test keeps per-test `SetupPostgres` |

### Dependencies

No new dependencies. `sync`, `pgx` (for `Identifier.Sanitize`) already present.

### Performance Regressions

- **Improvement**: `internal/db` drops from per-test Postgres containers to 1 shared container per binary for single-container tests. Measured during #346: internal/db 14.6s → 8.7s with shared fixtures.
- **Risk**: a leaked `IsolatedDB` database accumulates in the shared container if `t.Cleanup` is bypassed (hard test crash). Acceptable — the container is ephemeral per binary run.
