<!--
// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only
-->

# Centralized Authorization Architecture & End-to-End Verification Report

This document provides the authoritative architectural specification and the verified end-to-end (E2E) testing report for the **Canonical Centralized Authorization Platform**. It integrates four foundational systems:
1. **Istio Ingress Gateway & Ambient Waypoint Proxy** (Envoy External Authorization)
2. **Secure Token Service (STS - Janus)** (Phantom Token Pattern & JWT Issuance)
3. **Authorization Service** (Policy Decision Point, Route Matcher & Async OpenFGA Sync)
4. **Hook Service** (Identity Platform Hook & Groups Resource Server)

---

## 1. Executive Summary & The Big Picture

The Canonical Identity and Access Management (IAM) platform enforces a **zero-trust, fail-closed, centralized authorization model**. In this architecture:
- Microservices **do not manage authorization policies locally**. Instead, authorization logic is centralized in **OpenFGA** and orchestrated by **Authorization Service**.
- Microservices **do not handle raw user credentials or opaque external session cookies**. Instead, the **Phantom Token Pattern** is implemented by **Janus (Secure Token Service)** at the network edge.
- Network routing and policy enforcement are decoupled from service logic: **Istio Gateway / Waypoint Proxies** intercept incoming L7 traffic and query Authorization Service via Envoy's standard `ext_authz` gRPC interface before traffic ever reaches the target service.
- State changes within microservices (such as creating groups or modifying memberships) are decoupled from centralized authorization writes via **Apache Kafka** event streaming, ensuring high availability and fault isolation.

```
       +---------------------------------------------------------------------------------------------------+
       |                                          EXTERNAL ZONE                                            |
       |  Browser / CLI / External Client  (Holds Opaque Session Cookie: session=<cookie_value>)           |
       +---------------------------------------------------------------------------------------------------+
                                                      |
                                      HTTP Request with Session Cookie
                                                      v
+==================================================================================================================+
|  INGRESS & SERVICE MESH TIER (Istio Ambient Mode / Envoy Gateway)                                                |
|                                                                                                                  |
|  [Istio Ingress Gateway / Waypoint Proxy]                                                                        |
|      * Intercepts HTTP request using Envoy ext_authz filter                                                      |
|      * Pauses request processing and dispatches gRPC CheckRequest to Authorization Service                       |
+==================================================================================================================+
                                    |                                         ^
              gRPC CheckRequest     |                                         |  CheckResponse:
      (Path, Method, Cookie Header) |                                         |  - OK (Injects Bearer JWT)
                                    v                                         |  - DENIED (401/403)
+==================================================================================================================+
|  CENTRALIZED POLICY DECISION POINT (Authorization Service)                                                       |
|                                                                                                                  |
|  1. Session Swap:                                   2. Policy Evaluation:                                        |
|     Calls STS ExchangeSession(cookie)                  Matches Request to Route Rules in PostgreSQL              |
|     Validates returned JWT against STS JWKS            Evaluates Relation Tuples in OpenFGA                      |
+==================================================================================================================+
          |                               |                                 |
          | gRPC ExchangeSession          | HTTP GET JWKS                   | BatchCheck (with Tenant Context)
          v                               v                                 v
+--------------------------+   +----------------------+   +------------------------------------+
| Secure Token Service     |   | Secure Token Service |   | OpenFGA Engine                     |
| (STS - Janus) :9090      |   | (Janus) :8080        |   | (:8082 HTTP / :8081 gRPC)          |
|                          |   |                      |   |                                    |
| * Reads Valkey session   |   | * Serves public keys |   | * Stores compiled authz model      |
| * Mints internal ES256   |   |   at /.well-known/   |   | * Evaluates relations &            |
|   signed JWT             |   |   jwks.json          |   |   ABAC tenant_match conditions     |
+--------------------------+   +----------------------+   +------------------------------------+
          |
          +==============================================+
                                                         | (If OpenFGA check succeeds:
                                                         |  Envoy injects Authorization: Bearer <JWT>
                                                         v  and forwards request upstream)
+==================================================================================================================+
|  MICROSERVICE RESOURCE TIER (Hook Service)                                                                       |
|                                                                                                                  |
|  [Hook Service Admin API] (:8000 HTTP / :9095 gRPC)                                                              |
|      * Receives forwarded request with Authorization: Bearer <JWT>                                               |
|      * UserContextMiddleware extracts authenticated Subject (sub) and Tenant (org)                               |
|      * Executes local business logic and updates PostgreSQL groups database                                      |
|      * Standby Producer: PermissionPublisher is initialized and wired in memory for future event streaming       |
+==================================================================================================================+
                                                      |
                                      (Standby Kafka Producer:
                                       Ready for future streaming)
                                                      v
                                            [Kafka Broker :9092]
                                          (hook-service.permissions)
```

---

## 2. Core Architectural Pillars

### Pillar 1: Istio Ingress Gateway & Ambient Waypoint Proxy
- **Role**: Policy Enforcement Point (PEP) at the network perimeter and service mesh boundary.
- **Ambient Mode Architecture**: In Istio ambient mode, L4 traffic is handled by node-level `ztunnel` proxies, while L7 processing is handled by dedicated **Waypoint Proxies** (HBONE protocol on port `15008`).
- **Envoy `ext_authz` Integration**:
  - Waypoint proxies are configured with an Istio `AuthorizationPolicy` with `action: CUSTOM`.
  - The custom provider references `authorization-service` configured in Istio's `meshConfig.extensionProviders`.
  - On protected routes (such as `/api/v0/authz/groups/*`), Envoy suspends the client request and transmits a gRPC `CheckRequest` (`/envoy.service.auth.v3.Authorization/Check`) containing all HTTP headers, cookies, method, and URI path to Authorization Service on port `9091`.
  - When Authorization Service approves the request with `OkResponse`, Envoy merges the injected headers (`Authorization: Bearer <internal_jwt>`) into the upstream request and dispatches it to the destination workload.

### Pillar 2: Secure Token Service (STS - Janus)
- **Role**: Phantom Token translator and internal JWT authority.
- **The Phantom Token Pattern**:
  - External users interact with browsers or API clients holding an **opaque, encrypted session cookie** (`session=<cookie_value>`). External tokens never expose internal claims or infrastructure topologies to the public internet.
  - STS stores session metadata (user identity, original OIDC tokens, expiration) in a **Valkey / Redis** cluster.
  - STS manages asymmetric ECDSA (P-256) signing keys stored in PostgreSQL (`hydra_jwk` table), supporting zero-downtime key rotation via `./bin/sts rotate-key`.
