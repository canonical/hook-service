# Design: Centralize Integration Test Helpers

## Context

PR #286 ported the shell-based E2E suite into package-local `testcontainers-go` integration tests, deliberately keeping container helpers package-scoped (its own design decision 2: *"different packages require different combinations of containers"*). That decision was right for the migration and wrong as an end state: the helpers converged to near-identical copies with real drift. Issue #287 extracts them into one package. This design encodes the decisions agreed in exploration; the issue's own API sketch is the baseline.

## Goals / Non-Goals

**Goals:**
- One canonical implementation of every container/OAuth/migration helper, in `internal/testhelpers`.
- Identical container-start failure semantics in every package.
- Pinned image versions defined once as constants.
- Teardown owned by helpers via `t.Cleanup()`; callers never terminate containers.
- Phase 2: shared lazy fixtures so a package pays container startup once, not once per test.

**Non-Goals:**
- Server wiring (`httptest.NewServer` + `web.NewRouter`) abstraction.
- `internal/importer` migration — superseded: brought into scope per review (see Review Amendments).
- Aligning the Hydra test image with the docker-compose dev image (follow-up).

## Decisions

### D0 — Sequencing: branch from `refactor/e2e`, rebase after #286 merges

The duplicated code does not exist on `main`; it lives entirely in #286's new files. Implementation therefore starts from `refactor/e2e` and rebases onto `main` when #286 lands. No merge conflicts are expected beyond import blocks, since every migrated line is inside files #286 introduced.

- *Alternative*: wait for #286 to merge first. Rejected — serializes two workstreams for no benefit; worst case of stacking is one mechanical rebase.

### D1 — Failure semantics: fail fast, never skip

Container-start failure is `t.Fatalf`, uniformly. The existing `t.Skipf` + `recover()` guards + caller nil-checks dance (present in 4 of 5 helper copies) is deleted entirely.

