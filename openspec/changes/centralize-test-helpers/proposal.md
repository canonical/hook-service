# Centralize Integration Test Helpers

## Why

The integration tests introduced in [#286](https://github.com/canonical/hook-service/pull/286) each independently define the same container setup helpers. `setupTestPostgres` (7 copies, 3 dialects), `runMigrations`/`runMigrationsOnDSN` (6 copies), `sanitizeName` (7 copies), the Hydra setup + OAuth helpers (2 copies), and `setupTestOpenFGA` are copy-pasted across `pkg/groups` (3 files), `pkg/authorization` (2 files), `pkg/authentication` (2 files), `internal/db`, and `internal/importer`. An `IntegrationClient` HTTP helper is additionally duplicated in `pkg/groups` and `pkg/authorization`.

This causes two concrete problems (tracked as [#287](https://github.com/canonical/hook-service/issues/287), Jira IAM-2196):

1. **Maintenance drift** — a change to a container image version or a startup flag must be replicated in many files; it is easy to miss one.
2. **Inconsistency** — divergences already exist: different container name prefixes (including a copy-paste artifact where `pkg/groups` names its container `hook-authz-*`), different container-start failure semantics (`t.Skipf` everywhere except `t.Fatalf` in `internal/db`), and slightly different retry loops.

Two latent defects compound this:

- Tests run `openfga/openfga:latest` — unpinned, non-reproducible CI.
- Tests run `oryd/hydra:v25.4.0` while `docker-compose.dev.yml` runs `ghcr.io/canonical/hydra:2.3.0-canonical` — the dev environment and the test suite exercise different Hydra builds.

## What Changes

- Create `internal/testhelpers` as a regular (non-`_test`) package. Living under `internal/` keeps it importable by any test in the module while preventing leakage to external consumers; the helpers encode project-specific internals (`internal/db`, embedded migrations, `internal/authorization` model wiring), so the visibility restriction is load-bearing.
- Consolidate the duplicated helpers behind a small, typed API: `SetupPostgres`, `SetupHydra`, `SetupOpenFGA`, `RunMigrations`, `CreateHydraClient`, `GetAccessToken`, plus a shared `IntegrationClient` (separate commit). `SanitizeName` was dropped per review — testcontainers generates unique container names and Ryuk reaps leaks (see design Review Amendments).
- Pin all container images as named constants; move OpenFGA off `:latest`.
- Unify failure semantics: container-start failure is a hard `t.Fatalf`. Docker/Podman availability is a documented prerequisite for integration tests; `go test -short` is the unit-only escape hatch (existing tests already honor `testing.Short()`).
- Helpers register teardown via `t.Cleanup()` — callers never manage container lifecycle.
- Migrate the in-scope packages (`pkg/groups`, `pkg/authorization`, `pkg/authentication`, `internal/db`) and delete all local duplicates. Scope was expanded per review to also migrate `internal/importer`.
- Phase 2 was descoped from this PR per review and returned to follow-up status: `SharedContainers` with lazy `LazyPostgres`/`LazyHydra`/`LazyOpenFGA` fixtures and per-test isolated databases will land in a follow-up PR.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. This change touches test infrastructure only; no runtime behavior, API contracts, or schema are affected.

## Non-goals

- **Server wiring abstraction**: each package continues building its own `httptest.Server` + `web.NewRouter` wiring. The router wiring differs meaningfully per package (authorization wires OpenFGA, authentication wires a JWT verifier) — abstracting it now would be premature.
- **`SetupAll` / full-stack fixture**: deferred until a genuine full-stack test needs it (per issue: minimal dependencies principle).
- **Hydra image alignment with docker-compose** (`oryd/hydra:v25.4.0` vs `ghcr.io/canonical/hydra:2.3.0-canonical`): flagged in the PR description as a follow-up, not fixed here.

## Impact

### Affected Code

| File | Change |
|------|--------|
| `internal/testhelpers/containers.go` | **New** — image constants, `SetupPostgres`, `SetupHydra`, `SetupOpenFGA` |
| `internal/testhelpers/migrations.go` | **New** — `RunMigrations` |
| `internal/testhelpers/oauth.go` | **New** — `CreateHydraClient`, `GetAccessToken` |
| `internal/testhelpers/httpclient.go` | **New** — shared `IntegrationClient` (separate commit) |
| `internal/testhelpers/shared.go` | **Deferred to follow-up PR (per review)** — `SharedContainers`, lazy fixtures, `IsolatedDB` |
| `pkg/groups/groups_integration_test.go` | Delete local helpers, use `internal/testhelpers` |
| `pkg/groups/grpc_handlers_test.go` | Delete local helpers, use `internal/testhelpers` |
| `pkg/groups/mapping_grpc_handlers_test.go` | Use `internal/testhelpers` (reused groups helpers) |
| `pkg/authorization/authorization_integration_test.go` | Delete local helpers + OpenFGA store/model wiring, use `internal/testhelpers` |
| `pkg/authorization/grpc_handlers_test.go` | Delete local helpers, use `internal/testhelpers` |
| `pkg/authentication/authentication_integration_test.go` | Delete local helpers + Hydra/OAuth helpers, drop `suffix` plumbing |
| `pkg/authentication/authenticator_test.go` | Delete local Hydra/OAuth helpers, use `internal/testhelpers` |
| `internal/db/replica_integration_test.go` | Delete local helpers; start-failure semantics change `t.Fatalf` → same fail-hard behavior, now uniform |
| `internal/importer/importer_test.go` | Delete local helpers, use `internal/testhelpers` (scope expanded per review) |
| `AGENTS.md` | Document the integration-test prerequisites (Docker/Podman socket) and the `-short` unit-only convention |

### Dependencies

No new dependencies. `testcontainers-go`, `goose`, `pgx`, and the Hydra client SDK are already in `go.mod` (added by #286).

### Sequencing

This work is a follow-up to [#286](https://github.com/canonical/hook-service/pull/286) (branch `refactor/e2e`, open). Implementation branches from `refactor/e2e` and rebases onto `main` once #286 merges — no content changes required either way, since every migrated line lives in files #286 introduced.

### CI Impact

None structurally: `unittest.yaml` already installs Podman and exposes a rootless socket before `make test`, so fail-fast semantics hold in CI. Local developer docs must state that integration tests require a Docker/Podman socket and that `go test -short ./...` runs unit tests only.

### Performance Regressions

- **Risk**: Phase 1 keeps per-test containers — same cost as today. Phase 2 *reduces* runtime via shared containers.
- **Risk**: `t.Cleanup`-based teardown must survive `t.Parallel()` — handled by testcontainers' own termination guarantees plus unique generated container names (see design D2 and Review Amendments).
