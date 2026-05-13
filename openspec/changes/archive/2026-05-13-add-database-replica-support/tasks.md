## 1. Configuration

- [x] 1.1 Add `ReplicaDSN`, `ReplicaDBMaxConns`, `ReplicaDBMinConns`, `ReplicaDBMaxConnLifetime`, `ReplicaDBMaxConnIdleTime`, `MaxReplicaLagMs`, and `ReplicaPoolSizeMultiplier` fields to `config.EnvSpec` with appropriate defaults (ReplicaDSN defaults to empty string, MaxReplicaLagMs defaults to 1000)
- [x] 1.2 Add `ReplicaDSN`, `ReplicaMaxConns`, `ReplicaMinConns`, `ReplicaMaxConnLifetime`, `ReplicaMaxConnIdleTime`, `MaxReplicaLagMs`, and `ReplicaPoolSizeMultiplier` fields to `db.Config`

## 2. DBClient Replica Pool

- [x] 2.1 Add `replicaPool *pgxpool.Pool`, `replicaDB *sql.DB`, `replicaRunner sq.BaseRunner`, and `replicaLagMs int64` fields to `DBClient` struct
- [x] 2.2 Add replica pool initialization logic to `NewDBClient` — create a second `pgxpool.Pool` and `sql.DB` when `Config.ReplicaDSN` is non-empty; fall back to primary-only mode with a warning on failure
- [x] 2.3 Update `DBClient.Close()` to close replica `sql.DB` and `pgxpool.Pool` if present
- [x] 2.4 Add a background goroutine to periodically query `pg_stat_replication` and cache the current replica lag in `replicaLagMs`

## 3. Read-Only Context Key

- [x] 3.1 Define an unexported `ReadOnlyContextKey` struct and context helper functions (`contextWithReadOnly`, `readOnlyFromContext`) in `internal/db/`
- [x] 3.2 Update `TransactionMiddleware` to inject the read-only context key for `GET` and `HEAD` requests
- [x] 3.3 Add a public `WithReadOnly(ctx context.Context) context.Context` helper for gRPC and internal service-to-service callers

## 4. Statement Routing

- [x] 4.1 Update `DBClient.Statement(ctx)` to check for read-only context key after transaction checks — route to `replicaRunner` when present and replica pool is available
- [x] 4.2 Add lag check before routing: if `replicaLagMs > MaxReplicaLagMs`, fall back to primary runner and log a warning
- [x] 4.3 Ensure `WithTx`, `BeginTx`, and `TxStatement` always use the primary pool regardless of read-only context

## 5. Wiring

- [x] 5.1 Update `cmd/serve.go` to map replica env spec fields into `db.Config` and pass them to `NewDBClient`

## 6. Monitoring

- [x] 6.1 Add Prometheus counter `hook_service_replica_queries_total` for queries routed to the replica
- [x] 6.2 Add Prometheus gauge `hook_service_replica_lag_ms` for current replication lag
- [x] 6.3 Add Prometheus counter `hook_service_primary_fallback_total` for fallbacks to the primary pool
- [x] 6.4 Log warnings for high lag or repeated fallbacks

## 7. Tests

- [x] 7.1 Add unit tests for `Statement()` routing: read-only context + replica pool → replica runner; read-only context + no replica → primary runner; transaction context overrides read-only; no flags → primary runner
- [x] 7.2 Add unit tests for `TransactionMiddleware`: GET/HEAD injects read-only key; POST does not inject read-only key
- [x] 7.3 Add unit test for `WithReadOnly(ctx)` helper: sets and retrieves read-only context key
- [x] 7.4 Add unit test for `NewDBClient` with empty `ReplicaDSN` verifying primary-only mode
- [x] 7.5 Add unit test for `Close()` with and without replica pool
- [x] 7.6 Add integration test for replication lag handling using testcontainers with primary/replica PostgreSQL setup
- [x] 7.7 Add integration test for metrics validation (replica queries, lag, fallback counters)
- [x] 7.8 Run `go vet ./...` and `go test -race ./...` — zero failures
