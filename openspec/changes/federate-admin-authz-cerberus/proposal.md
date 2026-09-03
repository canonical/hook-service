## Why

The hook-service currently manages its own OpenFGA store and model for authorization, and its admin API endpoints are protected only by JWT authentication with no per-group authorization checks. This change federates the hook-service's admin API authorization to the centralized Cerberus authorization service, enabling per-group access control for group CRUD and membership operations.

A custom `hook-service` module is introduced in the Cerberus authorization model, named after the hook-service itself. The module defines the `group-in-claim` type with a role hierarchy (`admin` → `owner`) and a standalone `member` relation using `user with tenant_match` for consistency with the rest of the Cerberus ecosystem. A static `group-in-claim:__domain__` object controls access to collection-level endpoints, while per-group `group-in-claim:<group_id>` objects enforce per-group access control.

## What Changes

- Introduce a custom `hook-service` module in the Cerberus authorization model with a `group-in-claim` type using `admin` / `owner` / `member` relations and `user with tenant_match`.
- Add a Kafka producer to publish group ownership and membership permission updates to the `hook-service.permissions` topic (at-least-once delivery with idempotency keys; Kafka delivery failures do not affect the database transaction), consumed by Cerberus's async worker pipeline.
- Create Cerberus `rules.yaml` defining HTTP route-to-permission mappings for all group and membership admin API endpoints.
- Integrate with Istio extAuthz so Cerberus authorizes admin API requests before they reach hook-service.
- Disable `AUTHENTICATION_ENABLED` for admin API routes (Cerberus extAuthz handles authentication and authorization).
- Metrics (`/api/v0/metrics`), status (`/api/v0/status`), and token hook (`/api/v0/hook/hydra`) endpoints are excluded from extAuthz enforcement.
- On group creation, record the creator with owner role in the database and publish an `owner` tuple for the creator via Kafka (`user:<creator_id> → owner → group-in-claim:<group_id>`).
- On user addition to a group, publish a `member` tuple via Kafka (`user:<user_id> → member → group-in-claim:<group_id>`).
- On user removal, publish a delete `member` tuple via Kafka.
- On group deletion, retrieve the group's owner and member user IDs from PostgreSQL, perform the database deletion, and publish delete tuples for the owner and all members via Kafka to maintain OpenFGA store hygiene.
- Seed a one-time static `admin` tuple on `group-in-claim:__domain__` to bootstrap initial admin access.
- Group deletion is restricted to domain admins only (checks `admin` on `__domain__`, not on the group).

## Non-goals

- App-to-group authorization (`POST /groups/{id}/apps`) — not in scope
- Token hook endpoint (`POST /hook/hydra`) — continues using its own OpenFGA client and store, unchanged
- Migrate hook-service to the Cerberus shared OpenFGA store — hook-service keeps its own OpenFGA instance
- Kafka pipeline for anything other than `group-in-claim` permission tuples
- Remove existing OpenFGA write methods — they remain for app-to-group authorization which is not in scope

## Capabilities

### New Capabilities

- `cerberus-model-federation`: Federate hook-service's admin API authorization model to Cerberus via a custom `hook-service` module and `rules.yaml` route-to-permission mappings
- `cerberus-admin-api-extauthz`: Protect hook-service admin API endpoints with Istio + Cerberus extAuthz, replacing JWT middleware
- `cerberus-kafka-permission-pipeline`: Publish `group-in-claim` permission updates to Kafka for Cerberus to apply to the shared OpenFGA store

### Modified Capabilities

<!-- No existing spec-level requirements change -->

## Impact

- **Cerberus repository**: Add `hook-service` module file and `rules.yaml` mapping hook-service admin API routes
- **pkg/web/router.go**: Disable `AUTHENTICATION_ENABLED` for admin routes; trust Istio-forwarded `Authorization` header
- **pkg/groups/service.go**: Persist owner identity on group creation, and publish Kafka messages on group create, user add/remove, and group delete (cleaning up owner and member tuples)
- **internal/kafka/**: New package for Kafka permission publisher
- **cmd/serve.go**: Wire Kafka producer
- **New dependencies**: `segmentio/kafka-go`, Cerberus protobuf (`PermissionUpdateEnvelope`)
- **Istio configuration**: AuthorizationPolicy + EnvoyFilter for Cerberus extAuthz (authorization service responsibility)
- **One-time manual operation**: Seed `user:<admin_id> → admin → group-in-claim:__domain__` in the Cerberus shared OpenFGA store