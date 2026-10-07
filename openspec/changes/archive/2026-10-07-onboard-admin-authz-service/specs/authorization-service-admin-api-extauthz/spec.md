# Purpose

Protect the hook-service admin API endpoints with Istio external authorization (`extAuthz`) delegating to Authorization Service, replacing local JWT authentication middleware. Authorization Service handles authentication (verifying the session cookie via STS exchange) and authorization (evaluating `role:admin#assignee` in OpenFGA per `rules.yaml`).

Key decisions:
- Disable local JWT authentication on admin routes by setting `AUTHENTICATION_ENABLED=false`. Authorization Service extAuthz handles authentication and forwards the cryptographically verified `Authorization: Bearer <jwt>` header to hook-service.
- The token hook endpoint (`POST /api/v0/hook/hydra`) is excluded from extAuthz enforcement — it continues using its existing `API_TOKEN` Bearer token middleware.
- Metrics (`/api/v0/metrics`) and status (`/api/v0/status`) endpoints are excluded from extAuthz enforcement.
- Istio AuthorizationPolicy is configured to apply extAuthz only to admin API paths (`/api/v0/authz/*`).
- An EnvoyFilter registers Authorization Service as the extAuthz provider for the hook-service sidecar.
- Caller identity is extracted exclusively from the forwarded `Authorization: Bearer <jwt>` header; custom identity headers are ignored.

Non-goals:
- This spec does not define route rules in Authorization Service (see `authorization-service-model-onboarding`).
- This spec does not define Kafka producer infrastructure (see `authorization-service-kafka-permission-pipeline`).
- This spec does not cover Istio mesh deployment — AuthorizationPolicy and EnvoyFilter are managed by platform operators.

## ADDED Requirements

### Requirement: Admin API endpoints protected by Authorization Service extAuthz
The system SHALL route all admin API requests through Istio's extAuthz filter, delegating authorization decisions to Authorization Service.

#### Scenario: Authenticated admin requests pass through
- **WHEN** an authenticated user holding `role:admin` makes a request to an admin API endpoint
- **THEN** Authorization Service extAuthz verifies the session via STS exchange, checks `role:admin#assignee` in OpenFGA, and returns allow
- **THEN** Istio forwards the request to hook-service with the `Authorization: Bearer <jwt>` header intact

#### Scenario: Unauthenticated requests are denied
- **WHEN** a request to an admin API endpoint lacks a valid session cookie
- **THEN** Authorization Service extAuthz returns 401 Unauthorized
- **THEN** Istio denies the request before it reaches hook-service

#### Scenario: Unauthorized requests are denied
- **WHEN** an authenticated user without the `role:admin` assignment makes a request to an admin API endpoint
- **THEN** Authorization Service extAuthz returns 403 Forbidden
- **THEN** Istio denies the request before it reaches hook-service

### Requirement: Token hook, metrics, and status endpoints excluded from extAuthz
The token hook endpoint, metrics endpoint, and status endpoint SHALL NOT be protected by Authorization Service extAuthz.

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
The JWT authentication middleware SHALL be disabled on admin routes by setting `AUTHENTICATION_ENABLED=false`, with Authorization Service extAuthz handling authentication and authorization.

#### Scenario: Admin routes have no JWT middleware
- **WHEN** hook-service starts with `AUTHENTICATION_ENABLED=false`
- **THEN** the admin router does not include the `authentication.Middleware.Authenticate()` handler
- **THEN** hook-service trusts the `Authorization` header forwarded by Istio without re-verifying the JWT

### Requirement: Caller identity extracted strictly from Authorization Bearer token
The system SHALL extract caller identity (user ID and tenant) exclusively from the cryptographically verified `Authorization: Bearer <jwt>` injected by Istio/Envoy after STS session exchange. The system SHALL NOT trust or extract identity from caller-supplied custom identity headers.

#### Scenario: Custom identity headers are ignored
- **WHEN** a client request includes custom identity headers such as `X-User-Id`
- **THEN** hook-service ignores these headers and extracts user identity solely from the JWT in the `Authorization` header
- **THEN** user impersonation attacks via header injection are prevented

#### Scenario: Direct forged Bearer tokens without session are rejected
- **WHEN** an unauthenticated request attempts to bypass Istio extAuthz by providing a direct `Authorization: Bearer <token>` without a valid session cookie
- **THEN** Istio extAuthz denies the request with `401 Unauthorized` before reaching hook-service