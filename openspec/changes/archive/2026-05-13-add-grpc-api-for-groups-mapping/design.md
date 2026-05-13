## Context

The hook-service currently exposes groups and authorization APIs exclusively through gRPC-gateway — the `GrpcServer` structs in `pkg/groups` and `pkg/authentication` implement protobuf service interfaces but are used purely as in-process HTTP handlers via `runtime.NewServeMux`. There is no native gRPC listener. All list/query methods in `internal/storage/groups.go` fully materialize results into slices (`[]*types.Group`, `[]string`), which risks OOM and gRPC message-size violations for large tenants. The `pkg/authentication` package provides only an HTTP middleware for JWT validation — no gRPC interceptors exist.

The existing `v0_groups.AuthzGroupsServiceServer` (from `github.com/canonical/identity-platform-api`) uses unary patterns only. This change introduces a new local proto (`GroupsMappingService`) with server-streaming RPCs, running on a dedicated native gRPC port.

## Goals / Non-Goals

**Goals:**
- Spin up a native gRPC server on a dedicated `GRPC_PORT` alongside the existing HTTP server
- Define and stage local Protobuf definitions for `GroupsMappingService` with server-streaming RPCs
- Support optional tenant filtering via `tenant_id` in request messages
- Secure the native gRPC API with a `StreamServerInterceptor` that validates JWTs
- Stream `sql.Rows` results via callbacks from storage to gRPC, maintaining O(1) memory per request
- Enforce `context.WithTimeout` on every database query in the streaming path
- Unconditional adherence to `testcontainers`, `go vet`, and `go test -race` CI protocols
- Graceful shutdown of both HTTP and gRPC servers on OS signals

**Non-Goals:**
- Exposing the native gRPC API to external clients — strictly for internal service-to-service use
- Modifying the existing gRPC-gateway HTTP endpoints or `v0_groups`/`v0_authz` service contracts
- Client-side streaming or bidirectional streaming RPCs

## Decisions

### 1. Dual-server architecture: native gRPC + gRPC-gateway HTTP

**Decision**: Run `grpc.NewServer()` on `GRPC_PORT` concurrently with the existing `http.Server`. Both servers share the same `Storage`, `Authorizer`, and `JWTVerifier` instances. The `cmd/serve.go` entry point launches both listeners and orchestrates graceful shutdown.

**Alternatives considered**:
- *Single server with gRPC and HTTP on the same port*: Requires `cmux` or similar multiplexing. Adds complexity and obscures the separation between internal and external traffic.
- *Replace gRPC-gateway entirely*: Breaking change for existing HTTP consumers. Not feasible.

**Rationale**: A dedicated gRPC port cleanly separates internal (native gRPC) from external (HTTP/gRPC-gateway) traffic. This aligns with Kubernetes service mesh patterns where internal services communicate over gRPC while external clients use REST.

### 2. Local Protobuf definitions at `proto/hook/groups/v1/mapping.proto`

**Decision**: Stage a new `.proto` file locally rather than adding to the `identity-platform-api` repository. This allows rapid iteration before the contract is promoted.

**New RPCs**:
```protobuf
service GroupsMappingService {
  rpc GetGroupsForUser(GetGroupsForUserReq) returns (stream Group);
  rpc GetUsersInGroup(GetUsersInGroupReq) returns (stream User);
}
```

Both request messages include an optional `string tenant_id = 2`.

**Alternatives considered**:
- *Add to `identity-platform-api`*: Slower iteration cycle; requires cross-repo coordination.
- *Use existing `v0_groups` service*: The existing service is unary and externally-facing; mixing streaming and unary concerns would violate separation.

### 3. Callback-based streaming in storage layer

**Decision**: Refactor `GetGroupsForUser` and `ListUsersInGroup` to accept a callback function instead of returning materialized slices:

