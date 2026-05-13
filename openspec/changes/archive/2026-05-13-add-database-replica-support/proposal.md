## Why

The hook-service routes all database reads and writes through a single `pgxpool.Pool` to the primary. The `FetchUserGroups` path — invoked on every OAuth token issuance request from Ory Hydra — is read-heavy. As read traffic grows, the primary becomes a bottleneck, increasing latency for both reads and writes. Adding replica support lets us offload read queries to a read-only replica, reducing primary load and improving read latency and availability.

## What Changes

- Add a second `pgxpool.Pool` + `sql.DB` to `DBClient` for replica connections, configurable via `REPLICA_DSN` environment variable
- Introduce a `ReadOnlyContextKey` that `TransactionMiddleware` injects for `GET`/`HEAD` requests
- Add a `WithReadOnly(ctx)` helper for gRPC and internal callers to opt into replica routing
- Modify `DBClient.Statement(ctx)` to route to the replica pool when a read-only context is present, no transaction is bound, and the replica is available and not lagged
- Keep `WithTx`, `BeginTx`, and `TxStatement` always bound to the primary — transactions must never span pools
- Add `MaxReplicaLagMs` configuration: if the replica's replication lag exceeds the threshold, `Statement()` falls back to the primary
- Make replica support fully backward-compatible: when `REPLICA_DSN` is empty, `DBClient` behaves exactly as today
- Add `ReplicaDSN`, `ReplicaDBMaxConns`, `ReplicaDBMinConns`, `ReplicaDBMaxConnLifetime`, `ReplicaDBMaxConnIdleTime`, `MaxReplicaLagMs`, and `ReplicaPoolSizeMultiplier` to `db.Config` and `config.EnvSpec`
- Add Prometheus metrics for replica query count, replication lag, and primary fallbacks
- No schema migrations required — this is a connection routing change only
- No API contract changes — routing is transparent to callers

## Capabilities

### New Capabilities
- `db-replica-routing`: Routing of read-only database queries to a replica connection pool, with context-based read/write hinting, replication lag awareness, and graceful fallback to the primary when no replica is configured or the replica is degraded

### Modified Capabilities

## Impact

### Architectural Impact (internal/domain vs internal/infrastructure)

This change is confined to the **infrastructure layer** (`internal/db`, `internal/config`). No domain logic changes are required:

- **`internal/db` (Infrastructure)**: Core change — `DBClient` gains a second pool and `Statement()` gains routing logic. `TransactionMiddleware` injects a read-only context key. A `WithReadOnly(ctx)` helper enables gRPC and internal callers to opt into replica routing.
- **`internal/storage` (Infrastructure)**: No changes — `Storage` calls `DBClientInterface.Statement(ctx)` and is agnostic to which pool serves the query.
- **`pkg/groups`, `pkg/authorization`, `pkg/hooks` (Business logic / Delivery)**: No changes — these layers call `StorageInterface` or `DatabaseInterface` and never interact with `DBClient` directly.
- **`internal/config` (Infrastructure)**: New env vars for replica configuration.
- **`cmd/` (Application entry point)**: Wiring change to pass replica config into `db.Config`.

### API Contract Changes

**None.** `DBClientInterface`, `StorageInterface`, and all HTTP/gRPC API contracts remain unchanged. Replica routing is transparent.

### Schema Migrations

**None.** This change affects connection routing only. No database schema modifications are required.

### Performance Regressions

- **Risk**: Stale reads from the replica due to replication lag may cause read-after-write anomalies (e.g., a group created on the primary but not yet visible on the replica).
  → *Mitigation*: Mutations are always wrapped in transactions on the primary. Reads within a mutation request hit the primary via the transaction context. The `MaxReplicaLagMs` threshold proactively redirects to the primary when lag exceeds the configured bound.
- **Risk**: If the replica pool is undersized relative to read traffic, connection exhaustion on the replica could degrade read latency.
  → *Mitigation*: The replica pool is independently configurable. A `ReplicaPoolSizeMultiplier` heuristic simplifies sizing relative to the primary pool.
- **No regression for write path**: All writes and transactions continue to use the primary pool with identical behavior.

### Affected Code

| File | Change |
|------|--------|
| `internal/db/storage.go` | Add replica pool fields, initialization, `Statement()` routing, lag check, and `Close()` update |
| `internal/db/middleware.go` | Inject `ReadOnlyContextKey` for GET/HEAD; add `WithReadOnly(ctx)` helper |
| `internal/config/specs.go` | Add `ReplicaDSN`, `ReplicaDBMaxConns`, `ReplicaDBMinConns`, `ReplicaDBMaxConnLifetime`, `ReplicaDBMaxConnIdleTime`, `MaxReplicaLagMs` |
| `cmd/serve.go` | Map replica env spec fields into `db.Config` |

### Dependencies

No new dependencies. Uses existing `pgxpool`, `sql.DB`, and `squirrel` packages.
