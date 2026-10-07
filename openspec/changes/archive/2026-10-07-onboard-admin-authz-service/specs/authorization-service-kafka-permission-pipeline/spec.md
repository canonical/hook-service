# Purpose

Provide a Kafka producer infrastructure in `internal/kafka/` capable of publishing protobuf `PermissionUpdateEnvelope` messages for authorization synchronization. Under the coarse-grained authorization model (`role:admin`), group lifecycle operations execute directly against PostgreSQL without publishing runtime permission events to Kafka. The Kafka producer infrastructure is maintained in standby mode, preserving architectural readiness for future event-driven authorization extensions.

Key decisions:
- Kafka producer implementation in `internal/kafka/` using `segmentio/kafka-go` configured for asynchronous, non-blocking publication (`Async: true`).
- Encapsulates `PermissionUpdateEnvelope` protobuf serialization, at-least-once delivery semantics, tracing, Prometheus metrics, and background delivery completion handling.
- Bounded network timeouts and retries: `MaxAttempts` (10) retries transient delivery failures, `ReadTimeout` (1s) bounds initial topic partition discovery, `WriteTimeout` (2s) bounds broker produce calls.
- Standby operation: The publisher is wired into the application lifecycle in `cmd/serve.go`. Under coarse-grained RBAC, group management operations (`pkg/groups/service.go`) operate directly on PostgreSQL.
- Isolation: Kafka availability or publisher errors have zero impact on PostgreSQL database transactions.
- Risk awareness: `segmentio/kafka-go` uses an unbounded slice for its internal batch queue. During an extended Kafka outage under write traffic, pending batches accumulate in memory without backpressure.

Non-goals:
- Publishing runtime permission events for group creation, deletion, or membership modifications under the coarse-grained RBAC model.
- Publishing events for token hook or app-to-group operations.
- Managing Kafka infrastructure deployment or topic lifecycle.

## ADDED Requirements

### Requirement: Kafka permission publisher infrastructure
The system SHALL provide a Kafka producer in `internal/kafka/` that implements `PermissionPublisherInterface` capable of publishing `PermissionUpdateEnvelope` protobuf messages to the `hook-service.permissions` topic.

#### Scenario: Publisher initialization with configured brokers
- **WHEN** hook-service starts with `KAFKA_BROKERS` configured
- **THEN** a `PermissionPublisher` instance is initialized with asynchronous writer connections (`Async=true`, `MaxAttempts=10`), tracing, and Prometheus metrics
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