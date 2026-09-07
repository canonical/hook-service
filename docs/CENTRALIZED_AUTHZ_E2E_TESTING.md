<!--
// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only
-->

# Centralized Authorization Architecture & End-to-End Verification Report

This document provides the authoritative architectural specification and the verified end-to-end (E2E) testing report for the **Canonical Centralized Authorization Platform**. It integrates four foundational systems:
1. **Istio Ingress Gateway & Ambient Waypoint Proxy** (Envoy External Authorization)
2. **Secure Token Service (STS - Janus)** (Phantom Token Pattern & JWT Issuance)
3. **Authorization Service (Cerberus)** (Policy Decision Point, Route Matcher & Async OpenFGA Sync)
4. **Hook Service** (Identity Platform Hook & Groups Resource Server)

---

## 1. Executive Summary & The Big Picture

The Canonical Identity and Access Management (IAM) platform enforces a **zero-trust, fail-closed, centralized authorization model**. In this architecture:
- Microservices **do not manage authorization policies locally**. Instead, authorization logic is centralized in **OpenFGA** and orchestrated by **Cerberus (Authorization Service)**.
- Microservices **do not handle raw user credentials or opaque external session cookies**. Instead, the **Phantom Token Pattern** is implemented by **Janus (Secure Token Service)** at the network edge.
- Network routing and policy enforcement are decoupled from service logic: **Istio Gateway / Waypoint Proxies** intercept incoming L7 traffic and query Cerberus via Envoy's standard `ext_authz` gRPC interface before traffic ever reaches the target service.
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
|      * Pauses request processing and dispatches gRPC CheckRequest to Cerberus                                    |
+==================================================================================================================+
                                    |                                         ^
              gRPC CheckRequest     |                                         |  CheckResponse:
      (Path, Method, Cookie Header) |                                         |  - OK (Injects Bearer JWT)
                                    v                                         |  - DENIED (401/403)
+==================================================================================================================+
|  CENTRALIZED POLICY DECISION POINT (Cerberus - Authorization Service)                                            |
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
|      * Decoupled Event Publication: Publishes PermissionUpdateEnvelope to Kafka broker                           |
+==================================================================================================================+
                                                      |
                                       Publish Protobuf Event
                                                      v
                                        [Kafka Broker :9092]
                                      (hook-service.permissions)
                                                      |
                                       Consume Protobuf Event
                                                      v
+==================================================================================================================+
|  ASYNCHRONOUS RECONCILIATION PIPELINE (Cerberus Background Workers)                                              |
|                                                                                                                  |
|  [Cerberus Kafka Listener] (:9101) ---> [PostgreSQL Work Queue] ---> [Cerberus Async Worker] (:9102)             |
|  Durable Kafka ingestion                 permission_update_work      Batch writes/deletes tuples into OpenFGA    |
+==================================================================================================================+
                                                                                       |
                                                                        Write / Delete Tuples
                                                                                       v
                                                                             [OpenFGA Engine :8082]
