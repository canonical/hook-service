## Purpose

Federate the hook-service's admin API authorization model to the Cerberus authorization service via a custom `hook-service` module. The module defines a `group-in-claim` type with a role hierarchy (`admin` → `owner`) and a decoupled `member` relation using `user with tenant_match`, a static `__domain__` object for collection-level access control, and per-group objects for per-group access control. Cerberus extAuthz uses `rules.yaml` route-to-permission mappings to enforce authorization on all admin API endpoints.

Key decisions:
- A custom `hook-service` module is used rather than the core `group` type. The core `group` type is shared infrastructure used by other services (e.g., portal) for groups not managed by the hook-service admin API. A dedicated module isolates hook-service's authorization domain.
- The `group-in-claim` type uses `admin` / `owner` / `member` relations with `user with tenant_match`, consistent with every other module in the Cerberus ecosystem. `admin` inherits `owner` for domain-level management. `member` represents token claim inclusion and is decoupled from `owner` (an administrative owner is not implicitly a claim member unless explicitly added).
- A static `group-in-claim:__domain__` object controls access to collection-level endpoints (list groups, create groups, delete groups, manage users). A single `admin` tuple seeded once bootstraps initial access.
- Per-group `group-in-claim:<group_id>` objects control per-group access (view details, edit, manage members), which require `owner` permissions. These are published dynamically via Kafka on group lifecycle events.
- The creator of a group is recorded in PostgreSQL with the owner role and published with `owner` on the new group object via Kafka. Group deletion requires domain `admin` on `__domain__`, not per-group admin, and triggers Kafka events to clean up all associated tuples.

Non-goals:
- This spec does not define the Kafka pipeline for publishing permission tuples (see `cerberus-kafka-permission-pipeline`).
- This spec does not define the Istio extAuthz integration (see `cerberus-admin-api-extauthz`).
- This spec does not cover app-to-group authorization (`POST /groups/{id}/apps`) or the token hook endpoint.

## Module definition

```openfga
module hook-service

type group-in-claim
  relations
    define member: [user with tenant_match]
    define owner: [user with tenant_match] or admin
    define admin: [user with tenant_match]
```

The type relations mean:
- A user with `admin` is also implicitly `owner` (transitive through `or` chain)
- A user with `owner` has full management and inspection rights over the group (view group details, edit group, manage members)
- A user with `member` represents membership in the token claim and does not grant Admin API access

## Requirements

### Requirement: hook-service module in Cerberus
The system SHALL provide a `hook-service` module in the Cerberus authorization model defining the `group-in-claim` type with `admin`, `owner`, and `member` relations, each accepting `user with tenant_match`.

#### Scenario: Module is loadable
- **WHEN** Cerberus starts with the `hook-service` module
- **THEN** the `group-in-claim` type is available for tuple writes and extAuthz checks

### Requirement: Static domain object for collection-level access
The system SHALL use a static `group-in-claim:__domain__` object to control access to collection-level endpoints (list groups, create groups, delete groups, manage user-centric endpoints).

#### Scenario: Admin user seeded for domain access
- **WHEN** `user:<admin_id> → admin → group-in-claim:__domain__` is seeded
- **THEN** the admin user can list groups, create groups, delete groups, and access user-centric endpoints

#### Scenario: Non-admin user cannot access collection-level endpoints
- **WHEN** a user without `admin` on `group-in-claim:__domain__` makes a request to collection-level endpoints
- **THEN** Cerberus extAuthz returns 403

### Requirement: Per-group objects for per-group access
The system SHALL use per-group `group-in-claim:<group_id>` objects to control per-group access (view details, edit, manage members).

#### Scenario: Group owner can view group details
- **WHEN** a user with `owner` (or `admin`) on `group-in-claim:<group_id>` requests `GET /api/v0/authz/groups/{id}`
- **THEN** Cerberus extAuthz allows the request

#### Scenario: Group owner can edit group and manage members
- **WHEN** a user with `owner` (or `admin`) on `group-in-claim:<group_id>` requests `PUT /api/v0/authz/groups/{id}`, `GET /api/v0/authz/groups/{id}/users`, `POST /api/v0/authz/groups/{id}/users`, or `DELETE /api/v0/authz/groups/{id}/users/*`
- **THEN** Cerberus extAuthz allows the request

#### Scenario: Non-owner cannot access per-group admin endpoints
- **WHEN** a user without `owner` (including users with only `member`) on `group-in-claim:<group_id>` requests any per-group admin endpoint
- **THEN** Cerberus extAuthz returns 403

