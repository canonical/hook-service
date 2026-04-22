# ADR-002: Integrate Tenant Service for Multi-Tenant Token Enrichment

**Status**: Proposed
**Date**: 2026-04-21
**Deciders**: Development Team

## Context

The Canonical Identity Platform uses two distinct services connected to Hydra via the
token hook mechanism:

- **hook-service**: Enriches OAuth tokens with user group claims (`groups`) and performs
  per-client authorization via OpenFGA.
- **tenant-service**: Enriches OAuth tokens with the user's selected tenant
  (`tenant_id`) and validates active tenant membership.

Hydra supports only **one** `oauth2.token_hook` URL. This means both services cannot
independently register as Hydra token hooks in the same deployment. Today, operators must
choose one or the other.

In production, deployments need **both** features active simultaneously — users need group
claims for RBAC and tenant claims for multi-tenancy.

### Current Token Hook Behavior

**hook-service** (registered as Hydra's token hook):
1. Extracts user identity (`subject`, `email`, `client_id`) from the `TokenHookRequest`.
2. Fetches user groups from the local database (and optionally Salesforce via CLI import).
3. Checks OpenFGA authorization: is this user allowed to access this client?
4. Injects `groups` (string array) into `id_token` and `access_token`.

**tenant-service** (currently a separate token hook):
1. Extracts `user_id` from `req.Session.Subject`.
2. Extracts `tenant_id` from `req.Session.Extra["_tenant_id"]` (set by Login UI at
   consent).
3. Validates user is an active member of the tenant via database lookup
   (`GetActiveMemberByTenantAndUserID`).
4. Injects `tenant_id` (string) into `id_token` and `access_token`.

The two claim sets (`groups` and `tenant_id`) do not conflict — they use different field
names and serve orthogonal purposes.

### Tenant-Service Lookup API

Tenant-service exposes an internal, unauthenticated lookup endpoint designed for
privileged callers (Login UI, hook-service):

```
GET /api/v0/tenants/lookup?email={email}
GET /api/v0/tenants/lookup?identity_id={kratos_identity_id}
```

The endpoint accepts **either** `email` or `identity_id`:
- `email`: resolves email → Kratos identity ID → active tenant memberships
- `identity_id`: skips the Kratos lookup, queries active tenant memberships directly

The `identity_id` parameter is being added as part of this work (small change to
tenant-service). This is necessary because hook-service may not have the user's email
available — the `email` claim is only present when the `email` scope is requested.
However, the `subject` (Kratos identity ID) is **always** available in the
`TokenHookRequest`.

Each `Tenant` in the response includes `id`, `name`, `created_at`, and `enabled`.
The endpoint returns only enabled tenants with active memberships.

## Decision

Enhance hook-service to optionally call tenant-service's **`LookupTenantsByEmail`
API** during the token hook flow. Hook-service remains the **sole Hydra token hook**
and takes responsibility for injecting **both** `groups` and `tenant_id` claims.

The integration uses tenant-service's internal lookup endpoint
(`GET /api/v0/tenants/lookup`), which is extended with an `identity_id` query
parameter. Hook-service prefers `identity_id` (always available from the Hydra
session subject) and falls back to `email` when available. This endpoint is
unauthenticated by design, intended for privileged internal callers.

### Changes to Tenant-Service

The `LookupTenantsByEmail` RPC is extended to accept an optional `identity_id` field:

```protobuf
message LookupTenantsByEmailRequest {
    string email = 1;
    string identity_id = 2;
}
```

Validation is performed in the handler (not via proto annotations) to support
exactly-one-of semantics: the handler validates email format with `net/mail` and
identity_id format with `uuid.Parse`. When `identity_id` is set, the service skips
the Kratos email→identity resolution and queries `ListActiveTenantsByUserID` directly.
This is both faster (one fewer network hop) and works for token hook requests where
the `email` scope was not requested.

### Flow

```
Hydra ──POST /api/v0/hook/hydra──▶ hook-service
                                       │
                           ┌───────────┴───────────┐
                           │                       │
                    (existing)              (new, if configured)
                    Fetch groups            Validate tenant
                    Check OpenFGA           membership
                           │                       │
                           │            Extract tenant_id from
                           │            session.Extra["_tenant_id"]
                           │            Extract identity_id from
                           │            session.Subject (always available)
                           │                       │
                           │            GET /api/v0/tenants/lookup?identity_id={id}
                           │                       │
                           │               ┌───────▼───────┐
                           │               │ tenant-service │
                           │               │  resolves      │
                           │               │  email→tenants │
                           │               └───────┬───────┘
                           │                       │
                           │            Check: is tenant_id in
                           │            the returned list?
                           │                       │
                           └───────────┬───────────┘
                                       │
                              Build claims:
                              { "groups": [...],
                                "tenant_id": "..." }
                                       │
                                       ▼
                                   Response to Hydra
```

### Steps in the Token Hook Handler

1. Parse `TokenHookRequest` (existing).
2. Extract user identity and fetch groups (existing).
3. Authorize request via OpenFGA (existing).
4. **NEW**: If tenant-service is configured (`TENANT_SERVICE_URL` is set):
   a. Extract `tenant_id` from `req.Session.Extra["_tenant_id"]`.
   b. If no `tenant_id` in session: skip tenant enrichment (proceed without tenant
      claims).
   c. Extract `identity_id` from `req.Session.Subject` (always available).
   d. Call `GET {TENANT_SERVICE_URL}/api/v0/tenants/lookup?identity_id={identity_id}`.
      No authentication header needed — the endpoint is unauthenticated.
   e. Check if `tenant_id` appears in the returned tenant list.
   f. If found: inject `tenant_id` into both `id_token` and `access_token` claims.
   g. If not found: return `403 Forbidden` to Hydra (deny the token).
   h. On network error / 5xx: **fail closed** — return 500 to Hydra.
5. Return `TokenHookResponse` to Hydra with both `groups` and `tenant_id` claims.

### Configuration

| Variable | Default | Required | Purpose |
|----------|---------|----------|---------|
| `TENANT_SERVICE_URL` | `""` (disabled) | No | Base URL of tenant-service (e.g., `http://tenant-service:8000`) |

When `TENANT_SERVICE_URL` is empty, hook-service behaves exactly as before — no tenant
enrichment, full backward compatibility.

No API token is needed — the `LookupTenantsByEmail` endpoint is unauthenticated by
design, intended for internal privileged callers within the cluster.

### Error Handling

The integration **fails closed** — any failure in contacting tenant-service results in
hook-service returning an error to Hydra, which blocks token issuance:

| Scenario | Hook-service behavior |
|----------|----------------------|
| `tenant_id` not in session | Skip tenant enrichment, proceed with groups only |
| `tenant_id` found in lookup response | Inject `tenant_id` claim |
| `tenant_id` not in lookup response | Return `403 Forbidden` to Hydra |
| Lookup returns empty list (email unknown) | Return `403 Forbidden` to Hydra |
| Tenant-service returns `4xx` | Return `500` to Hydra |
| Tenant-service returns `5xx` | Return `500` to Hydra |
| Tenant-service unreachable (timeout/DNS) | Return `500` to Hydra |
| `TENANT_SERVICE_URL` not configured | Skip tenant enrichment entirely |

### Operator Integration (Juju Charms)

- **hook-service-operator**: Adds a `requires: tenant-service-info` relation using the
  existing `tenant_service_info` charm library. When the relation is established,
  hook-service-operator reads `service_url` from the relation databag and sets
  `TENANT_SERVICE_URL` in the workload environment.
- **tenant-service-operator**: Already provides the `tenant-service-info` relation
  (publishes `service_url` and `grpc_url`). Removes its `hydra-token-hook` relation since
  hook-service now handles tenant validation during the token hook.
- **hydra-operator**: No changes. Only hook-service registers as the token hook.

### Terraform Integration

The `iam-bundle-integration` Terraform plan adds a `juju_integration` resource:
```hcl
resource "juju_integration" "hook_service_tenant_service" {
  model = var.model_name
  application {
    name     = module.hook_service.app_name
    endpoint = "tenant-service-info"
  }
  application {
    name     = module.tenant_service.app_name
    endpoint = "tenant-service-info"
  }
}
```

## Rationale

### Uses Existing Unauthenticated Lookup API

The `LookupTenantsByEmail` endpoint (`GET /api/v0/tenants/lookup`) is an existing,
stable, internal endpoint designed for privileged callers. It requires no
authentication tokens — simplifying configuration. The `identity_id` (subject) is
always available in the Hydra `TokenHookRequest`, so hook-service can always call
this endpoint reliably regardless of which OAuth scopes were requested.

### Separation of Concerns

Tenant-service continues to own all tenant/membership logic — storage, authorization,
and API. Hook-service only consumes the public API to validate membership, with no
knowledge of tenant-service internals.

### Simplicity

The validation logic in hook-service is trivial: call the lookup endpoint with the
user's identity_id, check if `tenant_id` is in the returned list. No authentication
setup, no request forwarding, no response merging, no dependency on
`oauth2.TokenHookRequest` serialization.

### Backward Compatibility

The feature is entirely opt-in. Without `TENANT_SERVICE_URL`, hook-service behaves
identically to today. Existing deployments are unaffected.

### Fail-Closed Security

If tenant-service is misconfigured or unreachable, tokens are **not** issued. This
prevents tokens from being issued without proper tenant validation in deployments where
multi-tenancy is expected.

### Performance

Adds one HTTP round-trip per token request to tenant-service (same datacenter /
Kubernetes cluster). Expected latency: 1-3ms. When called with `identity_id`, the
lookup skips the Kratos email→identity resolution and goes directly to a single
indexed database query (join on `memberships` and `tenants` tables filtered by
user_id and `enabled=true`).

## Alternatives Considered

### Forward Full TokenHookRequest to Tenant-Service Webhook

Forward the entire `TokenHookRequest` to tenant-service's
`POST /api/v0/webhooks/token` endpoint and merge the `TokenHookResponse`.

- ✅ Zero changes to tenant-service
- ✅ Tenant-service formats its own claims
- ❌ Couples hook-service to the Hydra webhook request/response contract
- ❌ Requires hook-service to understand and merge `TokenHookResponse` structures
- ❌ Tenant-service's webhook endpoint becomes a de-facto internal API, coupling
  release cycles
- ❌ More complex: serialize full request, deserialize response, merge maps

**Verdict**: Works but unnecessarily complex. Using the public REST API is simpler and
more maintainable.

### Generic Webhook Chaining (N downstream hooks)

Design hook-service as a generic "webhook aggregator" that can forward to N downstream
token hook endpoints configured via an ordered list:

```
DOWNSTREAM_HOOKS=http://tenant-service:8000/api/v0/webhooks/token,http://other-service/hook
```

Hook-service would iterate through each endpoint, forwarding the request and merging
responses sequentially.

- ✅ Fully extensible — supports arbitrary future hook services
- ✅ No code changes needed per new downstream service
- ❌ Over-engineered for current requirements (exactly 2 services)
- ❌ Complex error semantics: what if hook #2 fails? Roll back hook #1's claims?
- ❌ Ordering dependencies between hooks are implicit and fragile
- ❌ Harder to reason about in Juju relations (each downstream needs a relation)

**Verdict**: Valid long-term direction but premature. Documented here for future
reference. If a third hook service is needed, revisit this approach.

### Use ListUserTenants (Authenticated Admin Endpoint)

Use `GET /api/v0/users/{user_id}/tenants` instead of the lookup endpoint. This takes a
user UUID and returns their tenants.

- ✅ Direct user_id lookup, no email → Kratos resolution step
- ❌ Requires JWT authentication — hook-service would need an OAuth client + token
  management to call this endpoint
- ❌ More configuration: `TENANT_SERVICE_API_TOKEN`, token refresh logic
- ❌ Admin endpoint, not designed for internal service-to-service calls

**Verdict**: Higher configuration complexity for marginal benefit. The unauthenticated
lookup endpoint is simpler and purpose-built for this use case.

### Add a Dedicated Validation Endpoint to Tenant-Service

Add a purpose-built gRPC/HTTP endpoint like
`POST /api/v0/internal/memberships/validate` that takes `(user_id, tenant_id)` and
returns membership status.

- ✅ Clean API contract, purpose-built for this use case
- ✅ Minimal response payload (boolean + role)
- ❌ Requires changes to tenant-service (new proto RPC, handler, service method)
- ❌ `LookupTenantsByEmail` already provides the necessary data

**Verdict**: Unnecessary given `LookupTenantsByEmail` exists. If validation needs grow
more complex (e.g., checking specific roles or permissions), this becomes the right path.

### Embed Tenant Logic in Hook-Service

Give hook-service direct database access to the tenant-service schema and validate
membership locally.

- ✅ No network overhead
- ❌ Violates service boundaries — two services reading the same tables
- ❌ Tight coupling makes independent deployment impossible
- ❌ Schema changes in tenant-service break hook-service

**Verdict**: Rejected. Violates the microservice architecture.

## Consequences

### Positive

- Both `groups` and `tenant_id` claims are present in tokens.
- Single Hydra token hook configuration.
- Minimal changes to tenant-service (one new optional query parameter).
- Existing hook-service deployments without tenant-service are unaffected.
- Clear operational model: relate hook-service to tenant-service via
  `tenant-service-info` to enable.
- Uses stable public API — no coupling to internal webhook formats.

### Negative

- Additional network hop per token request (1-5ms).
- hook-service must handle tenant-service failures (fail closed adds a failure mode).
- The lookup returns more data than strictly needed (full tenant list vs boolean
  membership check), though the list is expected to be small (< 10 tenants per user).
- Small change to tenant-service required: adding `identity_id` query parameter to
  the lookup endpoint.

### Neutral

- Tenant-service-operator loses its `hydra-token-hook` relation (replaced by
  hook-service handling tenant validation).
- Monitoring/alerting should cover the hook-service → tenant-service call (latency,
  error rate).

## Implementation Notes

### New Files in hook-service

- `internal/tenants/client.go` — HTTP client implementing `TenantValidatorInterface`
- `internal/tenants/client_test.go` — Unit tests with mock HTTP server
- `internal/tenants/noop.go` — No-op implementation when disabled
- `pkg/hooks/interfaces.go` — Add `TenantValidatorInterface`

### Interface

```go
// TenantValidatorInterface validates that a user is an active member of a tenant.
type TenantValidatorInterface interface {
    // ValidateMembership checks if the user identified by identityID is an active
    // member of the given tenant. Returns nil if valid, ErrNotMember if not, or an
    // error on failure.
    ValidateMembership(ctx context.Context, identityID, tenantID string) error
}
```

### Client Implementation (sketch)

```go
// Client calls tenant-service's lookup API to validate membership.
type Client struct {
    baseURL    string
    httpClient *http.Client
    tracer     tracing.TracingInterface
    monitor    monitoring.MonitorInterface
    logger     logging.LoggerInterface
}

func (c *Client) ValidateMembership(ctx context.Context, identityID, tenantID string) error {
    ctx, span := c.tracer.Start(ctx, "tenants.Client.ValidateMembership")
    defer span.End()

    lookupURL := fmt.Sprintf("%s/api/v0/tenants/lookup?identity_id=%s",
        c.baseURL, url.QueryEscape(identityID))
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, lookupURL, nil)
    if err != nil {
        return fmt.Errorf("cannot create request: %v", err)
    }

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return fmt.Errorf("cannot reach tenant-service: %v", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("tenant-service returned status %d", resp.StatusCode)
    }

    var result struct {
        Tenants []struct {
            ID string `json:"id"`
        } `json:"tenants"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return fmt.Errorf("cannot decode tenant-service response: %v", err)
    }

    for _, t := range result.Tenants {
        if t.ID == tenantID {
            return nil // Lookup already filters to enabled tenants with active memberships
        }
    }

    return ErrNotMember
}
```

### Integration in Token Hook Handler

```go
// In handleHydraHook, after existing group/authz logic:
if tenantID := extractTenantID(req); tenantID != "" {
    if err := s.tenantValidator.ValidateMembership(ctx, user.SubjectId, tenantID); err != nil {
        if errors.Is(err, tenants.ErrNotMember) {
            w.WriteHeader(http.StatusForbidden)
            return
        }
        w.WriteHeader(http.StatusInternalServerError)
        return
    }
    resp.Session.AccessToken["tenant_id"] = tenantID
    resp.Session.IDToken["tenant_id"] = tenantID
}
```

### Docker-Compose Changes

The tenant-service `docker-compose.dev.yml` should be updated to include hook-service
as a service, with `TENANT_SERVICE_URL` pointing to the tenant-service:

```yaml
hook-service:
  build: ../hook-service
  environment:
    TENANT_SERVICE_URL: http://tenant-service:8000
```

## References

- [ADR-001: Remove Salesforce Integration from Hot Path](001-remove-salesforce-integration.md)
- [Hydra Token Hook Documentation](https://www.ory.sh/docs/hydra/guides/claims-at-refresh)
- tenant-service ADR-0008: Tenant-Aware Login
- tenant-service protobuf: `api/proto/v0/tenant.proto` (`LookupTenantsByEmail` RPC)
- `tenant_service_info` charm library: `lib/charms/tenant_service_operator/v0/tenant_service_info.py`