```

---

## 2. Core Architectural Pillars

### Pillar 1: Istio Ingress Gateway & Ambient Waypoint Proxy
- **Role**: Policy Enforcement Point (PEP) at the network perimeter and service mesh boundary.
- **Ambient Mode Architecture**: In Istio ambient mode, L4 traffic is handled by node-level `ztunnel` proxies, while L7 processing is handled by dedicated **Waypoint Proxies** (HBONE protocol on port `15008`).
- **Envoy `ext_authz` Integration**:
  - Waypoint proxies are configured with an Istio `AuthorizationPolicy` with `action: CUSTOM`.
  - The custom provider references `authorization-service` configured in Istio's `meshConfig.extensionProviders`.
  - On protected routes (such as `/api/v0/authz/groups/*`), Envoy suspends the client request and transmits a gRPC `CheckRequest` (`/envoy.service.auth.v3.Authorization/Check`) containing all HTTP headers, cookies, method, and URI path to Cerberus on port `9091`.
  - When Cerberus approves the request with `OkResponse`, Envoy merges the injected headers (`Authorization: Bearer <internal_jwt>`) into the upstream request and dispatches it to the destination workload.

### Pillar 2: Secure Token Service (STS - Janus)
- **Role**: Phantom Token translator and internal JWT authority.
- **The Phantom Token Pattern**:
  - External users interact with browsers or API clients holding an **opaque, encrypted session cookie** (`session=<cookie_value>`). External tokens never expose internal claims or infrastructure topologies to the public internet.
  - STS stores session metadata (user identity, original OIDC tokens, expiration) in a **Valkey / Redis** cluster.
  - STS manages asymmetric ECDSA (P-256) signing keys stored in PostgreSQL (`hydra_jwk` table), supporting zero-downtime key rotation via `./bin/sts rotate-key`.
- **Interfaces**:
  - **HTTP (`:8080`)**: Exposes public JSON Web Key Set at `/.well-known/jwks.json`, and OIDC lifecycle endpoints (`/auth/login`, `/auth/callback`, `/auth/logout`).
  - **gRPC (`:9090`)**: Exposes `SecurityTokenService.ExchangeSession`, swapping an opaque session cookie for an internal signed JWT containing `sub` (user identity) and `org` (tenant).

### Pillar 3: Authorization Service (Cerberus)
- **Role**: Centralized Policy Decision Point (PDP) and Kafka permission reconciler.
- **Three Core Operational Subsystems**:
  1. **extAuthz Server (`bin/app serve`)**:
     - Listens on gRPC `:9091` and REST `:8070`.
     - Validates the incoming session cookie with STS via gRPC `ExchangeSession`.
     - Verifies the minted JWT against STS's public JWKS endpoint.
     - Resolves the requested HTTP path and method against rules stored in PostgreSQL (`authorization_rule`).
     - Performs OpenFGA `BatchCheck` queries against OpenFGA (`:8082`), verifying user relations while enforcing multi-tenant isolation via the `tenant_match` condition.
     - Returns gRPC `CheckResponse` to Envoy, injecting `Authorization: Bearer <JWT>`.
  2. **Kafka Listener Daemon (`bin/app listen`)**:
     - Consumes protobuf messages from `<slug>.permissions` Kafka topics (e.g., `hook-service.permissions`).
     - Persists update intents into the PostgreSQL `permission_update_work` queue with idempotency keys.
  3. **Async Worker Daemon (`bin/app worker`)**:
     - Polls `permission_update_work` in batches.
     - Flushes relationship writes and deletes to OpenFGA (`:8082`).

### Pillar 4: Hook Service
- **Role**: Microservice resource server and Canonical Identity Platform Hydra token hook.
- **Admin APIs**:
  - Exposes Group Management endpoints under `/api/v0/authz/groups/*`.
  - Operates behind the Istio Gateway / Envoy proxy.
- **JWT Middleware**:
  - In production / authenticated mesh mode: `pkg/authentication/middleware.go` validates the forwarded `Authorization: Bearer <jwt>` against STS JWKS and populates the request context with caller identity.
  - In trusted proxy mode: `UserContextMiddleware` extracts caller identity from the `Authorization: Bearer <jwt>` header.
- **Decoupled Kafka Permission Streaming**:
  - When groups or memberships are modified in PostgreSQL, Hook Service writes domain records locally and publishes an asynchronous `PermissionUpdateEnvelope` protobuf event to Kafka (`hook-service.permissions`).
  - This ensures that group creation/update requests succeed with ultra-low latency without waiting for OpenFGA network roundtrips.

---

## 3. Detailed Request Lifecycle: Step-by-Step

### Complete End-to-End Flow Diagram

```mermaid
sequenceDiagram
    autonumber
    actor Client as External Client (Browser/API)
    participant Istio as Istio Gateway / Waypoint Proxy
    participant Cerberus as Cerberus extAuthz (:9091)
    participant STS as STS (Janus :9090 / :8080)
    participant FGA as OpenFGA Engine (:8082)
    participant Hook as Hook Service (:8000)
    participant Kafka as Kafka Broker (:9092)
    participant Worker as Cerberus Worker (:9102)

    Note over Client,Istio: Phase 1: Ingress Interception
    Client->>Istio: HTTP POST /api/v0/authz/groups (Cookie: session=<cookie_val>)
    Istio->>Cerberus: gRPC CheckRequest (Path, Method, Headers, Cookies)

    Note over Cerberus,STS: Phase 2: Phantom Token Translation
    Cerberus->>STS: gRPC ExchangeSession(session_cookie)
    STS-->>Cerberus: ExchangeResponse(access_token = JWT)
    Cerberus->>STS: GET /.well-known/jwks.json (Cached JWKS Verification)
    Note over Cerberus: Extract Claims: sub="domain-admin@canonical.com", org="hook-service"

    Note over Cerberus,FGA: Phase 3: Route Rule Resolution & OpenFGA Check
    Cerberus->>Cerberus: Match Route POST /api/v0/authz/groups -> admin on group-in-claim:__domain__
    Cerberus->>FGA: BatchCheck(user:domain-admin@canonical.com, admin, group-in-claim:__domain__, context={tenant_match})
    FGA-->>Cerberus: Allowed = true

    Note over Cerberus,Hook: Phase 4: Forwarding with Injected Bearer Token
    Cerberus-->>Istio: gRPC CheckResponse (OK, Header: Authorization=Bearer <JWT>)
    Istio->>Hook: Forwarded HTTP POST /api/v0/authz/groups (with Authorization: Bearer <JWT>)
    
    Note over Hook: Phase 5: Resource Execution & Kafka Event
    Hook->>Hook: UserContextMiddleware extracts sub="domain-admin@canonical.com"
    Hook->>Hook: Insert group & creator (RoleOwner) in PostgreSQL
    Hook->>Kafka: Publish PermissionUpdateEnvelope (owner tuple write)
    Hook-->>Istio: HTTP 200 OK {"status": 200, "message": "Group created"}
    Istio-->>Client: HTTP 200 OK {"status": 200, "message": "Group created"}

    Note over Kafka,Worker: Phase 6: Asynchronous Eventual Consistency
    Kafka->>Cerberus: Listener consumes message -> inserts into permission_update_work
    Worker->>FGA: Writes tuple (user:domain-admin@canonical.com -> owner -> group-in-claim:<id>)
```

### Protocol Steps Breakdown

| Step | Initiator | Receiver | Protocol | Payload / Operation |
|:---:|---|---|:---:|---|
| **1** | External Client | Istio Gateway | HTTP/1.1 or HTTP/2 | `POST /api/v0/authz/groups` with `Cookie: session=<opaque_cookie>` |
| **2** | Istio Waypoint | Cerberus | gRPC (`ext_authz.v3`) | `CheckRequest` containing headers, cookie, method `POST`, path `/api/v0/authz/groups` |
| **3** | Cerberus | STS (Janus) | gRPC (`sts.v1`) | `ExchangeSessionRequest{SessionCookie: "<opaque_cookie>"}` on port `9090` |
| **4** | STS | Cerberus | gRPC (`sts.v1`) | `ExchangeSessionResponse{AccessToken: "<signed_jwt>", ExpiresIn: 3600}` |
| **5** | Cerberus | STS (Janus) | HTTP (`GET`) | Fetches `/.well-known/jwks.json` on port `8080` (cached with TTL) to verify signature |
| **6** | Cerberus | OpenFGA | HTTP (`POST`) | `POST /stores/{id}/check` for `user:<subId> -> admin -> group-in-claim:__domain__` |
| **7** | Cerberus | Istio Waypoint | gRPC (`ext_authz.v3`) | `CheckResponse{Status: OK, Headers: [Authorization: Bearer <JWT>]}` |
| **8** | Istio Waypoint | Hook Service | HTTP/1.1 | Proxies request upstream with injected `Authorization: Bearer <JWT>` header |
| **9** | Hook Service | Database | PostgreSQL TCP | Inserts group into `groups` table and creator into `group_members` table |
| **10** | Hook Service | Kafka Broker | Kafka Protocol | Produces `PermissionUpdateEnvelope` protobuf message to `hook-service.permissions` |
| **11** | Hook Service | External Client | HTTP/1.1 | Returns `{"status": 200, "message": "Group created"}` via Istio |
| **12** | Cerberus Listener | Kafka Broker | Kafka Protocol | Consumes event from `hook-service.permissions` and writes to `permission_update_work` |
| **13** | Cerberus Worker | OpenFGA | HTTP (`POST`) | Writes `user:<subId> -> owner -> group-in-claim:<groupId>` tuple into OpenFGA store |

---

## 4. Network, Port, and Credential Allocation Matrix

| Service / Container | Process | Listen Port | Protocol | Purpose / URL |
|---|---|:---:|:---:|---|
| **Istio Ingress / Gateway** | Envoy | `80` / `443` / `10000` | HTTP / HTTPS | Public entry point for external client requests |
| **Istio Waypoint Proxy** | Envoy (Ambient) | `15008` | HBONE / HTTP | L7 Envoy proxy enforcing `AuthorizationPolicy` |
| **Secure Token Service (STS)** | `sts serve` | `8080` | HTTP | JWKS (`/.well-known/jwks.json`), OIDC login/callbacks |
| **Secure Token Service (STS)** | `sts serve` | `9090` | gRPC | `SecurityTokenService.ExchangeSession` |
| **Cerberus extAuthz Server** | `app serve` | `9091` | gRPC | Envoy `envoy.service.auth.v3.Authorization/Check` |
| **Cerberus REST Gateway** | `app serve` | `8070` | HTTP | Admin / Management REST API |
| **Cerberus Metrics (Serve)** | `app serve` | `9100` | HTTP | Prometheus metrics for extAuthz server |
| **Cerberus Kafka Listener** | `app listen` | `9101` | HTTP (Metrics) | Kafka consumer metrics & liveness |
| **Cerberus Async Worker** | `app worker` | `9102` | HTTP (Metrics) | Worker batch queue metrics & liveness |
| **Hook Service** | `app serve` | `8000` | HTTP | Group Admin REST API (`/api/v0/authz/groups`) |
| **Hook Service Internal gRPC** | `app serve` | `9095` | gRPC | Groups mapping streaming gRPC API |
| **OpenFGA Engine** | `openfga run` | `8082` (host) / `8080` (ctr) | HTTP | OpenFGA API (`/stores/{id}/check`, `/write`, `/read`) |
| **OpenFGA gRPC** | `openfga run` | `8081` (host) / `8081` (ctr) | gRPC | OpenFGA high-performance gRPC endpoint |
| **Kafka Broker** | `kafka` | `9092` | TCP | Event streaming topic: `hook-service.permissions` |
| **PostgreSQL Database** | `postgres:16` | `5433` (host) / `5432` (ctr) | TCP | Multi-DB host: `authorization-service`, `groups`, `sts` |
| **Valkey / Redis Cache** | `valkey:8` | `6380` (host) / `6379` (ctr) | TCP | Session store for STS / Janus |

---

## 5. End-to-End Test Plan

This test plan defines the complete verification strategy for validating the integrated Centralized Authorization platform across all four pillars.

### 5.1 Test Environment Architecture & Pre-requisites

```
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
[STS Janus]      [Cerberus extAuthz]                 [Cerberus Pipeline]  [Hook Service]
HTTP :8080       gRPC :9091                          Listener :9101       HTTP :8000
gRPC :9090       REST :8070                          Worker   :9102       gRPC :9095
```

### 5.2 Stage-by-Stage Bootstrapping & Setup

#### Stage 1: Infrastructure Provisioning
1. Launch dependencies using Docker Compose:
   ```bash
   VALKEY_PORT=6380 docker compose -f authorization-service/docker/dependencies/docker-compose.yml up -d
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

#### Stage 3: Cerberus Bootstrapping
1. Run Cerberus PostgreSQL migrations:
   ```bash
   POSTGRES_PORT=5433 ./bin/app migrate
   ```
2. Upload compiled OpenFGA model containing `group-in-claim` and `tenant_match` condition:
   ```bash
   ./bin/app authz model write --store-id 01GP1254CHWJC1MNGVB0WDG1T0
   ```
3. Seed Hook Service route rules:
   ```bash
   POSTGRES_PORT=5433 ./bin/app seed -s services/hook-service/rules.yaml
   ```
4. Seed initial Domain Admin tuple:
   ```bash
   curl -s -X POST http://localhost:8082/stores/01GP1254CHWJC1MNGVB0WDG1T0/write \
     -H "Content-Type: application/json" \
     -H "Authorization: Bearer 42" \
     -d '{
       "writes": {
         "tuple_keys": [
           {
             "user": "user:domain-admin@canonical.com",
             "relation": "admin",
             "object": "group-in-claim:__domain__",
             "condition": { "name": "tenant_match", "context": { "tenant": "hook-service" } }
           }
         ]
       }
     }'
   ```
5. Start Cerberus Daemons:
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
Start an Envoy proxy instance configured with `envoy.filters.http.ext_authz` pointing to Cerberus at `host.docker.internal:9091` and upstream routing pointing to Hook Service at `host.docker.internal:8000`.

---

## 6. Comprehensive Hook Service Admin API Permission Control Specification

To ensure robust defense-in-depth across the platform, Hook Service admin endpoints are guarded by a combination of:
1. **Edge Authentication (STS & Envoy)**: Requires a valid opaque session cookie (`session_id=<token>`) exchanged for an internal signed JWT.
2. **Centralized Policy Evaluation (Cerberus & OpenFGA)**: Every API route is matched against declarative rules in Cerberus and evaluated against OpenFGA relation tuples with ABAC conditions (`tenant_match`).
3. **Decoupled Asynchronous Lifecycle (Kafka & Async Workers)**: State modifications publish Protobuf events that asynchronously reconcile OpenFGA tuples.

### 6.1 Multi-Persona Model

The E2E testing framework establishes five distinct personas to rigorously verify vertical privilege separation, horizontal group isolation, and defense against privilege escalation:

| Persona Name | Identity / Subject (`sub`) | Roles & OpenFGA Relations | Description |
|---|---|---|---|
| **Domain Admin** | `domain-admin@canonical.com` | `admin` on `group-in-claim:__domain__` | Platform super-administrator. Can list all system groups, create groups, delete groups, and manage any group resource. |
| **Group Owner** | `alice@canonical.com` | `owner` on `group-in-claim:alpha` | Delegated administrator of Group Alpha. Can inspect, update, and manage members of Group Alpha. Has NO global admin privileges and CANNOT delete the group. |
| **Group Member** | `bob@canonical.com` | `member` on `group-in-claim:alpha` | Regular member of Group Alpha. Enjoys end-user token claims, but is STRICTLY FORBIDDEN from all administrative actions (cannot read admin endpoints, cannot update group, cannot manage members). |
| **Cross-Group Owner** | `charlie@canonical.com` | `owner` on `group-in-claim:beta` | Delegated administrator of a completely disjoint group (Group Beta). Used to verify **horizontal resource isolation** (Charlie has full control over Beta, but zero access to Alpha). |
| **Unprivileged Outsider** | `mallory@canonical.com` | None | Authenticated user with no group ownerships or memberships. Must be rejected with `HTTP 403 Forbidden` across all protected endpoints. |

---

### 6.2 Exhaustive Route & Permission Protection Matrix

The table below details the exact mapping between Hook Service Admin API endpoints, the Cerberus protection rule, the required OpenFGA relation, and the expected HTTP status code for each persona:

| HTTP Method | API Route | Cerberus Required Tuple | Domain Admin | Group Owner (Alpha) | Group Member (Alpha) | Cross Owner (Beta) | Outsider |
|:---:|---|---|:---:|:---:|:---:|:---:|:---:|
| `GET` | `/api/v0/authz/groups` | `user -> admin -> group-in-claim:__domain__` | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden | 403 Forbidden |
| `POST` | `/api/v0/authz/groups` | `user -> admin -> group-in-claim:__domain__` | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden | 403 Forbidden |
| `GET` | `/api/v0/authz/groups/{id_alpha}` | `user -> owner -> group-in-claim:{id_alpha}` | **200 OK** | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden |
| `PUT` | `/api/v0/authz/groups/{id_alpha}` | `user -> owner -> group-in-claim:{id_alpha}` | **200 OK** | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden |
| `DELETE` | `/api/v0/authz/groups/{id_alpha}` | `user -> admin -> group-in-claim:__domain__` | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden | 403 Forbidden |
| `GET` | `/api/v0/authz/groups/{id_alpha}/users` | `user -> owner -> group-in-claim:{id_alpha}` | **200 OK** | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden |
| `POST` | `/api/v0/authz/groups/{id_alpha}/users` | `user -> owner -> group-in-claim:{id_alpha}` | **200 OK** | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden |
| `DELETE` | `/api/v0/authz/groups/{id_alpha}/users/{uid}` | `user -> owner -> group-in-claim:{id_alpha}` | **200 OK** | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden |
| `GET` | `/api/v0/authz/users/{uid}/groups` | `user -> admin -> group-in-claim:__domain__` | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden | 403 Forbidden |
| `PUT` | `/api/v0/authz/users/{uid}/groups` | `user -> admin -> group-in-claim:__domain__` | **200 OK** | 403 Forbidden | 403 Forbidden | 403 Forbidden | 403 Forbidden |

> **Key Architectural Insights**:
> 1. **Group Deletion Restriction**: `DELETE /api/v0/authz/groups/{groupId}` requires `admin` on `__domain__`, NOT `owner` on the group. Group owners cannot self-delete groups; only domain admins can decommission groups.
> 2. **Owner Inheritance**: In OpenFGA, the `owner` relation is defined as `this or admin from group-in-claim`. Therefore, any user with `admin` on `__domain__` automatically satisfies `owner` checks across all group instances.
> 3. **Non-Existent Resource Defense**: Because OpenFGA checks require a valid relation tuple, requests targeted at non-existent resource IDs (`00000000-0000-0000-0000-000000000000`) are blocked at Envoy with `403 Forbidden` before ever querying the upstream Hook Service database.

---

### 6.3 Detailed End-to-End Test Execution Phases

The test suite executes 64 distinct assertions across 11 structured phases:

#### Phase 1: Edge Perimeter & Authentication Defense
- **TC-AUTH-01**: Missing session cookie in request header is immediately rejected at the Envoy edge with `HTTP 401 Unauthorized`.
- **TC-AUTH-02**: Tampered or corrupted session cookie is rejected during STS session exchange, causing Envoy to return `HTTP 403 Forbidden` (`code = InvalidArgument desc = invalid session cookie`).
- **TC-AUTH-03**: Probing non-existent UUIDs (`00000000-0000-0000-0000-000000000000`) is rejected by OpenFGA with `HTTP 403 Forbidden` without hitting the microservice.
- **TC-AUTH-04**: Custom header spoofing attempt: Mallory sends valid session cookie plus `X-User-Id: domain-admin@canonical.com`. Edge gateway and Cerberus evaluate identity strictly from the authenticated session, completely ignoring caller-supplied custom identity headers, resulting in `HTTP 403 Forbidden`.
- **TC-AUTH-05**: Unauthenticated request attempting to supply `X-User-Id: domain-admin@canonical.com` without a session cookie is rejected at the Envoy edge with `HTTP 401 Unauthorized`.
- **TC-AUTH-06**: Direct forged Bearer token (`Authorization: Bearer <fake>`) without a session cookie is rejected at the edge with `HTTP 401 Unauthorized` (Phantom Token pattern strictly enforced).
- **TC-AUTH-07**: Mallory sends valid session cookie plus forged admin Bearer header (`Authorization: Bearer <fake-admin>`). Envoy overwrites upstream authorization with the cryptographically minted internal JWT from STS, preventing privilege escalation (`HTTP 403 Forbidden`).

#### Phase 2: Domain-Level Administrative Operations
- **TC-DOM-01**: Domain Admin queries `GET /api/v0/authz/groups` and receives `HTTP 200 OK` with the complete list of system groups.
- **TC-DOM-02 - TC-DOM-04**: Alice (Group Owner), Bob (Member), and Mallory (Outsider) attempting to list system groups are all blocked with `HTTP 403 Forbidden`.
- **TC-DOM-05 - TC-DOM-06**: Alice and Mallory attempting `POST /api/v0/authz/groups` are blocked with `HTTP 403 Forbidden`.

#### Phase 3: Provisioning & Ownership Delegation
- Domain Admin creates Group Alpha (`test-group-alpha`) and Group Beta (`test-group-beta`).
- Domain Admin delegates `owner` of Alpha to Alice, adds Bob as `member` to Alpha, and delegates `owner` of Beta to Charlie.
- Cerberus async workers sync the corresponding relation tuples into OpenFGA with `tenant_match` ABAC conditions.

#### Phase 4: Group Read/Update Operations & Horizontal Isolation
- **TC-GRP-01 - TC-GRP-02**: Domain Admin and Owner Alice read Group Alpha details (`HTTP 200 OK`).
- **TC-GRP-03**: Member Bob attempting to read Group Alpha admin details is rejected with `HTTP 403 Forbidden`.
- **TC-GRP-04**: Charlie (Owner of Beta) attempting to read Group Alpha is blocked with `HTTP 403 Forbidden` (Horizontal Isolation).
- **TC-GRP-05**: Outsider Mallory is blocked with `HTTP 403 Forbidden`.
- **TC-GRP-06 - TC-GRP-07**: Charlie reads Group Beta (`HTTP 200 OK`), while Alice attempting to read Group Beta is blocked with `HTTP 403 Forbidden`.
- **TC-GRP-08**: Owner Alice updates Group Alpha description via `PUT /api/v0/authz/groups/{alpha_id}` (`HTTP 200 OK`).
- **TC-GRP-09 - TC-GRP-11**: Bob, Charlie, and Mallory attempting to update Group Alpha are all rejected with `HTTP 403 Forbidden`.

#### Phase 5: Membership Management & Privilege Escalation Prevention
- **TC-MEM-01 - TC-MEM-02**: Domain Admin and Owner Alice retrieve member list for Group Alpha (`HTTP 200 OK`).
- **TC-MEM-03 - TC-MEM-05**: Bob, Charlie, and Mallory attempting to list members are rejected with `HTTP 403 Forbidden`.
- **TC-MEM-06**: Regular member Bob attempting to add user `dave@canonical.com` is rejected with `HTTP 403 Forbidden` (Privilege Escalation Prevention).
- **TC-MEM-07 - TC-MEM-08**: Charlie and Mallory attempting to add users to Alpha are blocked with `HTTP 403 Forbidden`.
- **TC-MEM-09**: Owner Alice adds `dave@canonical.com` to Group Alpha (`HTTP 200 OK`).
- **TC-MEM-10**: Verified OpenFGA evaluates `user:dave@canonical.com -> member -> group-in-claim:{alpha_id}` as `allowed: true`.
- **TC-MEM-11 - TC-MEM-13**: Bob, Charlie, and Mallory attempting to remove Dave are rejected with `HTTP 403 Forbidden`.
- **TC-MEM-14**: Owner Alice removes Dave from Group Alpha (`HTTP 200 OK`).
- **TC-MEM-15**: Asynchronous reconciliation evicts Dave's member tuple from OpenFGA, verified with `allowed: false`.

#### Phase 6: Dual-Role User Lifecycle & Zombie Owner Prevention
Addresses data desync and zombie owner tuple elimination:
- **TC-DUAL-01**: Owner Alice adds herself as a member of Group Alpha (`POST /api/v0/authz/groups/{alpha_id}/users` `["alice@canonical.com"]` -> `HTTP 200 OK`).
- **TC-DUAL-02**: OpenFGA verification: Alice holds dual roles simultaneously (`owner: true` and `member: true`).
- **TC-DUAL-03**: PostgreSQL storage verification: Table `group_members` contains two distinct rows under composite PK `(group_id, role, user_id)`: one for `role = 1` (owner) and one for `role = 0` (member).
- **TC-DUAL-04**: Dual-role Alice successfully reads the group members list (`HTTP 200 OK`).
- **TC-DUAL-05**: Alice removes herself from the group members endpoint (`DELETE /api/v0/authz/groups/{alpha_id}/users/alice@canonical.com` -> `HTTP 200 OK`).
- **TC-DUAL-06**: OpenFGA verification: Alice's `member` tuple is revoked (`allowed: false`).
- **TC-DUAL-07**: **CRITICAL CHECK**: OpenFGA verification: Alice's `owner` tuple is **PRESERVED** (`allowed: true`). The owner tuple is not prematurely deleted or orphaned into a zombie state.
- **TC-DUAL-08**: PostgreSQL verification: Alice's `role = 1` (owner) row remains in `group_members`, and only `role = 0` was removed.
- **TC-DUAL-09**: Alice exercises owner privileges by updating Group Alpha (`PUT /api/v0/authz/groups/{alpha_id}` -> `HTTP 200 OK`).
- **TC-DUAL-10**: Alice exercises owner privileges by listing members (`GET /api/v0/authz/groups/{alpha_id}/users` -> `HTTP 200 OK`).

#### Phase 7: Idempotency & Edge Robustness
- **TC-IDEMP-01**: Idempotent member addition: Owner Alice adds `["bob@canonical.com"]` when Bob is already a member (`HTTP 200 OK`).
- **TC-IDEMP-02**: Idempotent member deletion: Owner Alice deletes non-existent user `nonexistent-user@canonical.com` (`HTTP 200 OK` per REST idempotency guidelines).

#### Phase 8: Multi-Tenant ABAC Isolation Check
- **TC-TENANT-01**: OpenFGA direct check succeeds when request context includes matching tenant (`context: {"tenant_enabled": true, "user_tenant": "hook-service"}` -> `allowed: true`).
- **TC-TENANT-02**: OpenFGA direct check is denied when request context includes an alien tenant (`context: {"tenant_enabled": true, "user_tenant": "alien-tenant"}` -> `allowed: false`).

#### Phase 9: Session Eviction & Revocation Defense
- **TC-SESS-01**: An ephemeral session cookie is minted, verified, and then explicitly evicted from the Valkey session cache (`valkey-cli del "session:<id>"`). The subsequent request through Envoy is rejected by STS/Cerberus with `HTTP 403 Forbidden`, confirming instant session revocation.

#### Phase 10: Group Deletion Security & Cascading Cleanup
- **TC-DEL-01**: Owner Alice attempting `DELETE /api/v0/authz/groups/{alpha_id}` is rejected with `HTTP 403 Forbidden` (Only Domain Admin can delete groups!).
- **TC-DEL-02 - TC-DEL-04**: Bob, Charlie, and Mallory attempting group deletion are rejected with `HTTP 403 Forbidden`.
- **TC-DEL-05 - TC-DEL-06**: Domain Admin executes `DELETE /api/v0/authz/groups/{alpha_id}` and `DELETE /api/v0/authz/groups/{beta_id}` (`HTTP 200 OK`).
- **TC-DEL-07**: Hook Service emits cascading deletion events to Kafka, and Cerberus worker evicts all owner and member tuples from OpenFGA. Verified **zero tuple leaks** (`0 tuples remaining`).

#### Phase 11: Global User Group Queries
- **TC-USR-01**: Domain Admin queries `GET /api/v0/authz/users/alice@canonical.com/groups` (`HTTP 200 OK`).
- **TC-USR-02 - TC-USR-03**: Non-admin users Alice and Mallory attempting user group inspection are blocked with `HTTP 403 Forbidden`.

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
| **Test Suite Script** | [`scripts/test-centralized-authz-e2e.sh`](file:///home/shu.du@canonical.com/workspaces/canonical/sync/hook-service/scripts/test-centralized-authz-e2e.sh) | The 64-assertion test suite executing against Envoy `:10000` |
| **All-in-One Runner** | [`scripts/run-centralized-authz-e2e.sh`](file:///home/shu.du@canonical.com/workspaces/canonical/sync/hook-service/scripts/run-centralized-authz-e2e.sh) | Runs setup -> test -> teardown in a single command |

#### Quick Start via Makefile

```bash
# Option A: Run everything end-to-end (setup -> test -> teardown)
make authz-e2e

# Option B: Setup environment and leave it running for iterative debugging
make authz-setup

# Run the 64-assertion test suite repeatedly against the running mesh
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
Total Tests Executed   : 64
Passed Tests           : 64
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
| **Phase 2: Domain-Level Administrative Operations** | **TC-DOM-01** | `/api/v0/authz/groups` | `GET` | Domain Admin | `200 OK` | **PASS** |
| | **TC-DOM-02** | `/api/v0/authz/groups` | `GET` | Alice (Group Owner) | `403 Forbidden` | **PASS** |
| | **TC-DOM-03** | `/api/v0/authz/groups` | `GET` | Bob (Group Member) | `403 Forbidden` | **PASS** |
| | **TC-DOM-04** | `/api/v0/authz/groups` | `GET` | Mallory (Outsider) | `403 Forbidden` | **PASS** |
| | **TC-DOM-05** | `/api/v0/authz/groups` | `POST` | Alice (Group Owner) | `403 Forbidden` | **PASS** |
| | **TC-DOM-06** | `/api/v0/authz/groups` | `POST` | Mallory (Outsider) | `403 Forbidden` | **PASS** |
| **Phase 3: Provisioning & Delegation** | — | `/api/v0/authz/groups` | `POST` | Domain Admin creates Alpha & Beta | `200 OK` | **DONE** |
| | — | `/api/v0/authz/groups/{alpha}/users` | `POST` | Domain Admin adds Bob to Alpha | `200 OK` | **DONE** |
| **Phase 4: Read/Update & Horizontal Isolation** | **TC-GRP-01** | `/api/v0/authz/groups/{alpha_id}` | `GET` | Domain Admin | `200 OK` | **PASS** |
| | **TC-GRP-02** | `/api/v0/authz/groups/{alpha_id}` | `GET` | Alice (Owner of Alpha) | `200 OK` | **PASS** |
| | **TC-GRP-03** | `/api/v0/authz/groups/{alpha_id}` | `GET` | Bob (Member of Alpha) | `403 Forbidden` | **PASS** |
| | **TC-GRP-04** | `/api/v0/authz/groups/{alpha_id}` | `GET` | Charlie (Owner of Beta) | `403 Forbidden` | **PASS** |
| | **TC-GRP-05** | `/api/v0/authz/groups/{alpha_id}` | `GET` | Mallory (Outsider) | `403 Forbidden` | **PASS** |
| | **TC-GRP-06** | `/api/v0/authz/groups/{beta_id}` | `GET` | Charlie (Owner of Beta) | `200 OK` | **PASS** |
| | **TC-GRP-07** | `/api/v0/authz/groups/{beta_id}` | `GET` | Alice (Owner of Alpha) | `403 Forbidden` | **PASS** |
| | **TC-GRP-08** | `/api/v0/authz/groups/{alpha_id}` | `PUT` | Alice (Owner of Alpha) | `200 OK` | **PASS** |
| | **TC-GRP-09** | `/api/v0/authz/groups/{alpha_id}` | `PUT` | Bob (Member of Alpha) | `403 Forbidden` | **PASS** |
| | **TC-GRP-10** | `/api/v0/authz/groups/{alpha_id}` | `PUT` | Charlie (Owner of Beta) | `403 Forbidden` | **PASS** |
| | **TC-GRP-11** | `/api/v0/authz/groups/{alpha_id}` | `PUT` | Mallory (Outsider) | `403 Forbidden` | **PASS** |
| **Phase 5: Membership Management & Kafka Sync** | **TC-MEM-01** | `/api/v0/authz/groups/{alpha_id}/users` | `GET` | Domain Admin | `200 OK` | **PASS** |
| | **TC-MEM-02** | `/api/v0/authz/groups/{alpha_id}/users` | `GET` | Alice (Owner of Alpha) | `200 OK` | **PASS** |
| | **TC-MEM-03** | `/api/v0/authz/groups/{alpha_id}/users` | `GET` | Bob (Member of Alpha) | `403 Forbidden` | **PASS** |
| | **TC-MEM-04** | `/api/v0/authz/groups/{alpha_id}/users` | `GET` | Charlie (Owner of Beta) | `403 Forbidden` | **PASS** |
| | **TC-MEM-05** | `/api/v0/authz/groups/{alpha_id}/users` | `GET` | Mallory (Outsider) | `403 Forbidden` | **PASS** |
| | **TC-MEM-06** | `/api/v0/authz/groups/{alpha_id}/users` | `POST` | Bob adds Dave (Escalation) | `403 Forbidden` | **PASS** |
| | **TC-MEM-07** | `/api/v0/authz/groups/{alpha_id}/users` | `POST` | Charlie adds Dave to Alpha | `403 Forbidden` | **PASS** |
| | **TC-MEM-08** | `/api/v0/authz/groups/{alpha_id}/users` | `POST` | Mallory adds Dave to Alpha | `403 Forbidden` | **PASS** |
| | **TC-MEM-09** | `/api/v0/authz/groups/{alpha_id}/users` | `POST` | Alice adds Dave to Alpha | `200 OK` | **PASS** |
| | **TC-MEM-10** | OpenFGA Engine Direct Check | `POST` | Dave `member` tuple check | `allowed: true` | **PASS** |
| | **TC-MEM-11** | `/api/v0/authz/groups/{alpha_id}/users/dave@canonical.com` | `DELETE` | Bob removes Dave | `403 Forbidden` | **PASS** |
| | **TC-MEM-12** | `/api/v0/authz/groups/{alpha_id}/users/dave@canonical.com` | `DELETE` | Charlie removes Dave | `403 Forbidden` | **PASS** |
| | **TC-MEM-13** | `/api/v0/authz/groups/{alpha_id}/users/dave@canonical.com` | `DELETE` | Mallory removes Dave | `403 Forbidden` | **PASS** |
| | **TC-MEM-14** | `/api/v0/authz/groups/{alpha_id}/users/dave@canonical.com` | `DELETE` | Alice removes Dave | `200 OK` | **PASS** |
| | **TC-MEM-15** | OpenFGA Engine Direct Check | `POST` | Dave `member` tuple revoked | `allowed: false` | **PASS** |
| **Phase 6: Dual-Role User Lifecycle & Zombie Prevention** | **TC-DUAL-01** | `/api/v0/authz/groups/{alpha_id}/users` | `POST` | Owner Alice adds herself as member | `200 OK` | **PASS** |
| | **TC-DUAL-02** | OpenFGA Engine Direct Check | `POST` | Alice dual roles (both owner and member) | `owner=true, member=true` | **PASS** |
| | **TC-DUAL-03** | PostgreSQL `group_members` Table | `SELECT` | Distinct composite rows for role=1 and role=0 | `count = 2` | **PASS** |
| | **TC-DUAL-04** | `/api/v0/authz/groups/{alpha_id}/users` | `GET` | Dual-role Alice reads members | `200 OK` | **PASS** |
| | **TC-DUAL-05** | `/api/v0/authz/groups/{alpha_id}/users/alice@canonical.com` | `DELETE` | Alice removes herself from members | `200 OK` | **PASS** |
| | **TC-DUAL-06** | OpenFGA Engine Direct Check | `POST` | Alice `member` tuple revoked | `allowed: false` | **PASS** |
| | **TC-DUAL-07** | OpenFGA Engine Direct Check | `POST` | Alice `owner` tuple PRESERVED (No Zombie) | `allowed: true` | **PASS** |
| | **TC-DUAL-08** | PostgreSQL `group_members` Table | `SELECT` | Alice owner row (role=1) retained | `role = 1` | **PASS** |
| | **TC-DUAL-09** | `/api/v0/authz/groups/{alpha_id}` | `PUT` | Alice retains full owner update access | `200 OK` | **PASS** |
| | **TC-DUAL-10** | `/api/v0/authz/groups/{alpha_id}/users` | `GET` | Alice retains full owner member list access | `200 OK` | **PASS** |
| **Phase 7: Idempotency & Edge Robustness** | **TC-IDEMP-01** | `/api/v0/authz/groups/{alpha_id}/users` | `POST` | Alice adds Bob again (Duplicate member) | `200 OK` | **PASS** |
| | **TC-IDEMP-02** | `/api/v0/authz/groups/{alpha_id}/users/nonexistent@canonical.com` | `DELETE` | Alice deletes non-existent user | `200 OK` | **PASS** |
| **Phase 8: Multi-Tenant ABAC Isolation** | **TC-TENANT-01** | OpenFGA Engine Direct Check | `POST` | Alice owner with matching tenant context | `allowed: true` | **PASS** |
| | **TC-TENANT-02** | OpenFGA Engine Direct Check | `POST` | Alice owner with mismatched tenant context | `allowed: false` | **PASS** |
| **Phase 9: Session Eviction & Revocation Defense** | **TC-SESS-01** | `/api/v0/authz/groups` | `GET` | Evicted session cookie (deleted from Valkey) | `403 Forbidden` | **PASS** |
| **Phase 10: Group Deletion Security & Cascading Cleanup** | **TC-DEL-01** | `/api/v0/authz/groups/{alpha_id}` | `DELETE` | Alice (Owner of Alpha) | `403 Forbidden` | **PASS** |
| | **TC-DEL-02** | `/api/v0/authz/groups/{alpha_id}` | `DELETE` | Bob (Member of Alpha) | `403 Forbidden` | **PASS** |
| | **TC-DEL-03** | `/api/v0/authz/groups/{alpha_id}` | `DELETE` | Charlie (Owner of Beta) | `403 Forbidden` | **PASS** |
| | **TC-DEL-04** | `/api/v0/authz/groups/{alpha_id}` | `DELETE` | Mallory (Outsider) | `403 Forbidden` | **PASS** |
| | **TC-DEL-05** | `/api/v0/authz/groups/{alpha_id}` | `DELETE` | Domain Admin | `200 OK` | **PASS** |
| | **TC-DEL-06** | `/api/v0/authz/groups/{beta_id}` | `DELETE` | Domain Admin | `200 OK` | **PASS** |
| | **TC-DEL-07** | OpenFGA Engine Direct Read | `POST` | Zero tuple leaks on Alpha & Beta | `0 remaining` | **PASS** |
| **Phase 11: Global User Group Queries** | **TC-USR-01** | `/api/v0/authz/users/alice@canonical.com/groups` | `GET` | Domain Admin | `200 OK` | **PASS** |
| | **TC-USR-02** | `/api/v0/authz/users/alice@canonical.com/groups` | `GET` | Alice | `403 Forbidden` | **PASS** |
| | **TC-USR-03** | `/api/v0/authz/users/alice@canonical.com/groups` | `GET` | Mallory | `403 Forbidden` | **PASS** |

---

### 7.3 Empirical Learnings & Operational Insights

During the development and hardening of the automated E2E test harness, five critical operational properties were identified and addressed:

#### 1. Asynchronous Event Synchronization & Test Polling Semantics
- **Pipeline Latency**: Hook Service produces Protobuf events to Kafka synchronously during HTTP mutations, but the Cerberus background worker operates on a 500ms polling interval (`WORKER_POLL_INTERVAL=500ms`). End-to-end sync to OpenFGA typically completes within 400ms – 1200ms.
- **Flakiness Avoidance**: Static `sleep` calls (e.g. 1.0s) cause race conditions when multiple mutations occur in rapid succession. The test suite implements deterministic polling loops (up to 5s at 250ms intervals) querying the OpenFGA `/check` or `/read` API, guaranteeing 100% deterministic assertion results.

#### 2. Valkey JWKS Cache Incoherency Prevention
- **Stale Key Trap**: STS generates active asymmetric signing keys stored in PostgreSQL and caches public keys in Valkey under `jwks:all` with a 10-minute TTL.
- **Root Cause & Fix**: When PostgreSQL databases are wiped and recreated between test runs, STS generates a new key pair, but Valkey previously retained the stale JWKS cache. This caused Cerberus token verification failures (`failed to verify id token signature`).
- **Operational Standard**: The automated setup script explicitly flushes Valkey caches (`valkey-cli flushall`) during environment bootstrap, ensuring key coherency across consecutive runs.

#### 3. Cascading Group Deletion Zero-Leak Property
- **Decommissioning Flow**: When Domain Admin calls `DELETE /api/v0/authz/groups/{id}`, Hook Service reads all members and owners of the group from `group_members`, emits bulk revocation events to Kafka, deletes the database records, and Cerberus evicts all OpenFGA tuples.
- **Audit Verification**: Test assertion `TC-DEL-07` directly queries OpenFGA with `{"tuple_key": {"object": "group-in-claim:<id>"}}` to verify zero orphan tuples remain, guaranteeing complete resource decommissioning without relation leakage.

#### 4. Elimination of Custom Headers & User Impersonation Defense
- **Security Vulnerability Remediated**: Custom identity headers (such as `X-User-Id` or `X-Forwarded-User`) pose severe user impersonation risks if an external client can inject them and an upstream service trusts them.
- **Zero-Trust Enforcement**: The Hook Service `UserContextMiddleware` strictly parses only cryptographically verified `Authorization: Bearer <jwt>` tokens injected by Envoy after STS session exchange. Custom identity headers like `X-User-Id` are completely stripped/ignored. Tests `TC-AUTH-04` through `TC-AUTH-07` verify that neither unauthenticated nor authenticated callers can forge identity or elevate privileges via custom headers or injected Bearer tokens.

#### 5. Dual-Role Integrity & Composite Primary Keys
- **The Desync / Zombie Tuple Dilemma**: When an Owner adds themselves as a Member, a single `(group_id, user_id)` primary key previously forced an update that collapsed or overwrote their owner role. Subsequent removal via the members endpoint deleted the entire record while leaving OpenFGA with an orphaned zombie owner tuple that could never be cleaned up.
- **Solution Verification**: By adopting a composite primary key `PRIMARY KEY (group_id, role, user_id)` with explicit role-scoped queries (`WHERE role = $X`), PostgreSQL and OpenFGA maintain independent, synchronized lifecycles for both Owner (`role=1`) and Member (`role=0`). Phase 6 (`TC-DUAL-01` through `TC-DUAL-10`) thoroughly verifies that removing oneself as a member revokes the OpenFGA `member` relation while keeping the `owner` relation and database row intact.

---

## 8. Troubleshooting & Diagnostics

### 1. Inspecting In-Flight gRPC Check Requests
Run Cerberus with `LOGGING_LEVEL=debug` to observe the decision pipeline:
```bash
# Check log entries for:
# - "Check request received" method=POST path=/api/v0/authz/groups
# - "Calling STS ExchangeSession"
# - "BatchCheck executed" allowed=true/false
# - "Authorization decision" result=allow/deny
tail -f logs/centralized-authz/cerberus-serve.log
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
Cerberus includes a built-in debugging command to verify authorization evaluation without running Envoy:
```bash
./bin/app authz check \
  --cookie "session=$ADMIN_COOKIE" \
  --method "POST" \
  --path "/api/v0/authz/groups" \
  --host "localhost"
```
