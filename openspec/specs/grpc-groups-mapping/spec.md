## Purpose

Provide a native, high-performance, and memory-efficient gRPC server-streaming API for internal services to query user-to-group and group-to-user mappings.

This capability exists because internal services need access to group memberships for authorization decisions, but the existing HTTP/gRPC-gateway API requires fully materializing response arrays. For large tenants, this risks exceeding gRPC message size limits and causing memory spikes. A native gRPC server with streaming RPCs and callback-based storage iteration enforces O(1) memory usage per request.

Key decisions:
- Introduce a dedicated gRPC server running concurrently on its own port, sharing state and using graceful shutdown.
- Query results are streamed using a callback-based storage implementation to avoid OOM errors.
- Streaming operations are bound by context timeouts, and JWT validation is enforced on all streams using an interceptor.

Non-goals:
- This spec does not cover HTTP-gateway mapping streaming endpoints.
- This spec does not define group lifecycle management (creation or deletion).

## Requirements

### Requirement: Native gRPC server with dual-server lifecycle

The system SHALL run a native `grpc.NewServer()` concurrently alongside the existing HTTP server on a dedicated `GRPC_PORT` (default 9090). Both servers SHALL share the same `Storage`, `Authorizer`, and `JWTVerifier` instances.

#### Scenario: gRPC server starts on configured port
- **WHEN** the service starts
- **AND** `GRPC_PORT` is configured (default 9090)
- **THEN** a native gRPC server SHALL listen on that port
- **AND** the HTTP server SHALL continue listening on its configured `PORT` (default 8080)

#### Scenario: Dual-server graceful shutdown
- **WHEN** the process receives `SIGINT` or `SIGTERM`
- **THEN** `grpc.Server.GracefulStop()` SHALL be called to drain in-flight RPCs
- **AND** `http.Server.Shutdown(ctx)` SHALL be called with a 15-second deadline
- **AND** both shutdown operations SHALL run in parallel via `errgroup`

#### Scenario: Server startup failure propagates error
- **WHEN** the gRPC server fails to listen on the configured port
- **THEN** the error SHALL be returned from the serve command
- **AND** the HTTP server SHALL NOT start

### Requirement: GroupsMappingService with server-streaming RPCs

The system SHALL expose a `GroupsMappingService` gRPC service defined in `proto/hook/groups/v1/mapping.proto` with two server-streaming RPCs:
- `GetGroupsForUser(GetGroupsForUserReq) returns (stream GroupMapping)` — streams all groups a user belongs to
- `GetUsersInGroup(GetUsersInGroupReq) returns (stream UserMapping)` — streams all users in a group

#### Scenario: Stream groups for a user
- **WHEN** a client calls `GetGroupsForUser` with a valid `user_id`
- **THEN** the server SHALL stream `GroupMapping` messages containing group ID, name, tenant ID, description, type, and timestamps
- **AND** results SHALL be ordered by group name ascending

#### Scenario: Stream users in a group
- **WHEN** a client calls `GetUsersInGroup` with a valid `group_id`
- **THEN** the server SHALL stream `UserMapping` messages containing user IDs
- **AND** results SHALL be ordered by user ID ascending

#### Scenario: Empty result set completes stream normally
- **WHEN** a client calls either streaming RPC with valid parameters
- **AND** no results match the query
- **THEN** the stream SHALL close with `io.EOF` without error

### Requirement: Tenant-scoped filtering

Both RPC request messages SHALL include an optional `tenant_id` field. When provided, queries SHALL filter results to the specified tenant. The `tenant_id` column already exists in the `groups` and `group_members` tables and SHALL be used as a WHERE clause filter.

#### Scenario: Tenant filter restricts results
- **WHEN** a client calls `GetGroupsForUser` with a specific `tenant_id`
- **THEN** only groups belonging to that tenant SHALL be returned
- **AND** groups belonging to other tenants SHALL be excluded

#### Scenario: Empty tenant_id returns all results
- **WHEN** a client calls either streaming RPC without setting `tenant_id`
- **THEN** results SHALL NOT be filtered by tenant

### Requirement: Callback-based streaming storage layer

The storage layer SHALL stream database results via callback functions instead of materializing full slices. Storage methods SHALL accept a callback of type `func(*types.Group) error` or `func(string) error` and invoke it for each row. The stream SHALL stop on the first error returned by the callback, including context cancellation.

#### Scenario: Callback invoked per database row
- **WHEN** `StreamGroupsForUser` is called with a callback
- **THEN** the callback SHALL be invoked once for each matching database row
- **AND** the `sql.Rows` cursor SHALL be closed after iteration

#### Scenario: Callback error stops iteration
- **WHEN** the callback returns an error
- **THEN** the storage method SHALL stop iterating immediately
- **AND** the error SHALL be returned to the caller

#### Scenario: Client disconnect stops streaming
- **WHEN** a gRPC client disconnects during a stream
- **THEN** `stream.Send()` SHALL fail in the callback
- **AND** the callback error SHALL propagate up to stop the `sql.Rows` iteration
- **AND** the database cursor SHALL be closed