- **Interfaces**:
  - **HTTP (`:8080`)**: Exposes public JSON Web Key Set at `/.well-known/jwks.json`, and OIDC lifecycle endpoints (`/auth/login`, `/auth/callback`, `/auth/logout`).
  - **gRPC (`:9090`)**: Exposes `SecurityTokenService.ExchangeSession`, swapping an opaque session cookie for an internal signed JWT containing `sub` (user identity) and `org` (tenant).

### Pillar 3: Authorization Service
- **Role**: Centralized Policy Decision Point (PDP) and Kafka permission reconciler.
- **Operational Subsystems**:
  1. **extAuthz Server (`bin/app serve`)**:
     - Listens on gRPC `:9091` and REST `:8070`.
     - Validates the incoming session cookie with STS via gRPC `ExchangeSession`.
     - Verifies the minted JWT against STS's public JWKS endpoint.
     - Resolves the requested HTTP path and method against rules stored in PostgreSQL (`authorization_rule`).
     - Performs OpenFGA `BatchCheck` queries against OpenFGA (`:8082`), verifying user relations while enforcing multi-tenant isolation via the `tenant_match` condition.
     - Returns gRPC `CheckResponse` to Envoy, injecting `Authorization: Bearer <JWT>`.
  2. **Kafka Listener Daemon (`bin/app listen`)** & **Async Worker Daemon (`bin/app worker`)**:
     - Consumes protobuf messages from `<slug>.permissions` Kafka topics and reconciles OpenFGA tuples.

### Pillar 4: Hook Service
- **Role**: Microservice resource server and Canonical Identity Platform Hydra token hook.
- **Admin APIs**:
  - Exposes Group Management endpoints under `/api/v0/authz/groups/*`.
  - Operates behind the Istio Gateway / Envoy proxy, guarded by coarse-grained RBAC requiring `role:admin#assignee`.
- **JWT Middleware**:
  - In production / authenticated mesh mode: `pkg/authentication/middleware.go` validates the forwarded `Authorization: Bearer <jwt>` against STS JWKS and populates the request context with caller identity.
  - In trusted proxy mode: `UserContextMiddleware` extracts caller identity from the `Authorization: Bearer <jwt>` header.
- **Standby Kafka Producer Architecture**:
  - Hook Service initializes a Kafka publisher (`internal/kafka/producer.go`) wired into `groups.Service` in standby mode. This preserves event streaming infrastructure ready for future asynchronous permission reconciliation while current CRUD operations execute directly against PostgreSQL.

---

## 3. Detailed Request Lifecycle: Step-by-Step

### 3.1 Complete End-to-End Flow Diagram

```mermaid
sequenceDiagram
    autonumber
    actor Client as External Client (Browser/API)
    participant Istio as Istio Gateway / Waypoint Proxy
    participant AuthzService as Authorization Service extAuthz (:9091)
    participant STS as STS (Janus :9090 / :8080)
    participant FGA as OpenFGA Engine (:8082)
    participant Hook as Hook Service (:8000)

    Note over Client,Istio: Phase 1: Ingress Interception
    Client->>Istio: HTTP POST /api/v0/authz/groups (Cookie: session=<cookie_val>)
    Istio->>AuthzService: gRPC CheckRequest (Path, Method, Headers, Cookies)

    Note over AuthzService,STS: Phase 2: Phantom Token Translation
    AuthzService->>STS: gRPC ExchangeSession(session_cookie)
    STS-->>AuthzService: ExchangeResponse(access_token = JWT)
    AuthzService->>STS: GET /.well-known/jwks.json (Cached JWKS Verification)
    Note over AuthzService: Extract Claims: sub="domain-admin@canonical.com", org="hook-service"

    Note over AuthzService,FGA: Phase 3: Route Rule Resolution & OpenFGA Check
    AuthzService->>AuthzService: Match Route POST /api/v0/authz/groups -> assignee on role:admin
    AuthzService->>FGA: BatchCheck(user:domain-admin@canonical.com, assignee, role:admin, context={tenant_match})
    FGA-->>AuthzService: Allowed = true

    Note over AuthzService,Hook: Phase 4: Forwarding with Injected Bearer Token
    AuthzService-->>Istio: gRPC CheckResponse (OK, Header: Authorization=Bearer <JWT>)
    Istio->>Hook: Forwarded HTTP POST /api/v0/authz/groups (with Authorization: Bearer <JWT>)
    
    Note over Hook: Phase 5: Resource Execution
    Hook->>Hook: UserContextMiddleware extracts sub="domain-admin@canonical.com"
    Hook->>Hook: Insert group in PostgreSQL
    Hook-->>Istio: HTTP 200 OK {"status": 200, "message": "Group created"}
    Istio-->>Client: HTTP 200 OK {"status": 200, "message": "Group created"}
```

### 3.2 Protocol Steps Breakdown

| Step | Initiator | Receiver | Protocol | Payload / Operation |
|:---:|---|---|:---:|---|
| **1** | External Client | Istio Gateway | HTTP/1.1 or HTTP/2 | `POST /api/v0/authz/groups` with `Cookie: session=<opaque_cookie>` |
| **2** | Istio Waypoint | Authorization Service | gRPC (`ext_authz.v3`) | `CheckRequest` containing headers, cookie, method `POST`, path `/api/v0/authz/groups` |
| **3** | Authorization Service | STS (Janus) | gRPC (`sts.v1`) | `ExchangeSessionRequest{SessionCookie: "<opaque_cookie>"}` on port `9090` |
| **4** | STS | Authorization Service | gRPC (`sts.v1`) | `ExchangeSessionResponse{AccessToken: "<signed_jwt>", ExpiresIn: 3600}` |
| **5** | Authorization Service | STS (Janus) | HTTP (`GET`) | Fetches `/.well-known/jwks.json` on port `8080` (cached with TTL) to verify signature |
| **6** | Authorization Service | OpenFGA | HTTP (`POST`) | `POST /stores/{id}/check` for `user:<subId> -> assignee -> role:admin` with `tenant_match` |
| **7** | Authorization Service | Istio Waypoint | gRPC (`ext_authz.v3`) | `CheckResponse{Status: OK, Headers: [Authorization: Bearer <JWT>]}` |
| **8** | Istio Waypoint | Hook Service | HTTP/1.1 | Proxies request upstream with injected `Authorization: Bearer <JWT>` header |
| **9** | Hook Service | Database | PostgreSQL TCP | Inserts group into `groups` table |
| **10** | Hook Service | External Client | HTTP/1.1 | Returns `{"status": 200, "message": "Group created"}` via Istio |

