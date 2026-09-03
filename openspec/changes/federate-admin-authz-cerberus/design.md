## Context

The hook-service currently manages authorization in three ways:
1. **Admin API endpoints** (`/api/v0/authz/*`): Protected only by JWT authentication middleware. No per-group authorization exists — any authenticated user can perform any admin operation.
2. **Token hook endpoint** (`/api/v0/hook/hydra`): Uses a direct OpenFGA client for `CanAccess`/`BatchCanAccess` checks. Group membership is passed as contextual tuples at check time.
3. **Permission updates**: Direct OpenFGA `WriteTuple`/`DeleteTuple` calls from the `pkg/authorization` service when apps are added to or removed from groups.

The Cerberus authorization service (`canonical/authorization-service`) provides a centralized authorization platform with a shared OpenFGA store, a modular authorization model, Istio extAuthz for HTTP route-based enforcement, and a Kafka-based async pipeline for permission tuple updates.

This design covers federating the admin API (group and membership management) to Cerberus via a custom `hook-service` module. The token hook endpoint continues using its direct OpenFGA client and its own OpenFGA store.

### Authorization model

```openfga
module hook-service

type group-in-claim
  relations
    define member: [user with tenant_match]
    define owner: [user with tenant_match] or admin
    define admin: [user with tenant_match]
```

The module is named after the hook-service's core function: enriching OAuth tokens with group claims. The `group-in-claim` type defines administrative hierarchy alongside token claim membership:

- `admin` → implicitly also `owner` (transitive through `or` chain)
- `owner` → per-group administration and inspection (view details, edit, manage members)
- `member` → token claim inclusion (independent of administrative roles)

Two categories of objects exist:

| Object | Purpose | Lifecycle |
|--------|---------|-----------|
| `group-in-claim:__domain__` | Controls access to collection-level endpoints (list groups, create groups, delete groups, manage users) | Static — seeded once manually |
| `group-in-claim:<group_id>` | Controls per-group access (view details, edit, manage members) | Dynamic — published via Kafka on group lifecycle events |

The `__domain__` object uses double-underscore naming to signal it is a synthetic/internal object, not a real business entity.

### Seeding

A single one-time manual seed bootstraps initial admin access:

| Object | Tuple |
|--------|-------|
| `group-in-claim:__domain__` | `user:<admin_id> → admin → group-in-claim:__domain__` |

### Kafka-published tuples per group lifecycle

| Event | Operation | Tuple |
|-------|-----------|-------|
| Group created | WRITE | `user:<creator> → owner → group-in-claim:<group_id>` |
| User added | WRITE | `user:<added> → member → group-in-claim:<group_id>` |
| User removed | DELETE | `user:<removed> → member → group-in-claim:<group_id>` |
| Group deleted | DELETE | `user:<owner> → owner → group-in-claim:<group_id>`<br/>`user:<member> → member → group-in-claim:<group_id>` (for each member) |

### Rules.yaml mapping

| Endpoint | Permission | Object |
|----------|-----------|--------|
| `GET /api/v0/authz/groups` | `admin` | `group-in-claim:__domain__` |
| `POST /api/v0/authz/groups` | `admin` | `group-in-claim:__domain__` |
| `GET /api/v0/authz/groups/{id}` | `owner` | `group-in-claim:{id}` |
| `PUT /api/v0/authz/groups/{id}` | `owner` | `group-in-claim:{id}` |
| `DELETE /api/v0/authz/groups/{id}` | `admin` | `group-in-claim:__domain__` |
| `GET /api/v0/authz/groups/{id}/users` | `owner` | `group-in-claim:{id}` |
| `POST /api/v0/authz/groups/{id}/users` | `owner` | `group-in-claim:{id}` |
| `DELETE /api/v0/authz/groups/{id}/users/*` | `owner` | `group-in-claim:{id}` |
| `GET /api/v0/authz/users/{id}/groups` | `admin` | `group-in-claim:__domain__` |
| `POST /api/v0/authz/users/{id}/groups` | `admin` | `group-in-claim:__domain__` |

## Goals / Non-Goals

**Goals:**
- Protect admin API endpoints with per-group, role-based authorization via Cerberus extAuthz
- Publish `group-in-claim` permission updates to Kafka for Cerberus to apply to its shared OpenFGA store
- Maintain OpenFGA store hygiene by deleting all associated tuples on group deletion
- Disable `AUTHENTICATION_ENABLED` for admin routes (Cerberus extAuthz handles auth)

**Non-Goals:**
- Migrate the token hook endpoint — it continues using its own OpenFGA client and store
- Migrate hook-service to the Cerberus shared OpenFGA store — hook-service keeps its own OpenFGA instance
- Migrate app-to-group authorization (`POST /groups/{id}/apps`) — out of scope; existing direct OpenFGA writes remain
- Remove existing OpenFGA write methods — they remain for app-to-group authorization which is not in scope

## Decisions

### D1: Custom `hook-service` module (not core `group` type)

**Chosen**: Custom `hook-service` module with a `group-in-claim` type using `admin` / `owner` / `member` relations and `user with tenant_match`.

**Alternatives considered**:
- Core `group` type with `owner`/`member`: The core `group` type is shared infrastructure; other services (e.g., portal) may use it for groups not managed by the hook-service admin API. A custom module isolates hook-service's authorization domain.
- `generic-asset` with `viewer`/`editor`: The three-tier hierarchy (`admin` → `owner` → `member`) maps more naturally to group administration semantics than a flat two-tier model.

