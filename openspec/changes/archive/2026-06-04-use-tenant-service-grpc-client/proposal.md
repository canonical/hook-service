## Why

The tenant-service integration in PR #225 uses a custom HTTP client to call the `/api/v0/tenants/lookup` REST endpoint. Now that the tenant-service exposes a gRPC API (via `github.com/canonical/identity-platform-api`), we should migrate to the generated gRPC client to eliminate bespoke HTTP plumbing, benefit from strongly-typed contracts, and align with the service's existing gRPC patterns.

## What Changes

- Add `github.com/canonical/identity-platform-api` at a version containing the tenant gRPC client (branch `IAM-1998`, to be pinned to `main` once merged).
- Replace `internal/tenants/client.go` (HTTP-based `Client`) with a new gRPC-backed `Client` that calls `TenantService/LookupTenants`.
- Remove the local HTTP type definitions (`tenant`, `lookupResponse`) that were only needed to decode the JSON response.
- Remove the `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` dependency from `internal/tenants` (no longer needed there; otel tracing is still done via the tracer interface).
- Update `internal/tenants/noop.go` (no changes needed, already implements the interface).
- Update `cmd/serve.go` wiring to pass a `grpc.ClientConn` (or equivalent) instead of the HTTP base URL and timeout.

## Capabilities

### New Capabilities

None — this is a pure internal refactoring with no externally visible behavior change.

### Modified Capabilities

None — `TenantValidatorInterface.ValidateMembership` contract is unchanged.

## Non-goals

- Exposing any new gRPC endpoints from hook-service itself.
- Changing the `TenantValidatorInterface` signature.
- Migrating other HTTP clients in the codebase.

## Impact

- **internal/tenants**: `client.go` rewritten; local HTTP types removed. Mock regenerated.
- **cmd/serve.go**: wiring updated to construct `grpc.ClientConn` and pass it to `tenants.NewClient`.
- **go.mod / go.sum**: version of `github.com/canonical/identity-platform-api` updated to include tenant package.
- **internal/config/specs.go**: HTTP-specific config (base URL + timeout) replaced or supplemented with a gRPC target address config var.
