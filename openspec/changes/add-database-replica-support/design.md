## Context

The hook-service uses a single `DBClient` backed by a `pgxpool.Pool` wrapped in `sql.DB`. All reads and writes flow through `DBClient.Statement(ctx)`, which resolves to either a transaction-bound runner (if a `lazyTx` or `TxInterface` exists in the context) or the default `dbRunner` (the primary pool). The `TransactionMiddleware` already distinguishes read-only HTTP methods (GET/HEAD skip transaction wrapping) from mutating methods, but both paths hit the same pool.

Current call flow:

```
HTTP Request
  │
  ├─ GET/HEAD ──► TransactionMiddleware skips tx ──► Statement(ctx) ──► primaryPool
  │
  └─ Other ────► TransactionMiddleware wraps in tx ──► Statement(ctx) ──► tx (on primaryPool)
```

The `DBClient` struct holds `pool *pgxpool.Pool`, `db *sql.DB`, and `dbRunner sq.BaseRunner`. The `Statement(ctx)` method checks three layers: lazy transaction → regular transaction → default runner. Context keys `TxContextKey` and `LazyTxContextKey` are unexported struct types — the same pattern should be used for `ReadOnlyContextKey`.

## Goals / Non-Goals

**Goals:**
- Route read-only queries (no transaction in context, read-only context flag) to a replica pool when configured
- Preserve existing behavior exactly when no replica DSN is provided (zero-config backward compatibility)
- Ensure transactions never span pools — all `WithTx`/`BeginTx`/`TxStatement` operations remain bound to the primary
- Make the routing decision at the `DBClient` level so that the `Storage` layer and `pkg/` packages remain unchanged
- Support replication lag awareness via a configurable staleness threshold
- Enable gRPC and internal callers to opt into replica routing via `WithReadOnly(ctx)`
- Add monitoring for replica usage, lag, and fallbacks

**Non-Goals:**
- Read-after-write consistency guarantees for non-transactional reads on the replica (callers that need strong consistency must use transactions)
- Multi-replica selection or weighted routing (single replica DSN only)
- Automatic failover from replica to primary on replica query failure (errors surface immediately)
- Changes to `StorageInterface` or `DBClientInterface` signatures

## Decisions

### 1. Routing at `DBClient.Statement()` via context key

**Decision**: Inject a `ReadOnlyContextKey` into the context by the `TransactionMiddleware` for GET/HEAD requests. `Statement()` checks for this key and routes to the replica pool when present, unless a transaction is bound to the context. Add a public `WithReadOnly(ctx) context.Context` helper for gRPC and internal service-to-service callers.

**Alternatives considered**:
- *Separate `ReadOnlyStatement()` method*: Requires changing `DBClientInterface` and every read call site in `Storage`. Too invasive.
- *Routing at `Storage` level with separate primary/replica `DBClient`*: Duplicates infrastructure, breaks the single-client abstraction.
- *SQL-level routing via PgBouncer or ProxySQL*: Loses context-awareness — cannot know if a read is inside a transaction.

**Rationale**: Context-based routing is the least invasive approach. The middleware already knows the HTTP method; `Statement()` already inspects context for transactions. Adding one more context key keeps the change localized to `internal/db/`. The `WithReadOnly` helper ensures gRPC streams can also benefit from replica routing.

### 2. Single replica DSN with independent pool configuration

**Decision**: Add `ReplicaDSN`, `ReplicaMaxConns`, `ReplicaMinConns`, `ReplicaMaxConnLifetime`, `ReplicaMaxConnIdleTime`, `MaxReplicaLagMs`, and `ReplicaPoolSizeMultiplier` to `db.Config`. When `ReplicaDSN` is empty, no replica pool is created and all traffic goes to the primary.

**Alternatives considered**:
- *Shared pool config for primary and replica*: Read pools typically benefit from different sizing. Too restrictive.
- *Array of replica DSNs*: Over-engineering for current needs.

### 3. Replica pool initialization and failure handling

**Decision**: If the replica pool fails health check at startup, log a warning and fall back to primary-only mode. At runtime, if a replica query returns a connection error, `Statement()` does NOT retry on primary — it returns the error.

**Alternatives considered**:
- *Transparent retry on primary*: Can cause unexpected load spikes on the primary and hides misconfiguration.

### 4. Separate `sql.DB` for replica with own `pgxpool.Pool`

**Decision**: Create a distinct `pgxpool.Pool` + `sql.DB` for the replica, mirroring the primary setup. The `DBClient` holds both pools and selects between them.

**Rationale**: Keeps connection pool management clean and independent. Each pool can be sized, traced, and closed independently.

### 5. Replication Lag Awareness

**Decision**: Add a `MaxReplicaLagMs` configuration to `db.Config`. If the replica's reported lag exceeds this threshold, `Statement()` falls back to the primary pool.

**Implementation**:
- Use PostgreSQL's `pg_stat_replication` to query the replica's write-lag in milliseconds.
- Cache the lag value for a short duration (1 second) to avoid excessive queries.
- Log a warning when falling back to the primary due to excessive lag.

**Alternatives considered**:
- *No lag awareness*: Risks serving severely stale data.
- *Client-side lag handling*: Not scalable.

### 6. Monitoring and Metrics

**Decision**: Add Prometheus metrics:
- `hook_service_replica_queries_total`: Counter for queries routed to the replica.
- `hook_service_replica_lag_ms`: Gauge for current replication lag.
- `hook_service_primary_fallback_total`: Counter for fallbacks to the primary pool.

**Rationale**: Operators need visibility into replica health, query distribution, and fallback frequency.

## Risks / Trade-offs

- **Stale reads**: Reads routed to the replica may return data behind the primary due to replication lag. → *Mitigation*: `MaxReplicaLagMs` proactively redirects to primary when lag exceeds the threshold. Callers needing read-after-write consistency must use transactions (which always hit the primary).

- **Replica pool exhaustion under heavy read load**: If read traffic is significantly higher than the primary pool was sized for, the replica pool may exhaust connections. → *Mitigation*: Replica pool is independently configurable. `ReplicaPoolSizeMultiplier` simplifies sizing.

- **Increased startup complexity**: Two pools to initialize and health-check means slightly longer startup and more failure modes. → *Mitigation*: Replica is optional. If it fails to initialize, fall back to primary-only with a logged warning.

- **Context key collision**: The `ReadOnlyContextKey` could collide with other keys. → *Mitigation*: Use an unexported struct type (same pattern as existing `TxContextKey` and `LazyTxContextKey`).
