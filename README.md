# Hook Service

[![CI](https://github.com/canonical/hook-service/actions/workflows/ci.yaml/badge.svg)](https://github.com/canonical/hook-service/actions/workflows/ci.yaml)
[![codecov](https://codecov.io/gh/canonical/hook-service/graph/badge.svg?token=YOUR_TOKEN)](https://codecov.io/gh/canonical/hook-service)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/canonical/hook-service/badge)](https://securityscorecards.dev/viewer/?platform=github.com&org=canonical&repo=hook-service)
[![pre-commit](https://img.shields.io/badge/pre--commit-enabled-brightgreen?logo=pre-commit)](https://github.com/pre-commit/pre-commit)
[![Conventional Commits](https://img.shields.io/badge/Conventional%20Commits-1.0.0-%23FE5196?logo=conventionalcommits&logoColor=white)](https://conventionalcommits.org)
[![GitHub Release](https://img.shields.io/github/v/release/canonical/hook-service)](https://github.com/canonical/hook-service/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/canonical/hook-service.svg)](https://pkg.go.dev/github.com/canonical/hook-service)

This is the Canonical Identity Platform Hook Service used for handling Hydra Hooks and managing groups. It integrates with Ory Kratos for identity management, Ory Hydra for OAuth2/OIDC flows, and OpenFGA for fine-grained authorization. User group data is stored locally in PostgreSQL and can be bulk-imported from external sources (e.g. Salesforce) via the CLI.

## Environment Variables

The application is configured via environment variables.

| Variable | Description | Default |
|----------|-------------|---------|
| `OTEL_GRPC_ENDPOINT` | OTel gRPC endpoint for traces | |
| `OTEL_HTTP_ENDPOINT` | OTel HTTP endpoint for traces | |
| `TRACING_ENABLED` | Enable tracing | `true` |
| `LOG_LEVEL` | Log level (`debug`, `info`, `warn`, `error`) | `error` |
| `DEBUG` | Enable debug mode | `false` |
| `PORT` | HTTP server port | `8080` |
| `GRPC_PORT` | Native gRPC server port for internal groups mapping API | `9090` |
| `GRPC_MAX_CONCURRENT_STREAMS` | Max concurrent streams allowed per gRPC connection | `100` |
| `API_TOKEN` | Token for API authentication | |
| `OPENFGA_API_SCHEME` | OpenFGA API scheme | |
| `OPENFGA_API_HOST` | OpenFGA API host | |
| `OPENFGA_API_TOKEN` | OpenFGA API token | |
| `OPENFGA_STORE_ID` | OpenFGA store ID | |
| `OPENFGA_AUTHORIZATION_MODEL_ID` | OpenFGA authorization model ID | |
| `AUTHORIZATION_ENABLED` | Enable authorization middleware | `false` |
| `OPENFGA_WORKERS_TOTAL` | Total OpenFGA workers | `150` |
| `HOOK_MAX_CONCURRENT` | Max concurrent token hook requests processed by the worker pool | `150` |
| `AUTHENTICATION_ENABLED` | Enable JWT authentication for Groups/Authz APIs | `true` |
| `AUTHENTICATION_ISSUER` | Expected JWT issuer (e.g., `https://auth.example.com`) | |
| `AUTHENTICATION_JWKS_URL` | Optional explicit JWKS URL (overrides OIDC discovery) | |
| `AUTHENTICATION_ALLOWED_SUBJECTS` | Comma-separated list of allowed JWT subjects | |
| `AUTHENTICATION_REQUIRED_SCOPE` | Required scope for access (e.g., `hook-service:admin`) | |
| `DSN` | Database connection string (Required) | |
| `DB_MAX_CONNS` | Max DB connections | `25` |
| `DB_MIN_CONNS` | Min DB connections | `2` |
| `DB_MAX_CONN_LIFETIME` | Max DB connection lifetime | `1h` |
| `DB_MAX_CONN_IDLE_TIME` | Max DB connection idle time | `30m` |
| `REPLICA_DSN` | Replica database connection string (empty = primary-only) | |
| `REPLICA_DB_MAX_CONNS` | Max replica pool connections | `25` |
| `REPLICA_DB_MIN_CONNS` | Min replica pool connections | `2` |
| `REPLICA_DB_MAX_CONN_LIFETIME` | Max replica connection lifetime | `1h` |
| `REPLICA_DB_MAX_CONN_IDLE_TIME` | Max replica connection idle time | `30m` |
| `MAX_REPLICA_LAG_MS` | Max replication lag before falling back to primary | `1000` |
| `REPLICA_POOL_SIZE_MULTIPLIER` | Multiplier for sizing replica pool relative to primary pool | `1.0` |

## Features

### JWT Authentication

The Groups and Authorization APIs (`/api/v0/authz`) are protected by JWT authentication middleware. When enabled, all requests to these endpoints must include a valid JWT token in the `Authorization` header.

**Configuration:**

- `AUTHENTICATION_ENABLED`: Set to `true` to enable JWT authentication (default: `true`)
- `AUTHENTICATION_ISSUER`: The expected JWT issuer URL. Used for OIDC metadata discovery to fetch JWKS or to verify the `iss` claim when using manual JWKS URL
- `AUTHENTICATION_JWKS_URL`: (Optional) Explicit JWKS URL. If set, fetches keys from this URL instead of using OIDC discovery
- `AUTHENTICATION_ALLOWED_SUBJECTS`: Comma-separated list of allowed JWT `sub` claims
- `AUTHENTICATION_REQUIRED_SCOPE`: (Optional) A specific scope that grants access (e.g., `hook-service:admin`)

**JWKS Configuration:**

The middleware supports two modes for fetching JWKS:
1. **OIDC Discovery** (default): Discovers JWKS URL from `{AUTHENTICATION_ISSUER}/.well-known/openid-configuration`
2. **Manual JWKS URL**: If `AUTHENTICATION_JWKS_URL` is set, fetches keys directly from that URL

Use manual JWKS URL when the issuer is not an OIDC provider or when OIDC discovery is not available.

**Authorization Logic:**

A request is authorized if **either**:
1. The JWT's `sub` claim matches one of the `AUTHENTICATION_ALLOWED_SUBJECTS`, OR
2. The JWT's `scope` or `scp` claim contains the `AUTHENTICATION_REQUIRED_SCOPE`

**Usage:**

```bash
# Include JWT token in Authorization header
curl -H "Authorization: Bearer <jwt-token>" http://localhost:8080/api/v0/authz/groups
```

### Database Replica Support

Read-only database queries (HTTP `GET`/`HEAD` requests) can be routed to a PostgreSQL read replica to reduce primary load. When `REPLICA_DSN` is empty, the service operates in primary-only mode with no behavior changes.

**Routing rules:**

- `GET`/`HEAD` requests are routed to the replica pool when configured and healthy
- All transactions (`WithTx`, `BeginTx`, `TxStatement`) always use the primary pool
- If replica replication lag exceeds `MAX_REPLICA_LAG_MS`, queries fall back to the primary with a logged warning
- gRPC and internal callers can opt into replica routing using `db.WithReadOnly(ctx)`

**Monitoring metrics:**

| Metric | Type | Description |
|--------|------|-------------|
| `hook_service_replica_queries_total` | Counter | Queries routed to the replica |
| `hook_service_replica_lag_ms` | Gauge | Current replication lag in milliseconds |
| `hook_service_primary_fallback_total` | Counter | Fallbacks to the primary pool |

### gRPC Groups Mapping API

A native gRPC server runs on `GRPC_PORT` (default `9090`) for internal service-to-service communication. It exposes server-streaming RPCs for querying user-to-group mappings with tenant-scoped filtering, secured by a JWT stream interceptor.

**Service:** `hook.groups.v1.GroupsMappingService`

| RPC | Request | Response | Description |
|-----|---------|----------|-------------|
| `GetGroupsForUser` | `user_id`, optional `tenant_id` | `stream GroupMapping` | Streams all groups for a user within a tenant |
| `GetUsersInGroup` | `group_id`, optional `tenant_id` | `stream UserMapping` | Streams all users in a group within a tenant |

**Streaming behavior:**

- Results are streamed one message at a time, maintaining O(1) memory per request
- Each database query is wrapped in a `context.WithTimeout` (30s default) to bound stream duration
- Client disconnect aborts the database cursor immediately via context cancellation
- All streams require a valid JWT in the `authorization` gRPC metadata (`Bearer <token>`)

**Proto definition:** `proto/hook/groups/v1/mapping.proto`

### Import Command

The `import` CLI command batch-imports user-group mappings from an external source into the local database. This decouples data ingestion from the token hook hot path.

```bash
# Import from Salesforce
hook-service import --driver salesforce --dsn "postgres://user:pass@host:5432/db" \
  --domain sf.example.com \
  --consumer-key KEY \
  --consumer-secret SECRET
```

Salesforce credentials can also be provided via environment variables (`SALESFORCE_DOMAIN`, `SALESFORCE_CONSUMER_KEY`, `SALESFORCE_CONSUMER_SECRET`). Flags take precedence over env vars.

| Flag | Description |
|------|-------------|
| `--driver` | Import driver (required, currently: `salesforce`) |
| `--dsn` | PostgreSQL connection string (required) |
| `--domain` | Salesforce domain |
| `--consumer-key` | Salesforce consumer key |
| `--consumer-secret` | Salesforce consumer secret |

## Development Setup

### Prerequisites

- Go 1.25+
- Make
- Docker
- Rockcraft (for building the container image)

### Build

To build the application binary:

```bash
make build
```

This produces a binary named `app` in the current directory.

### Container

To build the OCI image using Rockcraft:

```bash
rockcraft pack
```

This will produce a `.rock` file which can be imported into Docker.

### E2E Tests

The E2E tests are located in `tests/e2e` and run in a separate module to isolate test dependencies.

To run the E2E tests:

```bash
make test-e2e
```

This command will:
1. Switch to the `tests/e2e` directory.
2. Spin up the required environment (Postgres, Hydra, Kratos, OpenFGA) using Testcontainers.
3. Run the tests.

### Local Development Environment

You can start a full local development environment including dependencies:

```bash
make dev
# or
./start.sh
```

This starts Kratos, Hydra, OpenFGA, Postgres, and Mailslurper using `docker-compose.dev.yml`.

## Token Claims Configuration

The service adds user groups to OAuth2/OIDC tokens via the Hydra token hook. By default, Hydra would nest custom claims under an `ext` namespace. To ensure groups appear as a top-level claim in access tokens, the Hydra configuration includes:

```yaml
oauth2:
  allowed_top_level_claims:
    - groups
  mirror_top_level_claims: false
```

This configuration is set in `docker/hydra/hydra.yml` for local development. For production deployments, ensure your Hydra instance is configured with these settings to make the `groups` claim accessible at the top level of the token payload.

## Architecture Decision Records

Key technical decisions are documented in [`docs/adr/`](docs/adr/README.md).

## Security

Please see [SECURITY.md](https://github.com/canonical/hook-service/blob/main/SECURITY.md) for guidelines on reporting security issues.
