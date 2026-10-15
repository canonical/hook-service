## Why

The current Kafka client relies on `segmentio/kafka-go`, which is no longer actively maintained and requires custom RFC 7628 SASL state machines and leaky artificial interfaces. Migrating to `twmb/franz-go` provides native SASL OAUTHBEARER authentication with KIP-368 connection re-authentication, modern connection management, and adherence to Go idioms and clean architecture principles.

## What Changes

- Replace `segmentio/kafka-go` with `twmb/franz-go` as the underlying Kafka client in `internal/kafka`.
- Eliminate the artificial `KafkaWriterInterface` and its leaky `kafkago.Message` dependency.
- Refactor `PermissionPublisher` to directly encapsulate `*kgo.Client` while preserving asynchronous produce delivery (`NoAck`) and existing domain contracts (`PermissionPublisherInterface`).
- Replace custom SASL RFC 7628 framing (`internal/kafka/sasl/oauthbearer.go`) with `franz-go`'s native `pkg/sasl/oauth`.
- Connect `TokenSource` directly to `oauth.Oauth` with automatic cached token invalidation on broker SASL authentication failure via `HookBrokerConnect`.
- Remove connection recycling workarounds (`DefaultTransportIdleTimeout = 5 * time.Minute`) as `franz-go` natively manages connection lifecycles and re-authentication.

## Non-goals

- Upgrading producer durability from `NoAck` to `LeaderAck` or `AllISRAcks` in this change.
- Switching from asynchronous produce to synchronous socket-blocking produce.
- Adopting `twmb/franz-go/plugin/kotel` OpenTelemetry plugin at this time.
- Modifying the consumer client in `authorization-service`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `authorization-service-kafka-permission-pipeline`: Update the publisher infrastructure to use `twmb/franz-go` with native SASL OAUTHBEARER and asynchronous produce dispatch instead of `segmentio/kafka-go`.

## Success Metrics

- All Kafka producer unit, integration, and E2E tests pass using `twmb/franz-go`.
- `internal/kafka/sasl/oauthbearer.go` and `internal/kafka/sasl/oauthbearer_test.go` deleted.
- Zero leaky vendor message types in package interfaces (`interfaces.go`).
- Token refreshes and broker re-authentications execute without connection leaks or artificial idle timeout recycling.

## Impact

- Affected packages: `internal/kafka`, `internal/kafka/sasl`, `go.mod`.
- API impacts: Zero breaking changes to `PermissionPublisherInterface` or `pkg/groups`.
- Dependency changes: Add `github.com/twmb/franz-go` and remove `github.com/segmentio/kafka-go`.
