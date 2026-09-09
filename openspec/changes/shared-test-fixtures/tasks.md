# Tasks: Shared Integration Test Fixtures

## 1. `internal/testhelpers/shared.go`

- [x] 1.1 `SharedContainers` struct with `LazyPostgres`/`LazyHydra`/`LazyOpenFGA`; `Close()` terminates only started fixtures (no-op-safe)
- [x] 1.2 `lazyShared.ensure(t, start)` — `sync.Once` + recorded outcome replayed via `t.Fatalf`; `start` wrapped in `defer recover()` converting panic to `startErr`
- [x] 1.3 `LazyPostgres.IsolatedDB(t)` — `CREATE DATABASE` with `pgx.Identifier{...}.Sanitize()` quoting and a short random suffix (no `t.Name()`), `RunMigrations` applied, `t.Cleanup` terminates lingering connections then drops the DB; `t.Logf` maps test → db name
- [x] 1.4 `LazyHydra.Env(t)` / `LazyOpenFGA.Env(t)` — lazy wrappers over the existing `startHydra`/`startOpenFGA` helpers (no `t.Cleanup` inside; `Close()` owns lifecycle)
- [x] 1.5 AGPL-3.0-only header, doc comments on all exported items

## 2. Reference adoption in `internal/db`

- [x] 2.1 `var shared testhelpers.SharedContainers` + `TestMain(m)` calling `shared.Close()` in `internal/db/replica_integration_test.go`
- [x] 2.2 `TestIntegration_ReplicaUnconfigured` and `TestIntegration_MetricsValidation` switch to `shared.Postgres.IsolatedDB(t)`
- [x] 2.3 `TestIntegration_ReplicaLagFallback` keeps two per-test `SetupPostgres` calls (shared fixtures are single-container by design)

## 3. Verification

- [x] 3.1 `go build ./... && go vet ./...` clean
- [x] 3.2 `go test -race -count=1 ./internal/db/` and `-count=2` green
- [x] 3.3 `go test -short ./...` green with zero containers started
- [x] 3.4 `go test -run TestOffset ./internal/db/` starts zero containers; `go test -run TestIntegration_MetricsValidation ./internal/db/` starts exactly one
- [x] 3.5 Record before/after `internal/db` runtime in the PR description
