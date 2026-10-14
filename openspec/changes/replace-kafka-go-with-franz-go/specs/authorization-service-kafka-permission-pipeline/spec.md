## Purpose

Provide a Kafka producer infrastructure in `internal/kafka/` using `twmb/franz-go` capable of publishing protobuf `PermissionUpdateEnvelope` messages for authorization synchronization. The publisher uses native SASL OAUTHBEARER authentication with KIP-368 re-authentication for non-produce connections, bounded queuing, and non-blocking asynchronous dispatch with `NoAck`.

Key decisions:
- Kafka producer implementation in `internal/kafka/` using `twmb/franz-go` configured for asynchronous, non-blocking publication with `RequiredAcks(NoAck())` using `TryProduce` and bounded memory queuing via `MaxBufferedRecords(10000)` (alternative: synchronous blocking `ProduceSync` or blocking `Produce` rejected to preserve sub-millisecond webhook latency and prevent caller goroutine starvation during broker outages; upgrading to `LeaderAck` deferred).
- Direct encapsulation of `*kgo.Client` within `PermissionPublisher` (alternative: retaining `KafkaWriterInterface` rejected to eliminate leaky vendor types and avoid interface pollution).
- Native SASL OAUTHBEARER via `twmb/franz-go/pkg/sasl/oauth` eliminates custom RFC 7628 state machines and automatically supports KIP-368 session re-authentication for non-produce connections (alternative: custom SASL mechanism rejected to avoid redundant maintenance). With `NoAck`, KIP-368 in-place re-auth applies to non-produce connections only. Produce connections are re-dialed on session expiry. Broker-side produce errors are not visible.
- Bounded network retries and lifetime: records retry on transient errors up to 10 attempts bounded by `RecordDeliveryTimeout(30s)` (alternative: infinite retries or unbounded contexts rejected to bound memory buffering).
- Automatic cached token invalidation on broker SASL authentication failure via `HookBrokerConnect` matching `kerr.SaslAuthenticationFailed` (alternative: passive TTL expiration rejected to avoid repeated handshake failures on revoked tokens).
- Standby operation: The publisher is wired into the application lifecycle in `cmd/serve.go`. Under coarse-grained RBAC, group management operations operate directly on PostgreSQL.
- Isolation: Kafka availability or publisher errors have zero impact on PostgreSQL database transactions.

Non-goals:
- Upgrading to `LeaderAck` or `AllISRAcks` durability in this iteration.
- Synchronous blocking socket writes on publish calls.
- Adopting `twmb/franz-go/plugin/kotel` OpenTelemetry plugin at this time.
- Consumer implementation (scoped to `authorization-service`).

Backward compatibility:
- When `KAFKA_AUTH_TYPE` is unset or set to `none`, `hook-service` preserves unauthenticated plain TCP transport with zero breaking changes.
- All existing configuration environment variables (`KAFKA_BROKERS`, `KAFKA_AUTH_TYPE`, `KAFKA_OAUTH_*`, `KAFKA_TLS_*`) maintain full compatibility.

## MODIFIED Requirements

### Requirement: Kafka permission publisher infrastructure
The system SHALL provide a Kafka producer in `internal/kafka/` that implements `PermissionPublisherInterface` capable of publishing `PermissionUpdateEnvelope` protobuf messages to the federated permission topic.

#### Scenario: Publisher initialization with configured brokers
- **WHEN** hook-service starts with `KAFKA_BROKERS` configured and `KAFKA_AUTH_TYPE=none`
- **THEN** a `PermissionPublisher` instance is initialized using `twmb/franz-go` with asynchronous delivery, bounded retries (10), tracing, and Prometheus metrics
- **THEN** unauthenticated plain TCP transport connections are established to the brokers

#### Scenario: Publisher initialization without brokers
- **WHEN** hook-service starts without `KAFKA_BROKERS` configured
- **THEN** a no-op publisher is provided and service startup proceeds normally without error

#### Scenario: Publisher initialization with SASL/OAUTHBEARER transport
- **WHEN** hook-service starts with `KAFKA_BROKERS` configured and `KAFKA_AUTH_TYPE=oauthbearer`
- **THEN** the client is initialized with native SASL OAUTHBEARER using the OAuth token source and KIP-368 re-authentication enabled
- **THEN** message publication completes asynchronously across authenticated connections

#### Scenario: Standby publish capabilities with non-blocking dispatch
- **WHEN** `PublishWrite`, `PublishDelete`, or `PublishOperations` is called on the publisher
- **THEN** a `PermissionUpdateEnvelope` with the corresponding operation, unique `message_id`, and idempotency key is formatted and enqueued asynchronously via `TryProduce` with `RequiredAcks(NoAck())`
- **THEN** the publish call returns immediately to the caller without blocking on Kafka broker network transmission or client buffer limits

#### Scenario: Buffer saturation fail-fast error logging
- **WHEN** the publisher's internal buffer reaches capacity (`MaxBufferedRecords=10000`)
- **THEN** `TryProduce` fails fast without blocking caller threads and delivers `ErrMaxBuffered` to the completion callback to log the error

#### Scenario: Asynchronous delivery completion error logging
- **WHEN** the background Kafka producer fails delivery of a message
- **THEN** the delivery promise callback logs the delivery error via the logger interface

#### Scenario: Broker SASL authentication failure token invalidation
- **WHEN** broker authentication fails with SASL authentication failure
- **THEN** the broker connect hook invalidates the cached OAuth token in the token source so subsequent retries fetch a fresh token

#### Scenario: Clean shutdown and buffer flush
- **WHEN** the application receives a termination signal (`SIGTERM`/`SIGINT`)
- **THEN** the publisher flushes in-flight records with a bounded timeout (5 seconds) and closes broker connections cleanly