---

## 4. Network, Port, and Credential Allocation Matrix

### 4.1 Service Port & Network Protocol Allocation

| Service / Container | Process | Listen Port | Protocol | Purpose / URL |
|---|---|:---:|:---:|---|
| **Istio Ingress / Gateway** | Envoy | `80` / `443` / `10000` | HTTP / HTTPS | Public entry point for external client requests |
| **Istio Waypoint Proxy** | Envoy (Ambient) | `15008` | HBONE / HTTP | L7 Envoy proxy enforcing `AuthorizationPolicy` |
| **Secure Token Service (STS)** | `sts serve` | `8080` | HTTP | JWKS (`/.well-known/jwks.json`), OIDC login/callbacks |
| **Secure Token Service (STS)** | `sts serve` | `9090` | gRPC | `SecurityTokenService.ExchangeSession` |
| **Authorization Service extAuthz Server** | `app serve` | `9091` | gRPC | Envoy `envoy.service.auth.v3.Authorization/Check` |
| **Authorization Service REST Gateway** | `app serve` | `8070` | HTTP | Admin / Management REST API |
| **Authorization Service Metrics (Serve)** | `app serve` | `9100` | HTTP | Prometheus metrics for extAuthz server |
| **Authorization Service Kafka Listener** | `app listen` | `9101` | HTTP (Metrics) | Kafka consumer metrics & liveness |
| **Authorization Service Async Worker** | `app worker` | `9102` | HTTP (Metrics) | Worker batch queue metrics & liveness |
| **Hook Service** | `app serve` | `8000` | HTTP | Group Admin REST API (`/api/v0/authz/groups`) |
| **Hook Service Internal gRPC** | `app serve` | `9095` | gRPC | Groups mapping streaming gRPC API |
| **OpenFGA Engine** | `openfga run` | `8082` (host) / `8080` (ctr) | HTTP | OpenFGA API (`/stores/{id}/check`, `/write`, `/read`) |
| **OpenFGA gRPC** | `openfga run` | `8081` (host) / `8081` (ctr) | gRPC | OpenFGA high-performance gRPC endpoint |
| **Kafka Broker** | `kafka` | `9092` | TCP | Event streaming topic: `hook-service.permissions` |
| **PostgreSQL Database** | `postgres:16` | `5433` (host) / `5432` (ctr) | TCP | Multi-DB host: `authorization-service`, `groups`, `sts` |
| **Valkey / Redis Cache** | `valkey:8` | `6380` (host) / `6379` (ctr) | TCP | Session store for STS / Janus |

### 4.2 Credentials & Secrets Allocation Matrix

| System / Component | Credential / Secret | Environment Variable | Default / Test Value | Scope & Purpose |
|---|---|---|---|---|
| **PostgreSQL (openfga)** | DB Superuser Password | `POSTGRES_PASSWORD` | `password` | Superuser access to `openfga` database |
| **PostgreSQL (authz)** | Authz Service DB Password | `POSTGRES_PASSWORD` | `password` | Access to `authorization-service` and `sts` databases |
| **PostgreSQL (groups)** | Hook Service DB Password | `DSN` | `groups` | Access to `groups` database |
| **OpenFGA Engine** | Preshared API Token | `OPENFGA_API_KEY` | `42` | Bearer token authenticating API calls to OpenFGA |
| **STS (Janus)** | Cookie Hash Secret Key | `COOKIE_HASH_KEY` | `01234567890123456789...` (64-hex) | AES/HMAC session cookie integrity key |
| **STS (Janus)** | Mock OIDC Client Secret | `OIDC_CLIENT_SECRET` | `dummy` | Authenticates STS to upstream mock OIDC provider |
| **Valkey / Redis** | Cache Password | `CACHE_ADDR` | *(unauthenticated)* | Local test cache for STS session storage |
| **Kafka Broker** | SASL / SSL Credentials | `KAFKA_BROKERS` | *(plaintext)* | Local test event broker communication |

---

## 5. End-to-End Test Plan

This test plan defines the complete verification strategy for validating the integrated Centralized Authorization platform across all four pillars.

### 5.1 Test Environment Architecture & Pre-requisites

```text
                   +---------------------------------------------+
                   |             Docker Infrastructure           |
                   |  - authz-postgres (:5433)                   |
                   |  - authz-valkey   (:6380)                   |
                   |  - authz-kafka    (:9092)                   |
                   |  - authz-openfga  (:8082 / :8081)           |
                   |  - envoy-gateway  (:10000 -> ext_authz)     |
                   +---------------------------------------------+
                                         ^
                                         |
     +-------------------+---------------+-------------------+-------------------+
     |                   |                                   |                   |
     v                   v                                   v                   v
[STS Janus]      [Authorization Service extAuthz]    [Authorization Service Pipeline] [Hook Service]
HTTP :8080       gRPC :9091                          Listener :9101       HTTP :8000
gRPC :9090       REST :8070                          Worker   :9102       gRPC :9095
```

### 5.2 Stage-by-Stage Bootstrapping & Setup

#### Stage 1: Infrastructure Provisioning
1. Launch dependencies using Docker Compose:
   ```bash
   VALKEY_PORT=6380 docker compose -p dependencies -f docker/centralized-authz/docker-compose.yml up -d
   ```
2. Initialize Databases in `authz-postgres`:
   ```bash
   # Groups DB for Hook Service
   docker exec -i authz-postgres psql -U postgres -c "CREATE ROLE groups WITH LOGIN PASSWORD 'groups';"
   docker exec -i authz-postgres psql -U postgres -c "CREATE DATABASE groups OWNER groups;"
   
   # STS DB for Janus JWKS
   docker exec -i authz-postgres psql -U postgres -c "CREATE DATABASE sts OWNER \"authorization-service\";"
   ```

