# ADR-001: Remove Salesforce Integration from Hot Path

**Status**: Accepted  
**Date**: 2026-02-25  
**Deciders**: Development Team  
**Issue**: [#146](https://github.com/canonical/hook-service/issues/146)

## Context

The hook-service was performing real-time Salesforce API calls during the Hydra token hook flow to resolve user groups. This had several problems:

- **Latency**: Each token request incurred a round-trip to Salesforce, adding 200-500ms to the token issuance flow.
- **Reliability**: Salesforce API outages or rate limits caused token hook failures, blocking user authentication entirely.
- **Complexity**: The service had a runtime dependency on Salesforce credentials and connectivity, complicating deployments.
- **Coupling**: The `Service.FetchUserGroups` method aggregated results from multiple `ClientInterface` implementations (Salesforce + local DB), making the hot path unnecessarily complex.

## Decision

Remove the direct Salesforce API integration from the token hook hot path. Instead:

1. Use **only the local database** (`group_members` table) for group lookups during token processing via the `StorageHookGroupsClient`.
2. Introduce a **CLI `import` command** (`hook-service import --driver salesforce`) that batch-imports user-group mappings from Salesforce into the database.

## Rationale

### Performance
- Database lookups are sub-millisecond vs 200-500ms for Salesforce API calls.
- Token hook latency is now deterministic and under the service's control.

### Reliability
- Token processing no longer depends on external API availability.
- Salesforce outages don't affect authentication flows.

### Operational Simplicity
- Import can be scheduled independently (cron, Juju action).
- Data is always available locally, even during network partitions.

### Separation of Concerns
- Data ingestion (import) is separated from data consumption (token hook).
- Each concern can be scaled, monitored, and debugged independently.

## Alternatives Considered

### Keep Salesforce in the hot path with caching
- ✅ No data staleness
- ❌ Still depends on Salesforce for cold starts/cache misses
- ❌ Cache invalidation complexity
- ❌ Higher memory usage

### Background sync worker within the service
- ✅ Automatic sync
- ❌ Adds operational complexity (health checks, failure handling)
- ❌ Hard to debug sync issues
- ❌ Memory overhead from running a goroutine

### gRPC endpoint for import
- ✅ Can be triggered by external orchestrators
- ❌ Over-engineered for a batch operation
- ❌ Requires authentication/authorization infrastructure

## Consequences

### Positive
- Token hook latency reduced significantly
- No external API dependency during authentication
- Simpler deployment: `serve` no longer needs Salesforce credentials
- CLI approach works with Juju actions and cron jobs

### Negative
- **Data staleness**: Groups are only as fresh as the last import. This is acceptable because group memberships change infrequently (quarterly HR cycles).
- **Breaking change**: Deployments relying on real-time Salesforce resolution must switch to periodic imports.

### Neutral
- Salesforce credentials are still needed, but only by the `import` command.
- `SALESFORCE_ENABLED` env var is removed from the serve path.

## Implementation Notes

### CLI Usage

```bash
# Import from Salesforce
hook-service import --driver salesforce --dsn "postgres://..." \
  --domain sf.example.com \
  --consumer-key KEY \
  --consumer-secret SECRET
```

### Architecture

```
┌─────────────────┐     ┌──────────────┐     ┌────────────┐
│  CLI: import     │────▶│  Importer     │────▶│  Database   │
│  --driver sf     │     │  Service      │     │  groups +   │
└─────────────────┘     └──────┬───────┘     │  members   │
                               │              └────────────┘
                        ┌──────▼───────┐            ▲
                        │  Salesforce   │            │
                        │  Driver       │     ┌──────┴───────┐
                        └──────┬───────┘     │  serve:       │
                               │              │  token hook   │
                        ┌──────▼───────┐     │  reads DB     │
                        │  Salesforce   │     │  only         │
                        │  API          │     └──────────────┘
                        └──────────────┘
```

### Key Files Changed
- `cmd/import.go` — New CLI command
- `internal/importer/` — Importer service with driver interface
- `cmd/serve.go` — Removed Salesforce wiring
- `pkg/web/router.go` — Removed Salesforce parameter
- `pkg/hooks/salesforce.go` — Deleted

## References

- [GitHub Issue #146](https://github.com/canonical/hook-service/issues/146)
- [go-salesforce library](https://github.com/k-capehart/go-salesforce)
