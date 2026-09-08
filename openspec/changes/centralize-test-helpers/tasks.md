# Tasks: Centralize Integration Test Helpers

## 1. Phase 0 — Branch Setup

- [x] 1.1 Reset `IAM-2196` branch onto `origin/refactor/e2e` (or wait for #286 to merge and branch from `main`); PR targets `main` and rebases after #286 lands

## 2. Phase 1, Commit 1 — `internal/testhelpers` Package

- [x] 2.1 `internal/testhelpers/containers.go` — image constants (pinned `postgres:16-alpine`, `oryd/hydra:v25.4.0`, pinned OpenFGA version replacing `:latest`); `PostgresEnv`/`HydraEnv`/`OpenFGAEnv` typed returns
- [x] 2.2 `SetupPostgres(t *testing.T) *PostgresEnv` — container start, connection-string, ping-retry loop; teardown via `t.Cleanup`; hard `t.Fatalf` on any failure (no skip, no recover guards)
- [x] 2.3 `SetupHydra(t *testing.T) *HydraEnv` — container start with existing env/flags (`--dev`, memory DSN, JWT strategy), public/admin URL mapping; teardown via `t.Cleanup`; fail fast
- [x] 2.4 `SetupOpenFGA(t *testing.T) *OpenFGAEnv` — container start, `CreateStore`, `WriteModel` via `internal/authorization.NewAuthorizationModelProvider("v0").GetModel()`, configured `*openfga.Client` (noop tracer/monitor/logger) with store + model IDs set; teardown via `t.Cleanup`; fail fast
- [x] 2.5 `internal/testhelpers/migrations.go` — `RunMigrations(t *testing.T, connStr string)` (goose + `migrations.EmbedMigrations`)
- [x] 2.6 `internal/testhelpers/oauth.go` — `CreateHydraClient(t, hydra *HydraEnv, name string) (clientID, clientSecret string)` and `GetAccessToken(t, hydra *HydraEnv, clientID, clientSecret string) string` (client_credentials flow, 10s HTTP timeout)
- [x] 2.7 `internal/testhelpers/sanitize.go` — `SanitizeName(name string) string` (canonical implementation; container names built internally as `<prefix>-<SanitizeName(t.Name())>-<nano>`, no caller suffix param)
- [x] 2.8 AGPL-3.0-only headers on all new files; doc comments on every exported function; standard-library assertions only

Note: API amended during review — `SetupPostgres` returns a bare DSN (`PostgresEnv` deleted, migrations folded in), `sanitize.go`/`SanitizeName` removed (2.7 superseded), `HydraEnv.Issuer` added. See design.md "Review Amendments".

## 3. Phase 1, Commit 2 — Migrate Call Sites

- [x] 3.1 `pkg/groups/groups_integration_test.go` — delete `setupTestPostgres`, `runMigrations`, `sanitizeName`; use `internal/testhelpers`
- [x] 3.2 `pkg/authorization/authorization_integration_test.go` — additionally delete `setupTestOpenFGA` and the inline store/model wiring; use `SetupOpenFGA`
- [x] 3.3 `pkg/authentication/authentication_integration_test.go` — additionally delete `setupTestHydra`, `setupHydraClient`, `getJWTToken`; drop the `suffix` parameter plumbing
- [x] 3.4 `internal/db/replica_integration_test.go` — delete local copies; note in commit message that start-failure stays fail-hard (now uniform across packages) and skip-on-no-Docker is removed
- [x] 3.5 Migrate remaining in-package duplicates found during implementation: `pkg/groups/grpc_handlers_test.go`, `pkg/authorization/grpc_handlers_test.go`, `pkg/authentication/authenticator_test.go`, `pkg/groups/mapping_grpc_handlers_test.go` (reuses groups helpers)
- [x] 3.6 Verify no `setupTestPostgres`, `runMigrations`, `runMigrationsOnDSN`, `sanitizeName`, `setupTestOpenFGA`, `setupTestHydra`, `setupHydraClient`, or `getJWTToken` remain in the four packages
- [x] 3.7 `internal/importer/importer_test.go` — delete `setupTestPostgres`, `runMigrations`, `sanitizeName`; use `internal/testhelpers` (scope expanded per review; `salesforce_driver_test.go` is unit-only and needed no changes)

## 4. Phase 1, Commit 3 — Shared `IntegrationClient`

- [x] 4.1 Move the duplicated `IntegrationClient` (`Request`/`CreateGroup`/`DeleteGroup`) from `pkg/groups` and `pkg/authorization` into `internal/testhelpers/httpclient.go`; both packages use the shared type

## 5. Phase 1 — Verification & Docs

- [x] 5.1 `go build ./... && go vet ./... && golangci-lint run` clean
- [x] 5.2 `go test -race ./...` green with container runtime present; `go test -short ./...` green with zero containers started
- [x] 5.3 Coverage per package within 5% of baseline (expect net improvement from deleted duplicates)
- [x] 5.4 Update `AGENTS.md` testing section: integration tests require a Docker/Podman socket; `go test -short` is the unit-only mode; fail-fast (no skip) semantics are intentional
- [x] 5.5 PR description flags the Hydra image divergence (`oryd/hydra:v25.4.0` vs `ghcr.io/canonical/hydra:2.3.0-canonical`) as a follow-up

## 6. Phase 2 — Shared Fixtures (Follow-up PR)

Phase 2 (`shared.go` / `SharedContainers` / `IsolatedDB`) was REMOVED from this PR per review and returned to follow-up status.

- [ ] 6.1 `internal/testhelpers/shared.go` — `SharedContainers` with `LazyPostgres`/`LazyHydra`/`LazyOpenFGA`, each `sync.Once`-initialized with stored error replayed via `t.Fatalf` on every call after a failed start (deferred to follow-up PR per review)
- [ ] 6.2 `LazyPostgres.IsolatedDB(t *testing.T) string` — fresh database per test in the shared container, migrations applied, dropped in `t.Cleanup` (template cloning only if timing proves it necessary) (deferred to follow-up PR per review)
- [ ] 6.3 `SharedContainers.Close()` — no-op when nothing started; called from `TestMain` in adopting packages (deferred to follow-up PR per review)
- [ ] 6.4 Adopt the pattern in at least one reference package with a `TestMain` cleanup hook (deferred to follow-up PR per review)
- [ ] 6.5 Verify `go test -run TestPureUnit ./pkg/<adopted>/` starts zero containers; `go test ./pkg/<adopted>/` shares one container across tests (deferred to follow-up PR per review)
