## Why

The current end-to-end (E2E) testing suite runs via external shell commands and static Docker Compose networking, which introduces developer friction, prevents concurrent execution due to port conflicts, and risks resource leaks. Migrating to programmatically orchestrated, containerized integration tests using `testcontainers-go` simplifies test execution and improves resource lifecycle management.

## What Changes

- Refactor external shell-based E2E tests from `tests/e2e` into programmatic integration tests within standard Go test suites.
- Spin up ephemeral container instances of PostgreSQL, OpenFGA, and Ory Hydra dynamically using the `testcontainers-go` SDK.
- Boot the HTTP server in-process via `httptest.NewServer` in each test suite, injecting the required database, OpenFGA, and JWT clients.
- Remove the legacy `tests/e2e` directory and its associated setup scripts.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None.

## Non-goals

- This change does not add new API endpoints or modify any existing business logic or HTTP handlers.
- This change does not migrate low-level unit mock tests to integration tests.

## Impact

- **Affected Packages**: `pkg/groups`, `pkg/authorization`, and `pkg/authentication` will receive new integration test files.
- **APIs**: No API contract changes.
- **Dependencies**: Adds `github.com/testcontainers/testcontainers-go` and its modules to `go.mod` (already partly present).
- **CI/CD**: Simplifies workflow by allowing `go test ./...` to execute all integration tests programmatically without separate build steps.
