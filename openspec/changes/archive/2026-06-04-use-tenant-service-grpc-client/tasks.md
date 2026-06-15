## 1. Update dependency

- [x] 1.1 In `go.mod`, update `github.com/canonical/identity-platform-api` to the commit on branch `IAM-1998` that includes the tenant gRPC package (use `go get github.com/canonical/identity-platform-api@<commit-sha>` or the pseudo-version). Run `go mod tidy`.

## 2. Narrow gRPC interface in `internal/tenants`

- [x] 2.1 In `internal/tenants/interfaces.go`, add a new `TenantServiceClientInterface` that exposes only `LookupTenants(ctx context.Context, in *tenant.LookupTenantsRequest, opts ...grpc.CallOption) (*tenant.LookupTenantsResponse, error)`.
- [x] 2.2 Add `//go:generate mockgen` directive in `internal/tenants/client_test.go` to generate `mock_tenant_service_client.go` from `TenantServiceClientInterface`. Run `go generate ./internal/tenants/...`.

## 3. Rewrite `internal/tenants/client.go`

- [x] 3.1 Remove the local `tenant`, `lookupResponse` struct types and the `otelHTTPClient` package-level variable from `internal/tenants/client.go`.
- [x] 3.2 Replace the `httpClient *http.Client` and `baseURL string` fields in `Client` with a `grpcClient TenantServiceClientInterface` field.
- [x] 3.3 Update `NewClient` to accept a `TenantServiceClientInterface` (instead of `baseURL string` and `httpClient`). Keep `timeout time.Duration`, `tracer`, `monitor`, `logger` parameters.
- [x] 3.4 Rewrite `ValidateMembership` to call `grpcClient.LookupTenants` with a `LookupTenantsRequest{IdentityId: identityID}`, then iterate the response tenants and return `nil`/`ErrNotMember` as before. Return a wrapped error (not `ErrNotMember`) on gRPC failure.
- [x] 3.5 Remove the `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` import from `internal/tenants/client.go`.

## 4. Update configuration

- [x] 4.1 In `internal/config/specs.go`, remove `TenantServiceURL string` and `TenantServiceTimeout time.Duration` entirely and add `TenantServiceAddress string` (envconfig tag `tenant_service_address`, default `""`). No backwards compatibility with the old env var is required.
- [x] 4.2 Update the comment in `internal/tenants/noop.go` to reference `TENANT_SERVICE_GRPC_ADDRESS` instead of `TENANT_SERVICE_URL`.

## 5. Update `cmd/serve.go` wiring

- [x] 5.1 In `cmd/serve.go`, replace the `specs.TenantServiceURL` check with `specs.TenantServiceAddress`. When set, dial the gRPC address with `grpc.Dial` (or `grpc.NewClient`) and create the tenant gRPC client using `tenantpb.NewTenantServiceClient(conn)`. Pass the result to `tenants.NewClient`.
- [x] 5.2 Close the `grpc.ClientConn` on shutdown (defer or via cleanup registered with the server lifecycle).

## 6. Update tests in `internal/tenants`

- [x] 6.1 Rewrite `TestClientValidateMembership` in `internal/tenants/client_test.go` to use the generated `MockTenantServiceClientInterface` instead of `httptest.Server`. Cover: membership valid, membership denied, gRPC error.
- [x] 6.2 Remove imports no longer needed: `encoding/json`, `net/http`, `net/http/httptest`, `strings`.

## 7. Update ADR documentation

- [x] 7.1 Update `docs/adr/002-tenant-service-integration.md` to reflect the new `TENANT_SERVICE_GRPC_ADDRESS` config variable (replacing `TENANT_SERVICE_URL`), and note that the transport is now gRPC.