#### Stage 2: STS (Janus) Initialization
1. Run migrations for STS:
   ```bash
   DATABASE_URL="postgres://authorization-service:password@localhost:5433/sts?sslmode=disable" \
   ./bin/sts migrate
   ```
2. Generate initial asymmetric signing key:
   ```bash
   DATABASE_URL="postgres://authorization-service:password@localhost:5433/sts?sslmode=disable" \
   ./bin/sts rotate-key
   ```
3. Start STS server daemon:
   ```bash
   HTTP_PORT="8080" \
   GRPC_PORT="9090" \
   CACHE_ADDR="localhost:6380" \
   DATABASE_URL="postgres://authorization-service:password@localhost:5433/sts?sslmode=disable" \
   COOKIE_HASH_KEY="0123456789012345678901234567890123456789012345678901234567890123" \
   JWT_ISSUER="session-service" \
   JWT_AUDIENCE="internal-services" \
   ./bin/sts serve
   ```
4. Verify JWKS endpoint:
   ```bash
   curl -s http://localhost:8080/.well-known/jwks.json | jq .
   ```

#### Stage 3: Authorization Service Bootstrapping
1. Run Authorization Service PostgreSQL migrations:
   ```bash
   POSTGRES_PORT=5433 ./bin/app migrate
   ```
2. Ensure core OpenFGA model is written to the store:
   ```bash
   ./bin/app authz model write --store-id 01GP1254CHWJC1MNGVB0WDG1T0
   ```
3. Seed Hook Service route rules:
   ```bash
   POSTGRES_PORT=5433 ./bin/app seed -s services/hook-service/rules.yaml
   ```
4. Seed initial Domain Admin tuple (`role:admin#assignee`):
   ```bash
   curl -s -X POST http://localhost:8082/stores/01GP1254CHWJC1MNGVB0WDG1T0/write \
     -H "Content-Type: application/json" \
     -H "Authorization: Bearer 42" \
     -d '{
       "writes": {
         "tuple_keys": [
           {
             "user": "user:domain-admin@canonical.com",
             "relation": "assignee",
             "object": "role:admin",
             "condition": { "name": "tenant_match", "context": { "tenant": "hook-service" } }
           }
         ]
       }
     }'
   ```
5. Start Authorization Service Daemons:
   - **Kafka Listener**: `bin/app listen` on METRICS_PORT=9101
   - **Async Worker**: `bin/app worker` on METRICS_PORT=9102
   - **extAuthz Server**: `bin/app serve` on SERVER_GRPC_PORT=9091, STS_ADDRESS="localhost:9090", EXTAUTHZ_JWK_SET_URL="http://localhost:8080/.well-known/jwks.json"

#### Stage 4: Hook Service Bootstrapping
1. Run migrations for `groups` database:
   ```bash
   DSN="postgres://groups:groups@localhost:5433/groups?sslmode=disable" ./app migrate
   ```
2. Start Hook Service:
   ```bash
   PORT="8000" \
   GRPC_PORT="9095" \
   AUTHENTICATION_ENABLED="false" \
   KAFKA_BROKERS="localhost:9092" \
   DSN="postgres://groups:groups@localhost:5433/groups?sslmode=disable" \
   SALESFORCE_ENABLED="false" \
   AUTHORIZATION_ENABLED="false" \
   ./app serve
   ```

#### Stage 5: Istio / Envoy ExtAuthz Gateway Setup
1. Verify Envoy Ingress Gateway container is running and healthy:
   ```bash
   docker ps --filter "name=authz-envoy"
   ```
2. Verify routing and connectivity to extAuthz filter:
   ```bash
   curl -s -o /dev/null -w "%{http_code}\n" http://localhost:10000/api/v0/authz/groups
   # Expected response: 403 (or 401 when no session cookie is present)
   ```
   Envoy listens on port `10000`, intercepts incoming `/api/v0/authz/*` requests, dispatches `CheckRequest` to Authorization Service on port `9091`, and proxies authorized requests upstream to Hook Service on port `8000`.

---

## 6. Comprehensive Hook Service Admin API Permission Control Specification

To ensure robust defense-in-depth across the platform, Hook Service admin endpoints are guarded by a combination of:
1. **Edge Authentication (STS & Envoy)**: Requires a valid opaque session cookie (`session_id=<token>`) exchanged for an internal signed JWT.
2. **Centralized Policy Evaluation (Authorization Service & OpenFGA)**: Every API route under `/api/v0/authz/*` is matched against declarative rules in Authorization Service and evaluated as `role:admin#assignee` in OpenFGA with ABAC conditions (`tenant_match`).
3. **Standby Kafka Producer Architecture**: Hook Service initializes the Kafka publisher in memory for future event streaming while current group operations execute directly against PostgreSQL.

### 6.1 Multi-Persona Model

The E2E testing framework establishes five distinct personas to rigorously verify privilege separation, perimeter bounds, and defense against privilege escalation:

| Persona Name | Identity / Subject (`sub`) | Roles & OpenFGA Relations | Description |
|---|---|---|---|
| **Domain Admin** | `domain-admin@canonical.com` | `assignee` on `role:admin` | Platform administrator. Can list all system groups, create groups, read/update any group, manage group members, query user groups, and delete groups. |
| **Alice** | `alice@canonical.com` | None (Non-Admin) | Authenticated user without `role:admin`. Rejected with `HTTP 403 Forbidden` across all admin routes. |
| **Bob** | `bob@canonical.com` | None (Non-Admin) | Authenticated user without `role:admin`. Rejected with `HTTP 403 Forbidden` across all admin routes. |
| **Charlie** | `charlie@canonical.com` | None (Non-Admin) | Authenticated user without `role:admin`. Rejected with `HTTP 403 Forbidden` across all admin routes. |
| **Mallory** | `mallory@canonical.com` | None (Non-Admin) | Authenticated user without `role:admin`. Rejected with `HTTP 403 Forbidden` across all admin routes. |

---

### 6.2 Exhaustive Route & Permission Protection Matrix

The table below details the exact mapping between Hook Service Admin API endpoints, the Authorization Service protection rule, the required OpenFGA relation, and the expected HTTP status code for each persona:

