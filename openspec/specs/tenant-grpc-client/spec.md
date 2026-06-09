# tenant-grpc-client Specification

## Purpose

Hook-service is the **sole** Hydra `oauth2.token_hook` (Hydra allows only one), so it must enrich tokens with **both** `groups` (RBAC) and `tenant_id` (multi-tenancy). To validate the user's selected tenant, hook-service calls tenant-service during the token hook flow.

**Decision:** validate membership via the gRPC `TenantService.LookupTenants` RPC, keyed on `identity_id` (the Hydra session `subject`, always available) rather than `email` (only present when the `email` scope is requested). gRPC is preferred over the HTTP REST lookup for a typed contract and one fewer network hop (it skips the Kratos email→identity resolution).

**Goals:** opt-in via `TENANT_SERVICE_GRPC_ADDRESS`; full backward compatibility when unset; fail-closed on lookup/transport errors so tokens are never issued without tenant validation.

**Non-goals:** generic N-hook webhook chaining and forwarding the full `TokenHookRequest` to tenant-service were considered and rejected as over-engineered/coupling for the current two-service deployment. Using the authenticated `ListUserTenants` admin endpoint was rejected to avoid OAuth client/token management; the unauthenticated internal lookup is purpose-built for privileged in-cluster callers.

(Supersedes `docs/adr/002-tenant-service-integration.md`.)

## Requirements
### Requirement: Tenant membership validated via gRPC
The system SHALL validate tenant membership by calling `TenantService.LookupTenants`
via gRPC rather than via the HTTP REST endpoint.

#### Scenario: User is a member of the tenant
- **WHEN** `ValidateMembership` is called with a valid `identityID` and `tenantID`
- **THEN** the system calls `LookupTenants` with `identity_id` set
- **AND** iterates the returned `tenants`
- **AND** returns `nil` when a tenant with matching `id` is found

#### Scenario: User is not a member of the tenant
- **WHEN** `ValidateMembership` is called and none of the returned tenants match `tenantID`
- **THEN** the system returns `ErrNotMember`

#### Scenario: gRPC call fails
- **WHEN** `LookupTenants` returns a non-nil error
- **THEN** the system returns a wrapped error (not `ErrNotMember`)

#### Scenario: gRPC client is constructed from a connection
- **WHEN** `NewClient` is called with a `TenantServiceClientInterface`
- **THEN** the client stores the gRPC client internally and uses it in `ValidateMembership`

### Requirement: Tenant service address configurable via environment variable
The system SHALL read the tenant service gRPC target address from
`TENANT_SERVICE_GRPC_ADDRESS` (e.g., `host:port`).

#### Scenario: Address is set
- **WHEN** `TENANT_SERVICE_GRPC_ADDRESS` is set in the environment
- **THEN** the service dials that address to construct the gRPC connection

#### Scenario: Address is empty / service is disabled
- **WHEN** `TENANT_SERVICE_GRPC_ADDRESS` is not set
- **THEN** the service uses the noop tenant client (existing behaviour)

