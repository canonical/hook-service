## Context

The current E2E testing framework is defined in `tests/e2e/e2e_test.go` and configured by `tests/e2e/setup_test.go`. It requires spinning up a static Docker Compose stack and running the compiled hook-service application in a subprocess. This design migrates these tests to package-specific, programmatic integration tests utilizing the `testcontainers-go` library and `httptest.NewServer`.

## Goals / Non-Goals

**Goals:**
- Port all existing E2E test cases to package-specific integration test files:
  - Group CRUD and User Membership tests to `pkg/groups`.
  - App Authorization tests to `pkg/authorization`.
  - JWT Authentication tests to `pkg/authentication`.
- Use the `testcontainers-go` SDK to dynamically spawn ephemeral dependencies (PostgreSQL, OpenFGA, Ory Hydra).
- Use `httptest.NewServer` with the application's actual HTTP router ([pkg/web/router.go](file:///home/shipperizer/shipperizer/hook-service/pkg/web/router.go)) to run integration tests in-process.
- Clean up legacy `tests/e2e` files and setup scripts.

**Non-Goals:**
- Adding new feature functionality or API endpoints.
- Replacing existing unit tests that use mocks.

## Decisions

### 1. In-Process HTTP Server
We will use `httptest.NewServer` wrapping the `chi` HTTP router rather than compiling a binary and running it as a background process.
- *Rationale*: Avoids compilation time overhead, simplifies log aggregation, and makes test execution much faster.
- *Alternatives*: Keep running the binary in a subprocess. This was rejected because it is harder to debug and control process lifecycles.

### 2. Package-Scoped Container Helpers
Define local container initialization helpers directly within the package test files.
- *Rationale*: Different packages require different combinations of containers (e.g., `pkg/groups` needs only Postgres, while `pkg/authentication` needs Postgres and Hydra). Programmatically starting only what is needed per package reduces total test suite runtime.

## Risks / Trade-offs

- **Risk**: Test execution time might increase if container startup is not optimized.
  → *Mitigation*: Enable reuse of containers where possible and only start necessary containers for each package (e.g. `pkg/groups` doesn't boot Hydra or OpenFGA).
- **Risk**: Sandboxed CI environments without a Docker daemon will skip or fail these integration tests.
  → *Mitigation*: Wrap container instantiation in a safety check (`t.Skipf` or check docker availability) to gracefully skip integration tests when Docker is unavailable.