| HTTP Method | API Route | Authorization Service Required Tuple | Domain Admin | Alice (Non-Admin) | Bob (Non-Admin) | Charlie (Non-Admin) | Mallory (Non-Admin) |
|:---:|---|---|:---:|:---:|:---:|:---:|:---:|
| `GET` | `/api/v0/authz/groups` | `user -> assignee -> role:admin` | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden | 403 Forbidden |
| `POST` | `/api/v0/authz/groups` | `user -> assignee -> role:admin` | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden | 403 Forbidden |
| `GET` | `/api/v0/authz/groups/{id}` | `user -> assignee -> role:admin` | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden | 403 Forbidden |
| `PUT` | `/api/v0/authz/groups/{id}` | `user -> assignee -> role:admin` | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden | 403 Forbidden |
| `DELETE` | `/api/v0/authz/groups/{id}` | `user -> assignee -> role:admin` | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden | 403 Forbidden |
| `GET` | `/api/v0/authz/groups/{id}/users` | `user -> assignee -> role:admin` | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden | 403 Forbidden |
| `POST` | `/api/v0/authz/groups/{id}/users` | `user -> assignee -> role:admin` | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden | 403 Forbidden |
| `DELETE` | `/api/v0/authz/groups/{id}/users/{uid}` | `user -> assignee -> role:admin` | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden | 403 Forbidden |
| `GET` | `/api/v0/authz/users/{uid}/groups` | `user -> assignee -> role:admin` | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden | 403 Forbidden |
| `PUT` | `/api/v0/authz/users/{uid}/groups` | `user -> assignee -> role:admin` | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden | 403 Forbidden |

---

### 6.3 Detailed End-to-End Test Execution Phases

The test suite executes 49 distinct assertions across 8 structured phases:

#### Phase 1: Edge Perimeter & Authentication Defense
- **TC-AUTH-01**: Missing session cookie in request header is immediately rejected at the Envoy edge with `HTTP 401 Unauthorized`.
- **TC-AUTH-02**: Tampered or corrupted session cookie is rejected during STS session exchange, causing Envoy to return `HTTP 403 Forbidden`.
- **TC-AUTH-03**: Probing non-existent UUIDs (`00000000-0000-0000-0000-000000000000`) is rejected by OpenFGA with `HTTP 403 Forbidden` for non-admin callers without hitting the microservice.
- **TC-AUTH-04**: Custom header spoofing attempt: Mallory sends valid session cookie plus `X-User-Id: domain-admin@canonical.com`. Edge gateway and Authorization Service evaluate identity strictly from the authenticated session, completely ignoring caller-supplied custom identity headers, resulting in `HTTP 403 Forbidden`.
- **TC-AUTH-05**: Unauthenticated request attempting to supply `X-User-Id: domain-admin@canonical.com` without a session cookie is rejected at the Envoy edge with `HTTP 401 Unauthorized`.
- **TC-AUTH-06**: Direct forged Bearer token (`Authorization: Bearer <fake>`) without a session cookie is rejected at the edge with `HTTP 401 Unauthorized` (Phantom Token pattern strictly enforced).
- **TC-AUTH-07**: Mallory sends valid session cookie plus forged admin Bearer header (`Authorization: Bearer <fake-admin>`). Envoy overwrites upstream authorization with the cryptographically minted internal JWT from STS, preventing privilege escalation (`HTTP 403 Forbidden`).

#### Phase 2: Coarse-Grained RBAC & Endpoint Enforcement
- **TC-RBAC-01**: Domain Admin queries `GET /api/v0/authz/groups` and receives `HTTP 200 OK` with the complete list of system groups.
- **TC-RBAC-02 - TC-RBAC-04**: Alice, Bob, and Mallory attempting to list system groups are all blocked with `HTTP 403 Forbidden`.
- **TC-RBAC-05 - TC-RBAC-06**: Alice and Mallory attempting `POST /api/v0/authz/groups` are blocked with `HTTP 403 Forbidden`.

#### Phase 3: Administrative Group Lifecycle & CRUD Operations
- Domain Admin creates Group Alpha (`test-group-alpha`) and Group Beta (`test-group-beta`).
- **TC-GRP-01**: Domain Admin reads Group Alpha details (`HTTP 200 OK`).
- **TC-GRP-02 - TC-GRP-04**: Alice, Bob, and Mallory attempting to read Group Alpha are blocked with `HTTP 403 Forbidden`.
- **TC-GRP-05**: Domain Admin reads Group Beta details (`HTTP 200 OK`).
- **TC-GRP-06**: Charlie attempting to read Group Beta is blocked with `HTTP 403 Forbidden`.
- **TC-GRP-07**: Domain Admin updates Group Alpha description via `PUT /api/v0/authz/groups/{alpha_id}` (`HTTP 200 OK`).
- **TC-GRP-08 - TC-GRP-10**: Alice, Bob, and Mallory attempting to update Group Alpha are all rejected with `HTTP 403 Forbidden`.

#### Phase 4: Administrative Membership Management
- **TC-MEM-01**: Domain Admin retrieves member list for Group Alpha (`HTTP 200 OK`).
- **TC-MEM-02 - TC-MEM-04**: Alice, Bob, and Mallory attempting to list members are rejected with `HTTP 403 Forbidden`.
- **TC-MEM-05 - TC-MEM-07**: Alice, Bob, and Mallory attempting to add users to Alpha are blocked with `HTTP 403 Forbidden`.
- **TC-MEM-08**: Domain Admin adds Bob and Dave to Group Alpha (`HTTP 200 OK`).
- **TC-MEM-09**: Idempotent member addition: Domain Admin adds Bob again, returning `HTTP 200 OK`.
- **TC-MEM-10 - TC-MEM-12**: Alice, Bob, and Mallory attempting to remove Dave from Alpha are rejected with `HTTP 403 Forbidden`.
- **TC-MEM-13**: Domain Admin removes Dave from Group Alpha (`HTTP 200 OK`).
- **TC-MEM-14**: Idempotent member deletion: Domain Admin removes non-existent user, returning `HTTP 200 OK`.

#### Phase 5: Multi-Tenant ABAC Isolation Check
- **TC-TENANT-01**: OpenFGA direct check succeeds when request context includes matching tenant (`context: {"tenant_enabled": true, "user_tenant": "hook-service"}` -> `allowed: true`).
- **TC-TENANT-02**: OpenFGA direct check is denied when request context includes an alien tenant (`context: {"tenant_enabled": true, "user_tenant": "alien-tenant"}` -> `allowed: false`).

