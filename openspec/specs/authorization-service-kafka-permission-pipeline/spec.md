## Purpose

Provide a Kafka producer infrastructure in `internal/kafka/` capable of publishing protobuf `PermissionUpdateEnvelope` messages for authorization synchronization, supporting plain TCP or authenticated (SASL/OAUTHBEARER) and TLS-encrypted transport connections. Under the coarse-grained authorization model (`role:admin`), group lifecycle operations execute directly against PostgreSQL without publishing runtime permission events to Kafka. The Kafka producer infrastructure is maintained in standby mode, preserving architectural readiness for future event-driven authorization extensions.

Key decisions:
- Kafka producer implementation in `internal/kafka/` using `segmentio/kafka-go` configured for asynchronous, non-blocking publication (`Async: true`).
- Encapsulates `PermissionUpdateEnvelope` protobuf serialization, at-least-once delivery semantics, tracing, Prometheus metrics, and background delivery completion handling.
- Bounded network timeouts and retries: `MaxAttempts` (10) retries transient delivery failures, `ReadTimeout` (1s) bounds initial topic partition discovery, `WriteTimeout` (2s) bounds broker produce calls.
- Standby operation: The publisher is wired into the application lifecycle in `cmd/serve.go`. Under coarse-grained RBAC, group management operations (`pkg/groups/service.go`) operate directly on PostgreSQL.
- Isolation: Kafka availability or publisher errors have zero impact on PostgreSQL database transactions.
- Risk awareness: `segmentio/kafka-go` uses an unbounded slice for its internal batch queue. During an extended Kafka outage under write traffic, pending batches accumulate in memory without backpressure.
- Wire authenticated and encrypted `kafkago.Transport` into `NewKafkaWriter` while preserving the existing `PermissionPublisher` interface and non-blocking asynchronous delivery semantics.
- Configure `IdleTimeout: 5 * time.Minute` on the underlying transport to recycle idle connections, ensuring rotated OAuth tokens are picked up on subsequent dials.
- The publisher owns the transport it builds and releases its idle connections on `Close`, so no caller has to track transport lifetime alongside the publisher.
- Publisher selection is driven by one signal, the normalized broker list, so the publisher in use cannot disagree with the deployment mode reported to operators.

Non-goals:
- Publishing runtime permission events for group creation, deletion, or membership modifications under the coarse-grained RBAC model.
- Publishing events for token hook or app-to-group operations.
- Managing Kafka infrastructure deployment or topic lifecycle.
- Modifying the envelope schema (`PermissionUpdateEnvelope`) or publish method signatures.
- Changing the fire-and-forget asynchronous batching behavior (`Async=true`, `RequiredAcks=RequireNone`).

## Requirements

### Requirement: Kafka permission publisher infrastructure
The system SHALL provide a Kafka producer in `internal/kafka/` that implements `PermissionPublisherInterface` capable of publishing `PermissionUpdateEnvelope` protobuf messages to the permissions topic derived from the federated service name (`permissions.<federated-service-name>`) over unauthenticated or authenticated transports.

#### Scenario: Publisher initialization with configured brokers
- **WHEN** hook-service starts with `KAFKA_BROKERS` configured and `KAFKA_AUTH_TYPE=none`
- **THEN** a `PermissionPublisher` instance is initialized with asynchronous writer connections (`Async=true`, `MaxAttempts=10`), tracing, and Prometheus metrics using plain TCP transport
- **THEN** the publisher gracefully flushes pending batches and closes connections on application shutdown (`SIGTERM`/`SIGINT`)

#### Scenario: Publisher initialization without brokers
- **WHEN** hook-service starts without `KAFKA_BROKERS` configured
- **THEN** a noop publisher is provided and service startup proceeds normally without error

#### Scenario: Standby publish capabilities with non-blocking dispatch
- **WHEN** `PublishWrite`, `PublishDelete`, or `PublishOperations` is called on the publisher
- **THEN** a `PermissionUpdateEnvelope` with the corresponding operation, unique `message_id`, and idempotency key is formatted and enqueued asynchronously with `requiredAcks=RequireNone` (fire-and-forget)
- **THEN** the publish call returns immediately to the caller without blocking on Kafka broker network transmission

#### Scenario: Asynchronous delivery completion error logging
- **WHEN** the background Kafka writer fails delivery of a message batch
- **THEN** the completion handler logs the delivery error

#### Scenario: Publisher initialization with authenticated SASL/OAUTHBEARER transport
- **WHEN** hook-service starts with `KAFKA_BROKERS` configured and `KAFKA_AUTH_TYPE=oauthbearer`
- **THEN** the writer is constructed with a `kafkago.Transport` configured with the SASL/OAUTHBEARER mechanism and TLS configuration
- **THEN** message publication completes asynchronously across authenticated connections

#### Scenario: Publisher connection recycling on idle timeout
- **WHEN** a pooled broker connection has remained idle for the configured `IdleTimeout` (5 minutes)
- **THEN** the transport re-establishes the connection on the next publish call, executing a fresh SASL handshake with the currently valid OAuth token

#### Scenario: Transport released when the publisher is closed
- **WHEN** a publisher built with an authenticated or encrypted transport is closed
- **THEN** the underlying writer is closed and the transport's idle connections are released
- **THEN** closing an already closed publisher completes without error

#### Scenario: Publisher construction fails on unusable transport credentials
- **WHEN** hook-service starts with `KAFKA_BROKERS` configured and a broker or OAuth CA certificate file that cannot be read or parsed
- **THEN** publisher construction fails with an error naming the offending configuration
- **THEN** no publisher is returned and no partially initialized transport is left in place