### Requirement: Context timeout enforcement on streaming queries

Every storage method in the streaming path SHALL wrap its database query in `context.WithTimeout` to prevent unbounded database connection usage.

#### Scenario: Streaming query has bounded execution time
- **WHEN** a streaming storage method is called
- **THEN** a `context.WithTimeout(ctx, 30s)` SHALL be applied before the database query
- **AND** the timeout SHALL cancel the query if it exceeds 30 seconds

### Requirement: gRPC stream JWT authentication interceptor

The native gRPC server SHALL be secured by a `grpc.StreamServerInterceptor` that validates Bearer tokens on every stream. The interceptor SHALL extract the token from gRPC metadata, call the existing `JWTVerifier.VerifyToken()`, and return `UNAUTHENTICATED` on failure.

#### Scenario: Authenticated stream passes through
- **WHEN** a gRPC call includes a valid `authorization: Bearer <token>` metadata header
- **AND** the token is verified by `JWTVerifier.VerifyToken()`
- **THEN** the stream handler SHALL be invoked with the original context

#### Scenario: Missing metadata returns UNAUTHENTICATED
- **WHEN** a gRPC call has no metadata
- **THEN** the interceptor SHALL return `codes.Unauthenticated` with "missing metadata"

#### Scenario: Missing authorization header returns UNAUTHENTICATED
- **WHEN** a gRPC call has metadata without an `authorization` key
- **THEN** the interceptor SHALL return `codes.Unauthenticated` with "missing authorization header"

#### Scenario: Invalid authorization format returns UNAUTHENTICATED
- **WHEN** a gRPC call has an `authorization` header not prefixed with "Bearer "
- **THEN** the interceptor SHALL return `codes.Unauthenticated` with "invalid authorization format"

#### Scenario: Invalid token returns UNAUTHENTICATED
- **WHEN** a gRPC call has a Bearer token that fails `JWTVerifier.VerifyToken()`
- **THEN** the interceptor SHALL return `codes.Unauthenticated` with "invalid token"

#### Scenario: Token verification returns unauthorized
- **WHEN** a gRPC call has a validly-formatted Bearer token
- **AND** `JWTVerifier.VerifyToken()` returns `false` without error
- **THEN** the interceptor SHALL return `codes.Unauthenticated` with "unauthorized"

### Requirement: Domain error to gRPC status code mapping

The gRPC handler layer SHALL map domain sentinel errors to standard gRPC status codes via a `mapMappingErrorToStatus()` function.

| Domain Error | gRPC Status Code | Message |
|---|---|---|
| `ErrInvalidTenant` | `INVALID_ARGUMENT` | "invalid tenant" |
| `ErrStreamInterrupted` | `INTERNAL` | "stream interrupted" with cause |
| `ErrUnauthorizedStream` | `UNAUTHENTICATED` | "unauthorized" |
| `storage.ErrNotFound` | `NOT_FOUND` | "not found" |
| Any other error | `INTERNAL` | "<action> failed" |

#### Scenario: Domain error mapped to correct gRPC code
- **WHEN** the streaming handler encounters a domain error
- **THEN** the response SHALL use the gRPC status code specified in the mapping table
- **AND** the OpenTelemetry span SHALL record the error and set status to Error

### Requirement: Protobuf definition and code generation

The system SHALL maintain a local Protobuf definition at `proto/hook/groups/v1/mapping.proto`. Generated Go code SHALL be committed to `gen/hook/groups/v1/`. The proto file SHALL use `package hook.groups.v1` with `go_package` set to `github.com/canonical/hook-service/gen/hook/groups/v1`.

#### Scenario: Proto file is self-contained
- **WHEN** the `.proto` file is compiled with `protoc` or `buf`
- **THEN** it SHALL generate `GroupsMappingServiceServer` and `GroupsMappingServiceClient` interfaces
- **AND** it SHALL import only `google/protobuf/timestamp.proto`

#### Scenario: Generated code is committed
- **WHEN** proto compilation produces Go files
- **THEN** the generated files SHALL be committed to the repository
- **AND** CI SHALL NOT require `protoc` or `buf` to build

### Requirement: OpenTelemetry tracing on streaming operations

All streaming operations across storage, service, handler, and interceptor layers SHALL be instrumented with OpenTelemetry spans. Each span SHALL record the relevant identifiers (user ID, group ID, tenant ID) as span attributes. Errors SHALL be recorded on the span and set the span status to Error.

#### Scenario: Span attributes set on GetGroupsForUser
- **WHEN** `GetGroupsForUser` is called
- **THEN** span attributes `user.id` and `tenant.id` SHALL be set

#### Scenario: Span attributes set on GetUsersInGroup
- **WHEN** `GetUsersInGroup` is called
- **THEN** span attributes `group.id` and `tenant.id` SHALL be set