#### Phase 6: Session Eviction & Revocation Defense
- **TC-SESS-01**: An ephemeral session cookie is minted, verified, and then explicitly evicted from the Valkey session cache (`valkey-cli del "session:<id>"`). The subsequent request through Envoy is rejected by STS/Authorization Service with `HTTP 403 Forbidden`, confirming instant session revocation.

#### Phase 7: Global User Group Queries
- **TC-USR-01**: Domain Admin queries `GET /api/v0/authz/users/alice@canonical.com/groups` (`HTTP 200 OK`).
- **TC-USR-02 - TC-USR-03**: Non-admin users Alice and Mallory attempting user group inspection are blocked with `HTTP 403 Forbidden`.

#### Phase 8: Group Deletion Security & Cleanup
- **TC-DEL-01 - TC-DEL-04**: Non-admin users Alice, Bob, Charlie, and Mallory attempting group deletion are rejected with `HTTP 403 Forbidden`.
- **TC-DEL-05 - TC-DEL-06**: Domain Admin executes `DELETE /api/v0/authz/groups/{alpha_id}` and `DELETE /api/v0/authz/groups/{beta_id}` (`HTTP 200 OK`).

---

### 6.4 Automated Test Environment & Execution

The repository provides a complete, reproducible environment setup and test harness located under `docker/centralized-authz/` and `scripts/`:

