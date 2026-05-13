## ADDED Requirements

### Requirement: Read-only queries route to replica

The system SHALL route read-only database queries to a read-only replica connection pool when:
- A read-only context key is present in the request context
- No transaction or lazy transaction is bound to the context
- A replica connection pool is available and healthy

#### Scenario: GET request routes to replica
- **WHEN** an HTTP GET request is received
- **AND** no transaction is required (per `TransactionMiddleware`)
- **AND** a replica connection pool is configured and healthy
- **THEN** the query SHALL execute on the replica pool

#### Scenario: Transaction context overrides read-only routing
- **WHEN** a read-only context key is present
- **AND** a transaction or lazy transaction is bound to the context
- **THEN** the query SHALL execute on the primary pool

#### Scenario: Fallback to primary when replica unavailable
- **WHEN** a read-only context key is present
- **AND** no transaction is bound to the context
- **AND** the replica connection pool is unavailable or unhealthy
- **THEN** the query SHALL execute on the primary pool

#### Scenario: Replication lag fallback
- **WHEN** a read-only context key is present
- **AND** no transaction is bound to the context
- **AND** the replica's reported lag exceeds `MaxReplicaLagMs`
- **THEN** the query SHALL execute on the primary pool

### Requirement: Replica configuration

The system SHALL support configuration of a read-only replica via:
- `REPLICA_DSN`: Connection string for the replica
- `REPLICA_MAX_CONNS`: Maximum connections in the replica pool
- `REPLICA_MIN_CONNS`: Minimum connections in the replica pool
- `MAX_REPLICA_LAG_MS`: Maximum allowed replication lag before falling back to primary

#### Scenario: Replica disabled when DSN empty
- **WHEN** `REPLICA_DSN` is empty or unset
- **THEN** the system SHALL operate in primary-only mode

#### Scenario: Replica pool sized independently
- **WHEN** `REPLICA_MAX_CONNS` is set
- **THEN** the replica connection pool SHALL use this value for maximum connections

### Requirement: Monitoring and metrics

The system SHALL expose the following metrics for replica usage:
- `replica_queries_total`: Counter of queries routed to the replica
- `replica_lag_ms`: Gauge of current replication lag in milliseconds
- `primary_fallback_count`: Counter of fallbacks to the primary pool

#### Scenario: Metrics incremented on replica query
- **WHEN** a query executes on the replica pool
- **THEN** the `replica_queries_total` metric SHALL increment by 1

#### Scenario: Lag metric reflects replication status
- **WHEN** replication lag is queried from PostgreSQL
- **THEN** the `replica_lag_ms` metric SHALL update to the current lag value

### Requirement: Backward compatibility

The system SHALL maintain full backward compatibility when:
- No replica is configured (`REPLICA_DSN` empty)
- Existing transaction behavior is preserved
- All API contracts remain unchanged

#### Scenario: Primary-only mode behavior
- **WHEN** `REPLICA_DSN` is empty
- **THEN** all queries SHALL execute on the primary pool
- **AND** no metrics SHALL be exposed for replica usage

#### Scenario: Transaction behavior unchanged
- **WHEN** a transaction is active
- **THEN** all queries within the transaction SHALL execute on the primary pool
