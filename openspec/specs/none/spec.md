# none Specification

## Purpose
TBD - created by archiving change shared-test-fixtures. Update Purpose after archive.

## Requirements

### Requirement: Shared container fixtures with per-test database isolation

The `internal/testhelpers` package SHALL provide lazily-initialized shared containers (`SharedContainers`) that start at most once per test binary, and per-test isolated databases (`LazyPostgres.IsolatedDB`) within a shared Postgres container so tests remain independent without per-test container startup cost.

#### Scenario: package adopts shared fixture

- **WHEN** a test package declares a package-level `SharedContainers` and calls `Close()` from `TestMain`
- **THEN** the first test requesting a container starts it exactly once, subsequent tests reuse it, and `Close()` terminates only fixtures that started

#### Scenario: isolated database per test

- **WHEN** two parallel tests each call `IsolatedDB(t)`
- **THEN** each receives a distinct migrated database in the shared container, and each database is dropped when its owning test completes

#### Scenario: unit-only runs pay no container cost

- **WHEN** `go test -run <unit-test>` or `go test -short` executes in an adopting package
- **THEN** no container is started and `SharedContainers.Close()` is a no-op

#### Scenario: startup failure is replayed

- **WHEN** the shared container fails to start
- **THEN** every test requesting it fails with the recorded startup error, including after a panic inside the start function
