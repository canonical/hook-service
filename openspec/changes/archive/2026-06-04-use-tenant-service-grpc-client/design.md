## Context

`internal/tenants` currently contains a hand-written HTTP client that calls
`GET /api/v0/tenants/lookup` on the tenant service. This client manages its own
HTTP connection lifecycle, JSON decoding, error mapping, and OTel tracing via
`otelhttp`. The tenant service now exposes a gRPC API through
`github.com/canonical/identity-platform-api` (branch `IAM-1998`), which provides
a strongly-typed `TenantServiceClient` with a `LookupTenants` RPC. The existing
`identity-platform-api` dependency is already in `go.mod`.

## Goals / Non-Goals

**Goals:**
- Replace the bespoke HTTP client in `internal/tenants` with a gRPC client backed
  by `tenant.TenantServiceClient.LookupTenants`.
- Remove the local `tenant` and `lookupResponse` struct types that existed only to
  decode JSON.
- Keep `TenantValidatorInterface` unchanged so all callers remain unaffected.
- Update `cmd/serve.go` to construct a `grpc.ClientConn` for the tenant service.
- Update `internal/config/specs.go` to replace the HTTP-specific `TENANT_SERVICE_URL`
  / timeout config with a gRPC target address.

**Non-Goals:**
- Changing the observable behavior of `ValidateMembership`.
- Adding new configuration options beyond a gRPC address.
- Pooling or interceptor chaining beyond what is needed for basic OTel tracing.

## Decisions

### 1. Accept `tenant.TenantServiceClient` directly in `Client`

**Decision:** Store a `tenant.TenantServiceClient` (the generated interface) rather
than a raw `grpc.ClientConn` in `internal/tenants.Client`.

**Rationale:** Follows the project pattern used in `internal/openfga` where the
generated client interface is stored directly, making the dependency explicit and
mockable. The connection itself is owned by `cmd/serve.go` and shared across
clients.

**Alternatives considered:**
- Store `*grpc.ClientConn` and call `tenant.NewTenantServiceClient` inside
  `Client` — rejected because it makes the constructor harder to test and couples
  the client to connection lifecycle.

### 2. Use `TenantServiceClient` via a local interface in `internal/tenants`

**Decision:** Define a narrow `TenantServiceClientInterface` in
`internal/tenants/interfaces.go` that only exposes `LookupTenants`. The mock is
generated from this interface.

**Rationale:** Avoids importing the full generated mock from the external package
and keeps the mock minimal (only the method under test). Consistent with the
project's interface-per-package convention.

### 3. OTel tracing stays on the `tracer` interface, not transport-level

**Decision:** Remove `otelhttp` from `internal/tenants`; keep the
`tracer.Start`/`span.End` pattern.

**Rationale:** gRPC already supports OTel via `go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc`
interceptors wired at connection creation time in `cmd/serve.go`. The existing
`tracer.Start` pattern handles the span at the `ValidateMembership` method boundary,
which is the level the service cares about.

### 4. Configuration: replace `TENANT_SERVICE_URL` with `TENANT_SERVICE_GRPC_ADDRESS`

**Decision:** Replace the existing HTTP URL env var with a gRPC target address
(e.g., `tenant-service.default.svc.cluster.local:443`).

**Rationale:** gRPC targets don't use URL format. The timeout field is dropped
because gRPC deadlines are propagated via the context from callers.

## Risks / Trade-offs

- **Dependency on unreleased branch** → Pinned to a specific commit on `IAM-1998`
  until it merges to `main`; a `replace` directive in `go.mod` is not needed
  because the branch commit is addressable via pseudo-version.
  Mitigation: the task list includes updating the dependency to `main` once merged.

- **gRPC connection failure mode differs from HTTP** → Connection errors surface
  as gRPC status codes rather than HTTP status codes.
  Mitigation: map gRPC errors to the existing error contract in `ValidateMembership`
  (`ErrNotMember` on `codes.NotFound`, error on all other non-OK codes).

- **No per-call timeout on gRPC** → The HTTP client used `context.WithTimeout`.
  With gRPC the caller is expected to pass a context with deadline already set, or
  we add `context.WithTimeout` inside `ValidateMembership`.
  Mitigation: keep `context.WithTimeout` inside `ValidateMembership` using a
  configurable timeout field on `Client` (same as today).
