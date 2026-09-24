# Purpose

Onboard the hook-service admin API authorization rules to the Authorization Service via `rules.yaml` route configuration. Under this coarse-grained authorization model, all admin API endpoints require caller membership in the administrative role (`role:admin`). Authorization Service extAuthz evaluates incoming requests against `role:admin#assignee` in the shared OpenFGA store.

Key decisions:
- Route rules in `rules.yaml` map all `/api/v0/authz/*` endpoints to `permission: assignee`, `object_resource_type: role`, `object_resource_id: 'admin'`.
- All authorization checks delegate directly to the core `role` definition in `core.fga`; no custom OpenFGA module or types are required.
- Initial admin access is bootstrapped via a one-time seed of `user:<admin_id> → assignee → role:admin` in the shared OpenFGA store.
- No per-group objects or dynamic tuples are stored in OpenFGA; access governance is centralized at the `role:admin` level.

Non-goals:
- Custom OpenFGA type definitions or module onboarding files.
- Dynamic per-group permission tuple management.
- Modifying the token hook endpoint (`POST /api/v0/hook/hydra`) or app authorization endpoints.
- Defining Istio extAuthz infrastructure (see `authorization-service-admin-api-extauthz`).

## ADDED Requirements

### Requirement: Authorization Service rules.yaml for admin API endpoints
The system SHALL provide a `rules.yaml` file in Authorization Service mapping all hook-service admin API HTTP routes to `role:admin#assignee` checks.

#### Scenario: Group list endpoint
- **WHEN** Authorization Service extAuthz receives a `GET /api/v0/authz/groups` request
- **THEN** it checks whether the caller has `assignee` on `role:admin`

#### Scenario: Group create endpoint
- **WHEN** Authorization Service extAuthz receives a `POST /api/v0/authz/groups` request
- **THEN** it checks whether the caller has `assignee` on `role:admin`

#### Scenario: Group detail endpoint
- **WHEN** Authorization Service extAuthz receives a `GET /api/v0/authz/groups/{groupId}` request
- **THEN** it checks whether the caller has `assignee` on `role:admin`

#### Scenario: Group update endpoint
- **WHEN** Authorization Service extAuthz receives a `PUT /api/v0/authz/groups/{groupId}` request
- **THEN** it checks whether the caller has `assignee` on `role:admin`

#### Scenario: Group delete endpoint
- **WHEN** Authorization Service extAuthz receives a `DELETE /api/v0/authz/groups/{groupId}` request
- **THEN** it checks whether the caller has `assignee` on `role:admin`

#### Scenario: Group membership read endpoint
- **WHEN** Authorization Service extAuthz receives a `GET /api/v0/authz/groups/{groupId}/users` request
- **THEN** it checks whether the caller has `assignee` on `role:admin`

#### Scenario: User membership management endpoints
- **WHEN** Authorization Service extAuthz receives a `POST /api/v0/authz/groups/{groupId}/users` or `DELETE /api/v0/authz/groups/{groupId}/users/**` request
- **THEN** it checks whether the caller has `assignee` on `role:admin`

#### Scenario: User-centric group endpoints
- **WHEN** Authorization Service extAuthz receives a `GET /api/v0/authz/users/{userId}/groups` or `POST /api/v0/authz/users/{userId}/groups` request
- **THEN** it checks whether the caller has `assignee` on `role:admin`

#### Scenario: Excluded endpoints
- **WHEN** a request targets `/api/v0/hook/hydra`, `/api/v0/metrics`, or `/api/v0/status`
- **THEN** no rule in `rules.yaml` matches and the request is not subject to admin API authorization rules

### Requirement: Admin bootstrap seed
The system SHALL support bootstrapping initial administrative access by seeding the admin user into `role:admin`.

#### Scenario: Seeded admin user has access
- **WHEN** `user:<admin_id> → assignee → role:admin` is seeded in the Authorization Service OpenFGA store
- **THEN** that user is granted access across all hook-service admin API endpoints

#### Scenario: Unassigned user is denied
- **WHEN** a user who is not an assignee of `role:admin` requests any admin API endpoint
- **THEN** Authorization Service extAuthz denies the request with 403 Forbidden