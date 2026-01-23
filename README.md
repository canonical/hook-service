# Hook Service

[![CI](https://github.com/canonical/hook-service/actions/workflows/ci.yaml/badge.svg)](https://github.com/canonical/hook-service/actions/workflows/ci.yaml)
[![codecov](https://codecov.io/gh/canonical/hook-service/graph/badge.svg?token=YOUR_TOKEN)](https://codecov.io/gh/canonical/hook-service)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/canonical/hook-service/badge)](https://securityscorecards.dev/viewer/?platform=github.com&org=canonical&repo=hook-service)
[![pre-commit](https://img.shields.io/badge/pre--commit-enabled-brightgreen?logo=pre-commit)](https://github.com/pre-commit/pre-commit)
[![Conventional Commits](https://img.shields.io/badge/Conventional%20Commits-1.0.0-%23FE5196?logo=conventionalcommits&logoColor=white)](https://conventionalcommits.org)
[![GitHub Release](https://img.shields.io/github/v/release/canonical/hook-service)](https://github.com/canonical/hook-service/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/canonical/hook-service.svg)](https://pkg.go.dev/github.com/canonical/hook-service)

This is the Canonical Identity Platform Hook Service used for handling Hydra Hooks and managing groups. It integrates with Ory Kratos for identity management, Ory Hydra for OAuth2/OIDC flows, OpenFGA for fine-grained authorization, and optional Salesforce for group management.

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
| `API_TOKEN` | Token for API authentication | |
| `OPENFGA_API_SCHEME` | OpenFGA API scheme | |
| `OPENFGA_API_HOST` | OpenFGA API host | |
| `OPENFGA_API_TOKEN` | OpenFGA API token | |
| `OPENFGA_STORE_ID` | OpenFGA store ID | |
| `OPENFGA_AUTHORIZATION_MODEL_ID` | OpenFGA authorization model ID | |
| `SALESFORCE_ENABLED` | Enable Salesforce integration | `true` |
| `SALESFORCE_DOMAIN` | Salesforce domain | |
| `SALESFORCE_CONSUMER_KEY` | Salesforce consumer key | |
| `SALESFORCE_CONSUMER_SECRET` | Salesforce consumer secret | |
| `AUTHORIZATION_ENABLED` | Enable authorization middleware | `false` |
| `OPENFGA_WORKERS_TOTAL` | Total OpenFGA workers | `150` |
| `DSN` | Database connection string (Required) | |
| `DB_MAX_CONNS` | Max DB connections | `25` |
| `DB_MIN_CONNS` | Min DB connections | `2` |
| `DB_MAX_CONN_LIFETIME` | Max DB connection lifetime | `1h` |
| `DB_MAX_CONN_IDLE_TIME` | Max DB connection idle time | `30m` |

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

## Security

Please see [SECURITY.md](https://github.com/canonical/hook-service/blob/main/SECURITY.md) for guidelines on reporting security issues.
