## Why

Internal services need access to user-to-group mappings to make authorization decisions. The existing API is HTTP-only via gRPC-gateway, which carries HTTP translation overhead and returns fully materialized response arrays. For tenants with thousands of users in a group, these responses risk exceeding the 4 MB gRPC message size limit and cause memory spikes. A native gRPC server with server-streaming RPCs provides a strongly-typed, low-latency interface with bounded memory usage for internal consumers.

## What Changes

- Stage a new local Protobuf definition (`proto/hook/groups/v1/mapping.proto`) defining a `GroupsMappingService` with two server-streaming RPCs: `GetGroupsForUser` and `GetUsersInGroup`
- Both request messages include an optional `tenant_id` field for tenant-scoped filtering (the `tenant_id` column already exists in `groups` and `group_members` tables)
- Add a dedicated native gRPC server (`grpc.NewServer()`) running concurrently on a separate `GRPC_PORT` alongside the existing HTTP server
- Refactor `internal/storage/groups.go` — `GetGroupsForUser` and `ListUsersInGroup` must accept `tenant_id` and stream `sql.Rows` via a callback pattern (`func(types.Group) error` or `func(string) error`) instead of materializing full slices, with explicit `context.WithTimeout` per statement
- Add a `grpc.StreamServerInterceptor` in `pkg/authentication` that validates JWTs on native gRPC streams, reusing the existing `JWTVerifier` logic
- Create `pkg/groups/mapping_grpc_handlers.go` implementing the generated `GroupsMappingServiceServer` interface, piping the storage stream to the gRPC stream
- Add `GRPC_PORT` to `config.EnvSpec` and wire the dual-server lifecycle in `cmd/serve.go` with graceful shutdown on both listeners
- Define domain sentinel errors: `ErrInvalidTenant`, `ErrStreamInterrupted`, `ErrUnauthorizedStream`

## Capabilities

### New Capabilities
- `grpc-groups-mapping`: Native gRPC server-streaming API exposing user-to-group and group-to-user mappings for internal services, with tenant-scoped filtering and JWT-secured stream interceptor

### Modified Capabilities

## Impact

### Architectural Impact (internal/domain vs internal/infrastructure)

- **`cmd/serve.go` (Application entry point)**: Launch a `grpc.NewServer()` concurrently alongside the existing HTTP server. Both servers must be injected through explicit constructors — no `init()` functions or global state.
- **`pkg/groups/mapping_grpc_handlers.go` (Delivery)**: New handler implementing `GroupsMappingServiceServer`, cleanly decoupled from the existing gateway handlers in `grpc_handlers.go`.
- **`pkg/groups/interfaces.go` (Business logic)**: `ServiceInterface` and `DatabaseInterface` updated with tenant-aware streaming signatures (e.g., `func(ctx, tenantID, userID string, fn func(*types.Group) error) error`).
- **`internal/storage/groups.go` (Infrastructure)**: `GetGroupsForUser` and `ListUsersInGroup` refactored to accept `tenant_id` and stream results via callbacks with explicit `context.WithTimeout` deadlines.
- **`pkg/authentication` (Delivery / Middleware)**: New `grpc.StreamServerInterceptor` reusing `JWTVerifier` for native gRPC stream security.
- **`internal/config/specs.go` (Infrastructure)**: Add `GRPC_PORT` env var.

### API Contract Changes

**New contract:** `proto/hook/groups/v1/mapping.proto` defines `GroupsMappingService` — a net-new gRPC service for internal use only. No changes to existing `v0_groups` or `v0_authz` HTTP/gRPC-gateway contracts.

### Schema Migrations

**None.** The `tenant_id` column already exists in `groups` and `group_members` tables. The changes adjust query logic to filter on this existing column.

### Performance Regressions

- **Risk**: Unbounded materialized slices in `GetGroupsForUser` / `ListUsersInGroup` can cause OOM for large tenants.
  → *Mitigation*: Server-streaming RPCs push results one message at a time; the storage layer streams `sql.Rows` via callbacks, maintaining O(1) memory per request.
- **Risk**: Long-running streams could hold database connections indefinitely.
  → *Mitigation*: Every storage method wraps its query in `context.WithTimeout`. gRPC context cancellation is propagated — client disconnect aborts the cursor immediately.
- **Risk**: Missing JWT on the native gRPC port exposes internal data.
  → *Mitigation*: A `StreamServerInterceptor` validates Bearer tokens on every stream before the handler runs.

### Affected Code

| File | Change |
|------|--------|
| `internal/storage/groups.go` | Refactor `GetGroupsForUser` and `ListUsersInGroup` to accept `tenant_id`, add `context.WithTimeout`, stream via callbacks |
| `internal/storage/interfaces.go` | Update `StorageInterface` method signatures |
| `pkg/groups/interfaces.go` | Update `ServiceInterface` and `DatabaseInterface` with tenant-aware streaming signatures |
| `pkg/groups/service.go` | Adapt service methods to new streaming signatures |
| `pkg/groups/mapping_grpc_handlers.go` | New — implement `GroupsMappingServiceServer` |
| `pkg/authentication/middleware.go` | New `StreamServerInterceptor` for gRPC JWT validation |
| `internal/config/specs.go` | Add `GRPC_PORT` |
| `cmd/serve.go` | Dual-server lifecycle: native gRPC + HTTP, shared graceful shutdown |
| `proto/hook/groups/v1/mapping.proto` | New — Protobuf service definition |

### Dependencies

No new Go dependencies beyond what is already vendored (gRPC, protobuf, `pgx/v5`). Proto compilation requires `protoc` or `buf` at build time.