- *Rationale*: silently skipped integration tests hide exactly the failures CI exists to catch. CI (`unittest.yaml`) already provisions rootless Podman and sets `DOCKER_HOST` before `make test`, so the prerequisite is enforced in practice. Developers running unit-only work use `go test -short ./...` — every integration test already guards on `testing.Short()`.
- *Consequence*: helper signatures return only the env struct (no nil-container sentinel, no early-return contract). AGENTS.md gains a testing-prerequisites note (Docker/Podman socket; `-short` for unit-only).
- *Alternatives*: keep `t.Skipf` (issue's original sketch). Rejected per maintainer decision — skips mask misconfigured environments as green builds.

### D2 — Naming: `SanitizeName(t.Name())` + nano suffix, generated inside helpers

Each helper derives its container name internally: `fmt.Sprintf("%s-%s-%d", prefix, SanitizeName(t.Name()), time.Now().UnixNano()%100000)`. The `suffix string` parameter on the current `pkg/authentication` and `internal/db` copies is deleted.

- *Rationale*: no test starts two containers of the same kind within a single test function (verified across all four call sites), so the suffix carried no information. Uniqueness requirements are: (a) within a package across `t.Parallel()` tests — covered by the nano suffix; (b) across packages — free, since `go test` runs each package as a separate process and testcontainers does not require global name uniqueness for correct operation (the name is for human debuggability and leak-hunting); (c) against leaked containers from prior runs — covered by the nano suffix.
- *Parallelism note*: `go test` already parallelizes across packages (separate binaries/processes); `t.Parallel()` within packages (used in `internal/db`) is preserved and safe under this scheme.

### D3 — Pin all images as constants

```go
const (
    postgresImage = "postgres:16-alpine"
    hydraImage    = "oryd/hydra:v25.4.0"
    openfgaImage  = "openfga/openfga:v1.10.0" // replace :latest
)
```

- *Rationale*: `:latest` makes CI non-reproducible and has already drifted from the pinned dev-compose world. The exact OpenFGA version is chosen at implementation time to match the SDK version in `go.mod`; the constant makes future bumps single-line.
- *Known divergence (documented, not fixed)*: dev compose runs `ghcr.io/canonical/hydra:2.3.0-canonical` while tests run `oryd/hydra:v25.4.0`. Aligning them is a separate decision with its own risk (the Canonical build carries patches); flagged in the Phase 1 PR description.

### D4 — `IntegrationClient` consolidation rides along as a separate commit

`pkg/groups` and `pkg/authorization` each define an identical `IntegrationClient` (`Request`/`CreateGroup`/`DeleteGroup`). It is not named in the issue, but it is the same class of drift. It moves to `internal/testhelpers` as its own commit inside the Phase 1 PR so the extraction and the call-site migration stay reviewable independently.

### D5 — `SetupOpenFGA` wires store, model, and client (per issue sketch)

`SetupOpenFGA` returns `*OpenFGAEnv` containing the container URLs *and* a configured `*openfga.Client` with `StoreID` and `AuthModelID` already set — mirroring what `pkg/authorization` does inline today: `CreateStore` → `SetStoreID` → `WriteModel` (model from `internal/authorization.NewAuthorizationModelProvider("v0").GetModel()`) → `SetAuthorizationModelID`. Returning only a URL would force every caller to repeat that wiring, recreating the drift we are removing.

- *Note*: the OpenFGA client needs tracer/monitor/logger for `openfga.Config`. Helpers construct noop implementations internally; callers needing a real instrumented client can build one from the returned URL/token/store/model fields.

### D6 — Phase 2 lazy fixtures: `sync.Once` + stored error, fail-fast replay

Under D1 there are no skip semantics anywhere, so the issue's "skip on subsequent calls if startup failed" collapses to: first call starts the container inside `sync.Once` and records the outcome; every call (first or later) fails `t.Fatalf` with the recorded error if startup failed. `Close()` on `SharedContainers` is a no-op when nothing started, keeping `TestMain` cleanup trivial and `-run TestPureUnit` runs container-free.

`IsolatedDB` starts simple: create a fresh database per test in the shared container and run migrations into it (drop in `t.Cleanup`). Template cloning (`CREATE DATABASE ... TEMPLATE`) is the fallback only if migration-per-test proves too slow — the template approach carries an active-connections constraint that is not worth paying upfront.

## Risks / Trade-offs

- **Risk**: fail-fast semantics break contributors on machines without a container runtime.
  → *Mitigation*: documented prerequisite; `-short` escape hatch already exists; CI is the source of truth for integration results anyway.
- **Risk**: rebasing onto `main` after #286 merges conflicts if #286 changes the helpers in review.
  → *Mitigation*: all changes are deletions-plus-import swaps in test files; conflicts resolve mechanically toward "use testhelpers".
- **Risk**: pinning OpenFGA to a version mismatched with the SDK could surface protocol errors.
  → *Mitigation*: pin to the version the vendored SDK targets; integration tests validate the pairing immediately.
- **Trade-off**: per-test `t.Cleanup` termination loses testcontainers' Ryuk-based reaping guarantees for tests that hard-crash. Accepted — same behavior as today's `defer Terminate`, and unique names make leaks findable.

## Verification Plan

1. `go build ./... && go vet ./... && golangci-lint run`
2. `go test -race ./...` — full suite with container runtime present; all existing integration tests pass unmodified in behavior.
3. `go test -short ./...` — zero containers started; unit suite green.
4. `go test -run TestGroupLifecycle ./pkg/groups/` (Phase 2) — shared container starts exactly once; `-run TestPureUnit` starts none.
5. Coverage per package must not drop >5% (helpers add lines but also delete duplicated uncovered lines; expect a net improvement).
6. License headers (`Copyright 2026 Canonical Ltd.` / `AGPL-3.0-only`) on all new files; no testify; standard-library assertions only.

## Review Amendments

Amendments agreed during PR review, superseding the earlier decisions noted:

- **D2 (naming) superseded**: explicit container names were dropped entirely. Testcontainers generates unique names itself and Ryuk reaps leaked containers, so `SanitizeName` and the whole prefix/nano-suffix naming scheme were deleted (`internal/testhelpers/sanitize.go` removed).
- **Phase 2 descoped**: shared fixtures (`shared.go`, `SharedContainers`, `LazyPostgres`/`LazyHydra`/`LazyOpenFGA`, `IsolatedDB`, D6) were removed from this PR and returned to follow-up status.
- **`SetupPostgres` returns a bare DSN**: `connStr := testhelpers.SetupPostgres(t)` with migrations folded into the helper; the `PostgresEnv` struct was deleted. `RunMigrations(t, connStr)` remains available for callers that manage their own containers.
- **`RunMigrations` uses `goose.NewProvider`**: a per-call provider instead of the package-level goose API, so parallel tests cannot race on goose's global base FS and dialect.
- **`HydraEnv.Issuer` added**: carries the configured issuer URL (the claim issued tokens bear), distinct from the mapped public address clients reach Hydra through.
- **`OpenFGAEnv` deleted**: `SetupOpenFGA` returns `*openfga.Client` directly (only the client was ever used by callers).
- **Dead nil-guards deleted**: 13 unreachable `if client == nil` guards removed across `pkg/authorization` and `pkg/groups` test files.
- **`IntegrationClient` fully consolidated**: `testClient` in `pkg/groups` and `pkg/authorization` embeds `*testhelpers.IntegrationClient`, deleting redundant in-file `Request` implementations.
- **Redundant Hydra tests merged**: `pkg/authentication/authenticator_test.go` deleted; single surviving test in `authentication_integration_test.go` uses `HydraEnv.Issuer`.
- **Scope expanded**: `internal/importer` migration was brought into this PR (previously a Non-Goal), eliminating `recover` -> `t.Skipf` repo-wide.
- **`make test-unit` added**: provides the documented unit-only path (`go test -short ./...`) for environments without a container runtime.
- **Obsolete E2E workflow & make target removed**: `.github/workflows/e2e-test.yaml`, `ci.yaml` e2e job, and `test-e2e` in `Makefile` deleted following `tests/e2e/` removal.
