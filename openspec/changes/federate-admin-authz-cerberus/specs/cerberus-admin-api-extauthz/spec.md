## Purpose

Protect the hook-service admin API endpoints with Istio external authorization (extAuthz) delegating to the Cerberus authorization service, replacing the current JWT authentication middleware. Cerberus handles both authentication (JWT verification via STS exchange) and authorization (OpenFGA tuple checks against the `hook-service` module via route rules).

Key decisions:
- Disable JWT authentication on admin routes by setting `AUTHENTICATION_ENABLED=false`. Cerberus extAuthz handles JWT verification and forwards the `Authorization` header to hook-service.
- The token hook endpoint (`POST /api/v0/hook/hydra`) is excluded from extAuthz enforcement — it continues using its existing `API_TOKEN` Bearer token middleware.
- Metrics (`/api/v0/metrics`) and status (`/api/v0/status`) endpoints are excluded from extAuthz enforcement.
- Istio AuthorizationPolicy is configured to apply extAuthz only to admin API paths (`/api/v0/authz/*`).
- An EnvoyFilter registers Cerberus as the extAuthz provider for the hook-service's sidecar.
- The Istio AuthorizationPolicy and EnvoyFilter are the responsibility of the authorization service, not the hook-service.

Non-goals:
- This spec does not define the `hook-service` module or `rules.yaml` (see `cerberus-model-federation`).
- This spec does not define the Kafka permission pipeline (see `cerberus-kafka-permission-pipeline`).
- This spec does not cover Istio infrastructure deployment — the AuthorizationPolicy and EnvoyFilter are assumed to be applied by operators.

## Requirements

### Requirement: Admin API endpoints protected by Cerberus extAuthz
The system SHALL route all admin API requests through Istio's extAuthz filter, delegating authorization decisions to Cerberus.

#### Scenario: Authenticated admin requests pass through
- **WHEN** an authenticated admin user makes a request to an admin API endpoint
- **THEN** Cerberus extAuthz verifies the JWT, checks the `group-in-claim` OpenFGA tuple, and returns allow
- **THEN** Istio forwards the request to hook-service with the `Authorization` header intact

#### Scenario: Unauthenticated requests are denied
- **WHEN** a request to an admin API endpoint lacks a valid session cookie
- **THEN** Cerberus extAuthz returns 401
- **THEN** Istio denies the request before it reaches hook-service

#### Scenario: Unauthorized requests are denied
- **WHEN** an authenticated user without the required `group-in-claim` permission makes a request to an admin API endpoint
- **THEN** Cerberus extAuthz returns 403
- **THEN** Istio denies the request before it reaches hook-service

### Requirement: Token hook, metrics, and status endpoints excluded from extAuthz
The token hook endpoint, metrics endpoint, and status endpoint SHALL NOT be protected by Cerberus extAuthz.

#### Scenario: Token hook bypasses extAuthz
- **WHEN** Hydra calls `POST /api/v0/hook/hydra`
- **THEN** the request is not subject to extAuthz enforcement
- **THEN** hook-service's existing `AuthMiddleware` validates the `API_TOKEN` Bearer token

#### Scenario: Metrics bypasses extAuthz
- **WHEN** a request is made to `GET /api/v0/metrics`
- **THEN** the request is not subject to extAuthz enforcement

#### Scenario: Status bypasses extAuthz
- **WHEN** a request is made to `GET /api/v0/status`
- **THEN** the request is not subject to extAuthz enforcement

### Requirement: JWT authentication disabled on admin routes
The JWT authentication middleware SHALL be disabled on admin routes by setting `AUTHENTICATION_ENABLED=false`, with Cerberus extAuthz handling authentication and authorization.

#### Scenario: Admin routes have no JWT middleware
- **WHEN** the hook-service starts with `AUTHENTICATION_ENABLED=false`
- **THEN** the `authzRouter` does not include the `authentication.Middleware.Authenticate()` handler
- **THEN** hook-service trusts the `Authorization` header forwarded by Istio without re-verifying the JWT