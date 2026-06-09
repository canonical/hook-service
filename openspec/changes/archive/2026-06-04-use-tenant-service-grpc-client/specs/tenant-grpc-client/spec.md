## ADDED Requirements

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

## REMOVED Requirements

### Requirement: Tenant membership validated via HTTP REST
**Reason**: Replaced by gRPC-based validation (see above). The HTTP client, local
`tenant`/`lookupResponse` types, and `otelhttp` dependency in `internal/tenants`
are removed.
**Migration**: No external migration needed; this is an internal transport change.
The `TenantValidatorInterface` is unchanged.