### Requirement: Group deletion restricted to domain admins
The system SHALL restrict group deletion to domain admins by checking `admin` on `group-in-claim:__domain__` rather than on the group itself.

#### Scenario: Domain admin deletes a group
- **WHEN** a user with `admin` on `group-in-claim:__domain__` requests `DELETE /api/v0/authz/groups/{id}`
- **THEN** Cerberus extAuthz allows the request

#### Scenario: Group owner cannot delete their own group
- **WHEN** a user with only `owner` on `group-in-claim:<group_id>` (but not `admin` on `__domain__`) requests `DELETE /api/v0/authz/groups/{id}`
- **THEN** Cerberus extAuthz returns 403

### Requirement: Cerberus rules.yaml for admin API endpoints
The system SHALL provide a `rules.yaml` file in the Cerberus repository that maps hook-service admin API HTTP routes to `group-in-claim` permission checks.

#### Scenario: Group list endpoint
- **WHEN** Cerberus extAuthz receives a `GET /api/v0/authz/groups` request
- **THEN** it checks whether the caller has `admin` on `group-in-claim:__domain__`

#### Scenario: Group create endpoint
- **WHEN** Cerberus extAuthz receives a `POST /api/v0/authz/groups` request
- **THEN** it checks whether the caller has `admin` on `group-in-claim:__domain__`

#### Scenario: Group detail endpoint
- **WHEN** Cerberus extAuthz receives a `GET /api/v0/authz/groups/{groupId}` request
- **THEN** it checks whether the caller has `owner` on `group-in-claim:{groupId}`

#### Scenario: Group update endpoint
- **WHEN** Cerberus extAuthz receives a `PUT /api/v0/authz/groups/{groupId}` request
- **THEN** it checks whether the caller has `owner` on `group-in-claim:{groupId}`

#### Scenario: Group delete endpoint
- **WHEN** Cerberus extAuthz receives a `DELETE /api/v0/authz/groups/{groupId}` request
- **THEN** it checks whether the caller has `admin` on `group-in-claim:__domain__`

#### Scenario: Group membership read endpoint
- **WHEN** Cerberus extAuthz receives a `GET /api/v0/authz/groups/{groupId}/users` request
- **THEN** it checks whether the caller has `owner` on `group-in-claim:{groupId}`

#### Scenario: User membership management endpoints
- **WHEN** Cerberus extAuthz receives a `POST /api/v0/authz/groups/{groupId}/users` or `DELETE /api/v0/authz/groups/{groupId}/users/*` request
- **THEN** it checks whether the caller has `owner` on `group-in-claim:{groupId}`

#### Scenario: User-centric endpoints
- **WHEN** Cerberus extAuthz receives a `GET /api/v0/authz/users/{userId}/groups` or `POST /api/v0/authz/users/{userId}/groups` request
- **THEN** it checks whether the caller has `admin` on `group-in-claim:__domain__`

#### Scenario: Token hook, metrics, and status endpoints excluded
- **WHEN** Cerberus extAuthz receives a request to `/api/v0/hook/hydra`, `/api/v0/metrics`, or `/api/v0/status`
- **THEN** no rule matches and the request is allowed through (Istio AuthorizationPolicy excludes these paths from extAuthz)

### Requirement: Publish owner on group creation
The system SHALL publish an `owner` tuple for the group creator via Kafka when a group is created. The per-group `admin` relation is never published on group creation.

#### Scenario: Creator gets owner tuple
- **WHEN** a group is created
- **THEN** the Kafka pipeline publishes `user:<creator_id> → owner → group-in-claim:<group_id>`

#### Scenario: Owner can view and manage group
- **WHEN** a creator requests `GET /api/v0/authz/groups/{groupId}`, `PUT /api/v0/authz/groups/{groupId}`, or any membership management endpoint
- **THEN** the Cerberus extAuthz `owner` check succeeds

### Requirement: Domain admin bootstrap seed
The system SHALL support a one-time manual seeding of an `admin` tuple on `group-in-claim:__domain__` to bootstrap initial admin access.

#### Scenario: Admin user has access to collection-level endpoints
- **WHEN** `user:<admin_id> → admin → group-in-claim:__domain__` is seeded
- **THEN** the admin can list groups, create groups, delete groups, and manage user-centric endpoints

#### Scenario: Admin becomes owner of created groups
- **WHEN** the seeded admin creates a group
- **THEN** they receive an `owner` tuple on the new `group-in-claim:<group_id>` via the Kafka pipeline (same as any other creator)