| Component / Script | Path | Purpose |
|---|---|---|
| **Docker Compose** | [`docker/centralized-authz/docker-compose.yml`](file:///home/shu.du@canonical.com/workspaces/canonical/sync/hook-service/docker/centralized-authz/docker-compose.yml) | Postgres, OpenFGA, Valkey, Kafka, and Envoy infrastructure |
| **Envoy Configuration** | [`docker/centralized-authz/envoy.yaml`](file:///home/shu.du@canonical.com/workspaces/canonical/sync/hook-service/docker/centralized-authz/envoy.yaml) | Envoy proxy routing & `ext_authz` filter wiring |
| **Postgres Multi-DB Init** | [`docker/centralized-authz/postgres/init-scripts/01-init-dbs.sql`](file:///home/shu.du@canonical.com/workspaces/canonical/sync/hook-service/docker/centralized-authz/postgres/init-scripts/01-init-dbs.sql) | Pre-creates `openfga`, `authorization-service`, `sts`, and `groups` databases |
| **Mock OIDC Server** | [`scripts/oidc_mock.py`](file:///home/shu.du@canonical.com/workspaces/canonical/sync/hook-service/scripts/oidc_mock.py) | Lightweight Python OIDC discovery & JWKS server on `:8888` |
| **Setup Script** | [`scripts/setup-centralized-authz-e2e.sh`](file:///home/shu.du@canonical.com/workspaces/canonical/sync/hook-service/scripts/setup-centralized-authz-e2e.sh) | Automated build, migrations, models, cache flushes, and daemon orchestration |
| **Teardown Script** | [`scripts/teardown-centralized-authz-e2e.sh`](file:///home/shu.du@canonical.com/workspaces/canonical/sync/hook-service/scripts/teardown-centralized-authz-e2e.sh) | Graceful daemon shutdown, PID tracking, and Docker volume wiping |
| **Test Suite Script** | [`scripts/test-centralized-authz-e2e.sh`](file:///home/shu.du@canonical.com/workspaces/canonical/sync/hook-service/scripts/test-centralized-authz-e2e.sh) | The 49-assertion test suite executing against Envoy `:10000` |
| **All-in-One Runner** | [`scripts/run-centralized-authz-e2e.sh`](file:///home/shu.du@canonical.com/workspaces/canonical/sync/hook-service/scripts/run-centralized-authz-e2e.sh) | Runs setup -> test -> teardown in a single command |

#### Quick Start via Makefile

```bash
# Option A: Run everything end-to-end (setup -> test -> teardown)
make authz-e2e

# Option B: Setup environment and leave it running for iterative debugging
make authz-setup

# Run the 49-assertion test suite repeatedly against the running mesh
make authz-test

# Teardown all background services and containers when finished
make authz-down
```

#### Running Directly via Scripts

```bash
# 1. Start all infrastructure and background daemons
./scripts/setup-centralized-authz-e2e.sh

# 2. Run the test suite repeatedly against the live mesh
./scripts/test-centralized-authz-e2e.sh

# 3. Teardown and wipe persistent volumes
./scripts/teardown-centralized-authz-e2e.sh
```

---

## 7. End-to-End Verification Test Report & Results Matrix

### 7.1 Reference Execution Summary

> [!NOTE]
> The summary below reflects the verified reference baseline execution. To observe live test execution results in your local or CI environment, execute [`scripts/test-centralized-authz-e2e.sh`](file:///home/shu.du@canonical.com/workspaces/canonical/sync/hook-service/scripts/test-centralized-authz-e2e.sh).

```text
==================================================================
                     E2E EXECUTION SUMMARY                        
==================================================================
Target Gateway (Envoy) : http://localhost:10000
OpenFGA PDP Engine     : http://localhost:8082
OpenFGA Store ID       : 01GP1254CHWJC1MNGVB0WDG1T0
Secure Token Service   : http://localhost:8080 (JWKS) / :9090 (gRPC)
Hook Service Resource  : http://localhost:8000
Total Tests Executed   : 49
Passed Tests           : 49
Failed Tests           : 0
Success Rate           : 100%
==================================================================
ALL SUITE TESTS PASSED!
```

---

### 7.2 Test Assertion Matrix & Baseline Verification Results

| Phase | Test ID | Target Endpoint | HTTP Verb | Caller Persona / Cookie | Expected Status / Condition | Verified Result |
| :--- | :--- | :--- | :---: | :--- | :---: | :---: |
| **Phase 1: Edge Perimeter & Authentication Defense** | **TC-AUTH-01** | `/api/v0/authz/groups` | `GET` | *(None / Missing Cookie)* | `401 Unauthorized` | **PASS** |
| | **TC-AUTH-02** | `/api/v0/authz/groups` | `GET` | Malformed Cookie (`bad-session`) | `403 Forbidden` | **PASS** |
| | **TC-AUTH-03** | `/api/v0/authz/groups/00000000-0000-0000-0000-000000000000` | `GET` | Authenticated Mallory | `403 Forbidden` | **PASS** |
| | **TC-AUTH-04** | `/api/v0/authz/groups` | `POST` | Mallory + `X-User-Id: domain-admin@canonical.com` | `403 Forbidden` | **PASS** |
| | **TC-AUTH-05** | `/api/v0/authz/groups` | `GET` | *(No Cookie)* + `X-User-Id: domain-admin@canonical.com` | `401 Unauthorized` | **PASS** |
| | **TC-AUTH-06** | `/api/v0/authz/groups` | `GET` | *(No Cookie)* + Direct Bearer JWT | `401 Unauthorized` | **PASS** |
| | **TC-AUTH-07** | `/api/v0/authz/groups` | `POST` | Mallory + Forged Admin Bearer | `403 Forbidden` | **PASS** |
| **Phase 2: Coarse-Grained RBAC & Endpoint Enforcement** | **TC-RBAC-01** | `/api/v0/authz/groups` | `GET` | Domain Admin | `200 OK` | **PASS** |
| | **TC-RBAC-02** | `/api/v0/authz/groups` | `GET` | Alice (Non-Admin) | `403 Forbidden` | **PASS** |
| | **TC-RBAC-03** | `/api/v0/authz/groups` | `GET` | Bob (Non-Admin) | `403 Forbidden` | **PASS** |
| | **TC-RBAC-04** | `/api/v0/authz/groups` | `GET` | Mallory (Non-Admin) | `403 Forbidden` | **PASS** |
| | **TC-RBAC-05** | `/api/v0/authz/groups` | `POST` | Alice (Non-Admin) | `403 Forbidden` | **PASS** |
| | **TC-RBAC-06** | `/api/v0/authz/groups` | `POST` | Mallory (Non-Admin) | `403 Forbidden` | **PASS** |
| **Phase 3: Group Lifecycle & CRUD Operations** | — | `/api/v0/authz/groups` | `POST` | Domain Admin creates Alpha & Beta | `200 OK` | **DONE** |
| | **TC-GRP-01** | `/api/v0/authz/groups/{alpha_id}` | `GET` | Domain Admin | `200 OK` | **PASS** |
| | **TC-GRP-02** | `/api/v0/authz/groups/{alpha_id}` | `GET` | Alice (Non-Admin) | `403 Forbidden` | **PASS** |
| | **TC-GRP-03** | `/api/v0/authz/groups/{alpha_id}` | `GET` | Bob (Non-Admin) | `403 Forbidden` | **PASS** |
| | **TC-GRP-04** | `/api/v0/authz/groups/{alpha_id}` | `GET` | Mallory (Non-Admin) | `403 Forbidden` | **PASS** |
| | **TC-GRP-05** | `/api/v0/authz/groups/{beta_id}` | `GET` | Domain Admin | `200 OK` | **PASS** |
| | **TC-GRP-06** | `/api/v0/authz/groups/{beta_id}` | `GET` | Charlie (Non-Admin) | `403 Forbidden` | **PASS** |
| | **TC-GRP-07** | `/api/v0/authz/groups/{alpha_id}` | `PUT` | Domain Admin | `200 OK` | **PASS** |
| | **TC-GRP-08** | `/api/v0/authz/groups/{alpha_id}` | `PUT` | Alice (Non-Admin) | `403 Forbidden` | **PASS** |
| | **TC-GRP-09** | `/api/v0/authz/groups/{alpha_id}` | `PUT` | Bob (Non-Admin) | `403 Forbidden` | **PASS** |
| | **TC-GRP-10** | `/api/v0/authz/groups/{alpha_id}` | `PUT` | Mallory (Non-Admin) | `403 Forbidden` | **PASS** |
| **Phase 4: Administrative Membership Management** | **TC-MEM-01** | `/api/v0/authz/groups/{alpha_id}/users` | `GET` | Domain Admin | `200 OK` | **PASS** |
| | **TC-MEM-02** | `/api/v0/authz/groups/{alpha_id}/users` | `GET` | Alice (Non-Admin) | `403 Forbidden` | **PASS** |
| | **TC-MEM-03** | `/api/v0/authz/groups/{alpha_id}/users` | `GET` | Bob (Non-Admin) | `403 Forbidden` | **PASS** |
| | **TC-MEM-04** | `/api/v0/authz/groups/{alpha_id}/users` | `GET` | Mallory (Non-Admin) | `403 Forbidden` | **PASS** |
| | **TC-MEM-05** | `/api/v0/authz/groups/{alpha_id}/users` | `POST` | Alice adds Dave | `403 Forbidden` | **PASS** |
| | **TC-MEM-06** | `/api/v0/authz/groups/{alpha_id}/users` | `POST` | Bob adds Dave | `403 Forbidden` | **PASS** |
| | **TC-MEM-07** | `/api/v0/authz/groups/{alpha_id}/users` | `POST` | Mallory adds Dave | `403 Forbidden` | **PASS** |
| | **TC-MEM-08** | `/api/v0/authz/groups/{alpha_id}/users` | `POST` | Domain Admin adds Bob & Dave | `200 OK` | **PASS** |
| | **TC-MEM-09** | `/api/v0/authz/groups/{alpha_id}/users` | `POST` | Domain Admin adds duplicate member | `200 OK` | **PASS** |
| | **TC-MEM-10** | `/api/v0/authz/groups/{alpha_id}/users/dave@canonical.com` | `DELETE` | Alice removes Dave | `403 Forbidden` | **PASS** |
| | **TC-MEM-11** | `/api/v0/authz/groups/{alpha_id}/users/dave@canonical.com` | `DELETE` | Bob removes Dave | `403 Forbidden` | **PASS** |
| | **TC-MEM-12** | `/api/v0/authz/groups/{alpha_id}/users/dave@canonical.com` | `DELETE` | Mallory removes Dave | `403 Forbidden` | **PASS** |
| | **TC-MEM-13** | `/api/v0/authz/groups/{alpha_id}/users/dave@canonical.com` | `DELETE` | Domain Admin removes Dave | `200 OK` | **PASS** |
| | **TC-MEM-14** | `/api/v0/authz/groups/{alpha_id}/users/nonexistent@canonical.com` | `DELETE` | Domain Admin deletes non-existent user | `200 OK` | **PASS** |
| **Phase 5: Multi-Tenant ABAC Isolation** | **TC-TENANT-01** | OpenFGA Engine Direct Check | `POST` | Domain Admin with matching tenant context | `allowed: true` | **PASS** |
| | **TC-TENANT-02** | OpenFGA Engine Direct Check | `POST` | Domain Admin with mismatched tenant context | `allowed: false` | **PASS** |
| **Phase 6: Session Eviction & Revocation Defense** | **TC-SESS-01** | `/api/v0/authz/groups` | `GET` | Evicted session cookie (deleted from Valkey) | `403 Forbidden` | **PASS** |
| **Phase 7: Global User Group Queries** | **TC-USR-01** | `/api/v0/authz/users/alice@canonical.com/groups` | `GET` | Domain Admin | `200 OK` | **PASS** |
| | **TC-USR-02** | `/api/v0/authz/users/alice@canonical.com/groups` | `GET` | Alice | `403 Forbidden` | **PASS** |
| | **TC-USR-03** | `/api/v0/authz/users/alice@canonical.com/groups` | `GET` | Mallory | `403 Forbidden` | **PASS** |
| **Phase 8: Group Deletion Security & Cleanup** | **TC-DEL-01** | `/api/v0/authz/groups/{alpha_id}` | `DELETE` | Alice | `403 Forbidden` | **PASS** |
| | **TC-DEL-02** | `/api/v0/authz/groups/{alpha_id}` | `DELETE` | Bob | `403 Forbidden` | **PASS** |
| | **TC-DEL-03** | `/api/v0/authz/groups/{alpha_id}` | `DELETE` | Charlie | `403 Forbidden` | **PASS** |
| | **TC-DEL-04** | `/api/v0/authz/groups/{alpha_id}` | `DELETE` | Mallory | `403 Forbidden` | **PASS** |
| | **TC-DEL-05** | `/api/v0/authz/groups/{alpha_id}` | `DELETE` | Domain Admin | `200 OK` | **PASS** |
| | **TC-DEL-06** | `/api/v0/authz/groups/{beta_id}` | `DELETE` | Domain Admin | `200 OK` | **PASS** |

---

### 7.3 Empirical Learnings & Operational Insights

During the development and hardening of the automated E2E test harness, five critical operational properties were identified and addressed:

#### 1. Zero-Trust Edge Perimeter and Phantom Tokens
- **Phantom Token Pattern**: External users present opaque, encrypted session cookies that are securely exchanged by STS for short-lived, internal signed JWTs containing subject (`sub`) and tenant (`org`).
- **Edge Gateway Policy**: Requests with missing, tampered, or evicted cookies never reach upstream resource handlers and are terminated at the Envoy edge.

#### 2. Valkey JWKS Cache Incoherency Prevention
- **Stale Key Trap**: STS generates active asymmetric signing keys stored in PostgreSQL and caches public keys in Valkey under `jwks:all` with a 10-minute TTL.
- **Root Cause & Fix**: When PostgreSQL databases are wiped and recreated between test runs, STS generates a new key pair, but Valkey previously retained the stale JWKS cache. This caused Authorization Service token verification failures (`failed to verify id token signature`).
- **Operational Standard**: The automated setup script explicitly flushes Valkey caches (`valkey-cli flushall`) during environment bootstrap, ensuring key coherency across consecutive runs.

#### 3. Coarse-Grained OpenFGA RBAC Evaluation
- **Centralized Rules**: All `/api/v0/authz/*` endpoints map to the OpenFGA relation `role:admin#assignee` with the `tenant_match` ABAC condition.
- **Immediate Rejection**: Non-admin callers (such as regular users Alice, Bob, Charlie, and Mallory) are rejected with `HTTP 403 Forbidden` directly at Envoy / Authorization Service extAuthz layer before ever invoking Hook Service handlers.

#### 4. Elimination of Custom Headers & User Impersonation Defense
- **Security Vulnerability Remediated**: Custom identity headers (such as `X-User-Id` or `X-Forwarded-User`) pose severe user impersonation risks if an external client can inject them and an upstream service trusts them.
- **Zero-Trust Enforcement**: The Hook Service `UserContextMiddleware` strictly parses only cryptographically verified `Authorization: Bearer <jwt>` tokens injected by Envoy after STS session exchange. Custom identity headers like `X-User-Id` are completely stripped/ignored. Tests `TC-AUTH-04` through `TC-AUTH-07` verify that neither unauthenticated nor authenticated callers can forge identity or elevate privileges via custom headers or injected Bearer tokens.

#### 5. Producer Standby Architecture
- **Infrastructure Readiness**: The Hook Service initializes and wires the `kafka.PermissionPublisher` into `groups.Service` in standby mode. This maintains operational readiness for future permission event streaming without incurring runtime Kafka event publication or fine-grained tuple sync overhead in the current release.


---

## 8. Troubleshooting & Diagnostics

### 1. Inspecting In-Flight gRPC Check Requests
Run Authorization Service with `LOGGING_LEVEL=debug` to observe the decision pipeline:
```bash
# Check log entries for:
# - "Check request received" method=POST path=/api/v0/authz/groups
# - "Calling STS ExchangeSession"
# - "BatchCheck executed" allowed=true/false
# - "Authorization decision" result=allow/deny
tail -f logs/centralized-authz/authorization-service-serve.log
```

### 2. Inspecting STS Session State in Valkey
```bash
docker exec -it authz-valkey redis-cli -p 6379 KEYS "*"
docker exec -it authz-valkey redis-cli -p 6379 GET "<session_key>"
```

### 3. Inspecting Raw Kafka Events
```bash
docker exec authz-kafka /opt/kafka/bin/kafka-console-consumer.sh \
  --bootstrap-server localhost:9092 \
  --topic hook-service.permissions \
  --from-beginning
```

### 4. Simulating Envoy Authorization via CLI
Authorization Service includes a built-in debugging command to verify authorization evaluation without running Envoy:
```bash
./bin/app authz check \
  --cookie "session=$ADMIN_COOKIE" \
  --method "POST" \
  --path "/api/v0/authz/groups" \
  --host "localhost"
```
