## 1. Domain Layer

- [x] 1.1 Define domain sentinel errors in `pkg/groups/errors.go`: `ErrInvalidTenant`, `ErrStreamInterrupted`, `ErrUnauthorizedStream`
- [x] 1.2 Create `proto/hook/groups/v1/mapping.proto` defining `GroupsMappingService` with `GetGroupsForUser` and `GetUsersInGroup` server-streaming RPCs, both request messages including optional `tenant_id` field
- [x] 1.3 Run proto compilation (`buf generate` or `protoc`) and commit generated Go files to `gen/hook/groups/v1/`

## 2. Usecase Layer

- [x] 2.1 Add streaming method signatures to `pkg/groups/interfaces.go`: `StreamGroupsForUser(ctx, tenantID, userID string, fn func(*types.Group) error) error` and `StreamUsersInGroup(ctx, tenantID, groupID string, fn func(string) error) error` on both `ServiceInterface` and `DatabaseInterface`
- [x] 2.2 Implement the streaming methods in `pkg/groups/service.go`, passing context and callback through to the database layer, mapping storage errors to domain errors

## 3. Infrastructure Layer

- [x] 3.1 Add streaming method signatures to `internal/storage/interfaces.go` `StorageInterface`: `StreamGroupsForUser(ctx, tenantID, userID string, fn func(*types.Group) error) error` and `StreamUsersInGroup(ctx, tenantID, groupID string, fn func(string) error) error`
- [x] 3.2 Implement `GetGroupsForUser` in `internal/storage/groups.go` with `tenant_id` filter, `context.WithTimeout`, and callback-based row streaming from `sql.Rows`
- [x] 3.3 Implement `ListUsersInGroup` in `internal/storage/groups.go` with `tenant_id` filter, `context.WithTimeout`, and callback-based row streaming from `sql.Rows`
- [x] 3.4 Update existing materializing callers of `GetGroupsForUser` and `ListUsersInGroup` in `pkg/groups/service.go` to use the new streaming signatures (materializing results where needed for HTTP handlers)

## 4. Delivery Layer

- [x] 4.1 Create `pkg/groups/mapping_grpc_handlers.go` implementing the generated `GroupsMappingServiceServer` interface, piping the storage callback to `stream.Send()`
- [x] 4.2 Implement `mapMappingErrorToStatus()` for error-to-gRPC-status mapping: `ErrInvalidTenant` → `INVALID_ARGUMENT`, `ErrStreamInterrupted` → `INTERNAL`, `ErrUnauthorizedStream` → `UNAUTHENTICATED`, `storage.ErrNotFound` → `NOT_FOUND`
- [x] 4.3 Create `pkg/authentication/grpc_interceptor.go` implementing a `grpc.StreamServerInterceptor` that extracts Bearer token from metadata, calls `JWTVerifier.VerifyToken()`, and returns `UNAUTHENTICATED` on failure
- [x] 4.4 Add `GRPCPort` field to `config.EnvSpec` with default `9090`
- [x] 4.5 Update `cmd/serve.go` to create `grpc.NewServer()` with the stream interceptor, register `GroupsMappingServiceServer`, listen on `GRPC_PORT`, and launch both HTTP and gRPC servers concurrently
- [x] 4.6 Implement dual-server graceful shutdown: on `SIGINT`/`SIGTERM`, call `grpc.Server.GracefulStop()` and `http.Server.Shutdown(ctx)` within a 15s deadline, using `errgroup.Group` to collect errors

## 5. Tests

- [x] 5.1 Add unit tests for `mapping_grpc_handlers.go`: stream groups for user with valid tenant, stream users in group with valid tenant, cross-tenant isolation returns zero results, context cancellation halts stream
- [x] 5.2 Add unit tests for `grpc_interceptor.go`: missing metadata → `UNAUTHENTICATED`, invalid token → `UNAUTHENTICATED`, valid token → passes through
- [x] 5.3 Add unit tests for `mapMappingErrorToStatus()`: each domain error maps to the correct gRPC status code
- [x] 5.4 Add integration tests using testcontainers-go: spin up PostgreSQL + Ory Hydra, register gRPC server, test `GetGroupsForUser` and `GetUsersInGroup` end-to-end with tenant filtering
- [x] 5.5 Add integration test for JWT interceptor: unauthenticated stream is rejected, authenticated stream succeeds
- [x] 5.6 Run `go build ./...`, `go vet ./...`, `golangci-lint run`, and `go test -race ./...` — zero failures