```go
func (s *Storage) GetGroupsForUser(ctx context.Context, userID, tenantID string, fn func(*types.Group) error) error
func (s *Storage) ListUsersInGroup(ctx context.Context, groupID, tenantID string, fn func(string) error) error
```

The storage method iterates `sql.Rows`, calls `fn` for each row, and returns on the first error (including context cancellation). Each query is wrapped in `context.WithTimeout` before execution.

**Alternatives considered**:
- *Return a channel*: Caller must drain the channel; risk of goroutine leaks on context cancellation.
- *Return an iterator (Go 1.23 `iter.Seq`)*: Requires Go 1.23+ iterator patterns; callback is more explicit about error propagation and cancellation.
- *Keep materialized slices*: Defeats the purpose of streaming — memory is O(n).

**Rationale**: Callbacks give the caller full control over flow: the gRPC handler calls `stream.Send(msg)` inside the callback. If the client disconnects, `stream.Send` fails, the callback returns an error, the storage loop stops, and the cursor is closed. No goroutine leaks.

### 4. gRPC Stream JWT Interceptor

**Decision**: Implement a `grpc.StreamServerInterceptor` in `pkg/authentication` that extracts the Bearer token from the gRPC metadata, calls `JWTVerifier.VerifyToken()`, and returns `UNAUTHENTICATED` on failure.

**Alternatives considered**:
- *Mutual TLS*: Requires certificate management for every internal client. Overhead not justified for this use case.
- *No authentication*: Unacceptable for internal services handling group memberships.

**Rationale**: Reuses the existing `JWTVerifier` infrastructure. The interceptor applies uniformly to all streams on the native gRPC server without per-handler boilerplate.

### 5. Error mapping for gRPC status codes

**Decision**: Define domain sentinel errors (`ErrInvalidTenant`, `ErrStreamInterrupted`, `ErrUnauthorizedStream`) and map them to gRPC status codes in the handler layer:

| Error | gRPC Code |
|-------|-----------|
| `ErrInvalidTenant` | `INVALID_ARGUMENT` |
| `ErrStreamInterrupted` | `INTERNAL` (with cause detail) |
| `ErrUnauthorizedStream` | `UNAUTHENTICATED` |
| `storage.ErrNotFound` | `NOT_FOUND` |

**Rationale**: Consistent error semantics across the API surface. Callers can distinguish validation errors from infrastructure failures.

### 6. Graceful shutdown for dual-server

**Decision**: Both HTTP and gRPC servers listen for OS signals (`SIGINT`/`SIGTERM`). On signal:
1. `grpc.Server.GracefulStop()` — allows in-flight RPCs to complete
2. `http.Server.Shutdown(ctx)` with 15s deadline — existing behavior
3. Both wrapped in a `errgroup.Group` so shutdown errors are collected

**Rationale**: Kubernetes sends `SIGTERM` and waits for the pod to terminate. Both servers must drain in-flight requests within the termination grace period.

## Risks / Trade-offs

- **Memory spike during stream setup**: Opening a `sql.Rows` cursor for a large tenant holds a database connection until the stream completes. → *Mitigation*: `context.WithTimeout` on every query bounds the maximum stream duration. Connection pool sizing should account for concurrent streams.

- **Client disconnect not detected immediately**: gRPC may not detect a client disconnect until the next `stream.Send()` fails. → *Mitigation*: The callback pattern naturally checks for errors on every send. A stuck client will timeout via `context.WithTimeout`.

- **Proto compilation in CI**: Adding local `.proto` files requires `protoc` or `buf` in the build environment. → *Mitigation*: Generated Go code should be committed to the repository so CI does not depend on proto toolchain. Proto compilation is a local developer step.

- **Breaking change to `StorageInterface`**: Adding `tenantID` and callback signatures to `GetGroupsForUser` and `ListUsersInGroup` changes the interface. → *Mitigation*: All existing callers (service layer, HTTP handlers) must be updated. The callback-based methods are additive — the existing materializing methods can be preserved for HTTP handler use if needed, or refactored to use the callback internally.
