## 1. Dependencies and SASL Cleanup

- [x] 1.1 Add `github.com/twmb/franz-go` to `go.mod` and run `go mod tidy` and `go mod vendor`. Verify dependency resolution via `go build ./...` (`go.mod`, `go.sum`, `vendor/`).
- [x] 1.2 Remove custom RFC 7628 SASL state machine and tests (`internal/kafka/sasl/oauthbearer.go`, `internal/kafka/sasl/oauthbearer_test.go`). Verify package builds with `go test ./internal/kafka/sasl/...`.
- [x] 1.3 Clean up SASL interfaces by removing `kafkago_sasl` imports while retaining `TokenProvider` and `TokenMetricsInterface` (`internal/kafka/sasl/interfaces.go`). Verify mock generation and unit tests pass with `go test -v ./internal/kafka/sasl/...`.

## 2. Kafka Publisher Redesign

- [x] 2.1 Remove `KafkaWriterInterface` from `internal/kafka/interfaces.go` and delete generated `mock_kafka.go` (`internal/kafka/interfaces.go`, `internal/kafka/mock_kafka.go`).
- [x] 2.2 Refactor `internal/kafka/producer.go`: encapsulate `*kgo.Client` directly in `PermissionPublisher`, extract pure `buildEnvelope` helper function, and implement asynchronous `kgo.TryProduce` with delivery promise callback (`internal/kafka/producer.go`). Verify pure envelope serialization and buffer overflow fail-fast with unit tests in `internal/kafka/producer_test.go`.
- [x] 2.3 Refactor `internal/kafka/config.go`: initialize `*kgo.Client` with `kgo.SeedBrokers`, `kgo.RequiredAcks(kgo.NoAck())`, `kgo.RecordRetries(maxRecordRetries)`, `kgo.DialTLSConfig`, `oauth.Oauth(...)`, and register `HookBrokerConnect` for token invalidation on failure (`internal/kafka/config.go`). Verify configuration parsing with unit tests in `internal/kafka/config_test.go`.

## 3. Unit and Integration Testing

- [x] 3.1 Refactor unit tests in `internal/kafka/producer_test.go` to test `buildEnvelope` deterministically and verify publisher lifecycle and operations with standard library assertions (`internal/kafka/producer_test.go`). Run unit tests via `go test -race -short ./internal/kafka/...`.
- [x] 3.2 Update Kafka integration tests: provide shared container fixture `testhelpers.SetupKafka` in `internal/testhelpers/containers.go` and consolidate publishing integration tests in `internal/kafka/integration_test.go` into a single table-driven test (`TestKafkaIntegration_PublishAndConsume`). Run integration suite via `go test -race -v -run TestKafkaIntegration ./internal/kafka/...`.
- [x] 3.3 Verify full SASL OAUTHBEARER integration with Ory Hydra and Kafka container in `tests/e2e/kafka_auth_test.go` (`tests/e2e/kafka_auth_test.go`). Run E2E authentication suite via `go test -race -v ./tests/e2e -run TestKafkaAuth`.

## 4. Verification Suite

- [x] 4.1 Run unit tests with race detection: `go test -race -short ./internal/kafka/...` with zero diagnostic failures.
- [x] 4.2 Run container-backed integration tests: `go test -race -v -run TestKafkaIntegration ./internal/kafka/...`.
- [x] 4.3 Run package linting and full unit test suite: `golangci-lint run ./internal/kafka/...` and `go test -short ./...`.

## 5. Documentation & Rollout

- [x] 5.1 Update Kafka architecture notes in `AGENTS.md` to reference `franz-go` instead of `kafka-go` (`AGENTS.md`).

## Implementation Notes

- Preserved fire-and-forget asynchronous dispatch using `kgo.TryProduce(context.WithoutCancel(ctx), record, promise)` with `kgo.RequiredAcks(kgo.NoAck())`, `kgo.DisableIdempotentWrite()`, bounded buffer capacity (`kgo.MaxBufferedRecords(10000)`), and delivery timeout (`kgo.RecordDeliveryTimeout(30s)`).
- In the delivery promise callback, delivery failures are logged via `p.logger.Errorf` without mutating dependency availability metrics, as requested during review.
- Unit tests use standard library assertions only and use an in-memory Kafka cluster fixture (`kfake`) without external network calls or container dependencies, running deterministically in `-short` mode.
- Container-backed integration tests use shared `testhelpers.SetupKafka` with `confluentinc/confluent-local:7.5.0` pinned in `internal/testhelpers/containers.go` and manage topics via `twmb/franz-go/pkg/kadm`.