**Rationale**: A dedicated module gives hook-service full ownership of its authorization model. The `admin` tier is reserved exclusively for the `__domain__` object — it is never published on per-group objects. The module name `group-in-claim` reflects the hook-service's core responsibility of enriching tokens with group claims.

### D2: Decoupled owner and member roles on group creation

**Chosen**: On group creation, record the creator in PostgreSQL with the owner role and publish only the `owner` tuple for the creator (`user:<creator> → owner → group-in-claim:<group_id>`). Do not automatically publish a `member` tuple unless the user is explicitly added as a member.

**Rationale**: In hook-service, `member` defines membership in group claims for token issuance. An administrative owner is not necessarily a member of the group in token claims. Decoupling `member` from `owner` in the OpenFGA model (`define member: [user with tenant_match]`) ensures clean separation of concerns between administrative access (`owner`) and claim membership (`member`).

### D3: Kafka for permission updates, decoupled from DB transaction

**Chosen**: PostgreSQL writes are synchronous within the request transaction. Kafka publishes are fire-and-forget with respect to the transaction — they do not affect the database outcome. If the Kafka publish fails, the database record is still committed and the request succeeds. Tuple application to OpenFGA is async via Cerberus's worker.

**Rationale**: The `groups` and `group_members` tables are the source of truth for membership listing. Kafka publishes `group-in-claim` tuples so Cerberus extAuthz can enforce per-group access control on admin API endpoints. Eventual consistency in OpenFGA is acceptable because the Cerberus worker applies tuples within seconds. The database record is the primary artifact — rolling it back for a Kafka delivery failure is worse than a temporarily missing tuple.

### D4: Kafka producer with at-least-once semantics

**Chosen**: `requiredAcks=RequireAll`, `maxAttempts=3`, idempotency keys on each message. Kafka delivery failures do not affect the database transaction — the DB write succeeds regardless.

**Rationale**: Lost permission messages mean missing OpenFGA tuples, which means authorization checks fail (deny-by-default). At-least-once delivery with idempotent processing on the Cerberus side is the standard pattern. The DB is the source of truth, so rolling it back for a Kafka delivery failure is worse than a missing tuple.

### D5: Disable JWT authentication for admin routes

**Chosen**: Set `AUTHENTICATION_ENABLED=false` to disable JWT middleware on admin routes. Cerberus extAuthz handles authentication and authorization.

**Alternatives considered**: Physically remove the middleware code from `pkg/web/router.go`. Rejected — using the existing config flag is simpler, less invasive, and preserves the code for non-Istio deployments.

### D6: Group deletion restricted to domain admins

**Chosen**: `DELETE /api/v0/authz/groups/{id}` checks `admin` on `group-in-claim:__domain__`, not on the group itself.

**Rationale**: Deleting a group is destructive — it cascades and removes all members. Requiring domain-level `admin` ensures that a group owner cannot destroy the group; only domain admins can. An owner can manage members but cannot delete the group.

### D7: `user with tenant_match`

**Chosen**: Use `user with tenant_match` on all relations, consistent with every other module in the Cerberus ecosystem.

**Rationale**: When `MULTITENANCY_ENABLED=false`, the `tenant_match` condition is a pass-through with zero cost. When enabled, it enforces tenant isolation. Using it from day one avoids a future migration.

### D8: Publish delete tuples for owner and all members on group deletion

**Chosen**: On group deletion, query PostgreSQL for the group's owner and members, execute the database deletion, and publish `PERMISSION_OP_DELETE` events to Kafka for the owner and all member tuples.

**Rationale**: PostgreSQL `group_members` is the authoritative source of truth for both member users and the group owner. Proactively publishing individual DELETE events cleans up the OpenFGA store, prevents dangling/stale tuples, and keeps Cerberus's OpenFGA state synchronized with hook-service without requiring a background reconciliation job. If any Kafka publish fails, a warning is logged while the database transaction succeeds.

## Risks / Trade-offs

| Risk | Mitigation |
|------|-----------|
| Kafka publish failure during group creation leaves missing tuples | At-least-once delivery with idempotency keys maximizes delivery success. If a message is still lost, eventual consistency is acceptable — the tuple can be reconciled by re-triggering the group operation. |
| Eventual consistency: tuple not yet applied when admin API is called | Cerberus worker applies within seconds; admin API is low-traffic |
| Kafka publish failure during group deletion leaves stale tuples | At-least-once delivery with retries. Stale tuples for a deleted group are harmless because the group no longer exists in PostgreSQL, and deletion is authorized via `__domain__`. |
| `tenant_match` condition introduces tenant isolation | When `MULTITENANCY_ENABLED=false`, condition is pass-through; when enabled, it's a net improvement |
| Two OpenFGA instances (hook-service own store + Cerberus shared store) | Separate stores are intentional — hook-service's token hook uses its own store for app-to-group checks. Configuration drift is not a concern since the stores serve different purposes. |
| Group ID format mismatch (hook-service base64-encodes group IDs in OpenFGA) | Kafka-published tuples use raw UUIDs matching the `group-in-claim` model. Legacy tuples in hook-service's own store are not migrated. |