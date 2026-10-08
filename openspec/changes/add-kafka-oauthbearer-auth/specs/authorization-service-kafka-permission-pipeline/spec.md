## Purpose

Extend the Kafka permission publisher infrastructure in `internal/kafka/` to support authenticated and encrypted transport connections. When `hook-service` runs in platform mode with `KAFKA_BROKERS` configured, the publisher can be initialized with plain unauthenticated transport (for backward compatibility) or with SASL/OAUTHBEARER authentication and TLS transport encryption (for secured production clusters).

Key decisions:
- Wire authenticated and encrypted `kafkago.Transport` into `NewKafkaWriter` while preserving the existing `PermissionPublisher` interface and non-blocking asynchronous delivery semantics.
- Configure `IdleTimeout: 5 * time.Minute` on the underlying transport to recycle idle connections, ensuring rotated OAuth tokens are picked up on subsequent dials.

Non-goals:
- Modifying the envelope schema (`PermissionUpdateEnvelope`) or publish method signatures.
- Changing the fire-and-forget asynchronous batching behavior (`Async=true`, `RequiredAcks=RequireNone`).

## MODIFIED Requirements

### Requirement: Kafka permission publisher infrastructure
The system SHALL provide a Kafka producer in `internal/kafka/` that implements `PermissionPublisherInterface` capable of publishing `PermissionUpdateEnvelope` protobuf messages to the `hook-service.permissions` topic over unauthenticated or authenticated transports.

#### Scenario: Publisher initialization with configured brokers
- **WHEN** hook-service starts with `KAFKA_BROKERS` configured and `KAFKA_AUTH_TYPE=none`
- **THEN** a `PermissionPublisher` instance is initialized with asynchronous writer connections (`Async=true`, `MaxAttempts=10`), tracing, and Prometheus metrics using plain TCP transport
- **THEN** the publisher gracefully flushes pending batches and closes connections on application shutdown (`SIGTERM`/`SIGINT`)

#### Scenario: Publisher initialization without brokers
- **WHEN** hook-service starts without `KAFKA_BROKERS` configured
- **THEN** a nil or noop publisher is provided and service startup proceeds normally without error

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
