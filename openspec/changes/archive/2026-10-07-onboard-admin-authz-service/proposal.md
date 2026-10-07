# Why

The hook-service manages user groups and group memberships used to enrich OAuth tokens during token issuance. Its admin API endpoints (`/api/v0/authz/*`) are currently protected only by local JWT authentication middleware with no centralized authorization checks. This change onboards the hook-service admin API authorization to the centralized Authorization Service, enforcing role-based access control (RBAC) via Istio `extAuthz`.

Under this coarse-grained authorization model, all admin API endpoints require caller membership in the administrative role (`role:admin`). A `rules.yaml` route configuration in Authorization Service maps incoming HTTP requests to `role:admin#assignee` checks against the core role model in the shared OpenFGA store. Because access is governed entirely through the core `role:admin` relation, no custom OpenFGA types or modules are required. This approach provides centralized access governance without requiring dynamic per-group permission publishing at runtime, while Kafka producer infrastructure is maintained in dormant standby for future event-driven authorization capabilities.

## What Changes

- Register `rules.yaml` in Authorization Service defining HTTP route-to-permission mappings that require `role:admin#assignee` for all admin API endpoints.
- Integrate with Istio `extAuthz` so Authorization Service authenticates and authorizes admin API requests before forwarding them to hook-service.
- Disable local JWT authentication middleware on admin routes (`AUTHENTICATION_ENABLED=false`); hook-service relies on Istio `extAuthz` for authentication and authorization.
- Exclude metrics (`/api/v0/metrics`), status (`/api/v0/status`), and token hook (`/api/v0/hook/hydra`) endpoints from extAuthz enforcement.
- Maintain Kafka producer infrastructure (`internal/kafka/`) in standby mode to preserve architectural readiness for future event publishing needs without publishing runtime group lifecycle events.
- Bootstrap admin access by seeding the initial admin user as an assignee of `role:admin` (`user:<admin_id> → assignee → role:admin`) in the Authorization Service shared OpenFGA store.

## Non-goals

- Introducing custom OpenFGA types or modules (delegates completely to `core.fga`'s `role:admin`).
- Fine-grained per-group ReBAC or dynamic tuple publishing for group creation, deletion, or membership updates.
- Modifying the token hook endpoint (`POST /api/v0/hook/hydra`) — it continues using its dedicated OpenFGA client and store.
- Modifying app-to-group authorization (`POST /groups/{id}/apps`) — it continues using existing direct OpenFGA write methods.
- Migrating hook-service's internal token hook store to the Authorization Service shared OpenFGA instance.

## Capabilities

### New Capabilities

- `authorization-service-model-onboarding`: Onboard hook-service admin API routing rules to Authorization Service via `rules.yaml` mapping routes to `role:admin#assignee`.
- `authorization-service-admin-api-extauthz`: Protect hook-service admin API endpoints with Istio + Authorization Service extAuthz, replacing local JWT middleware.
- `authorization-service-kafka-permission-pipeline`: Maintain dormant Kafka producer infrastructure for future event-driven authorization extensions.

### Modified Capabilities

<!-- No existing spec-level requirements change -->

## Impact

- **Authorization Service repository**: Add `rules.yaml` under `authz/model/services/hook-service/` mapping admin API routes to `role:admin#assignee`.
- **pkg/web/router.go**: Disable `AUTHENTICATION_ENABLED` for admin routes; trust Istio-forwarded `Authorization` header.
- **internal/kafka/**: Standby Kafka permission publisher for future extensibility.
- **cmd/serve.go**: Wire Kafka producer and configuration.
- **Dependencies**: `segmentio/kafka-go`, Authorization Service protobuf message definitions.
- **Istio configuration**: AuthorizationPolicy and EnvoyFilter for Authorization Service extAuthz (managed by platform operators).
- **One-time bootstrap operation**: Seed `user:<admin_id> → assignee → role:admin` in Authorization Service OpenFGA store.