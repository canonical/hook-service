# 1. Authorization Service rules.yaml (Authorization Service repository)

- [x] 1.1 Create `authz/model/services/hook-service/rules.yaml` mapping all admin API routes to `role:admin#assignee` per `authorization-service-model-onboarding/spec.md`
- [x] 1.2 Validate rules with `go run main.go seed validate` and `go test ./...`
- [x] 1.3 Open PR to Authorization Service repository and get review/merge

## 2. Protobuf schema and generation

- [x] 2.1 Add `PermissionUpdateEnvelope` proto definition (`proto/authorization/service/api/v1/messages.proto`) to `proto/` directory
- [x] 2.2 Generate Go protobuf code: `buf generate`, verify output in `gen/`
- [x] 2.3 Add `segmentio/kafka-go` dependency to `go.mod`

## 3. Kafka permission publisher infrastructure (`internal/kafka/`)

- [x] 3.1 Create `internal/kafka/producer.go` — `PermissionPublisher` struct with `NewPermissionPublisher`, `PublishWrite`, `PublishDelete` (`Async=true`, `requiredAcks=RequireNone`, `maxAttempts=10`, `ReadTimeout=1s`, `WriteTimeout=2s`, error-logging `Completion` callback, idempotency keys)
- [x] 3.2 Create `internal/kafka/interfaces.go` — `PermissionPublisherInterface`
- [x] 3.3 Add tracing spans and metrics (publish count, duration) per project conventions
- [x] 3.4 Add `//go:generate mockgen` directive and generate mocks
- [x] 3.5 Write unit tests for `internal/kafka/producer.go`

## 4. Kafka configuration

- [x] 4.1 Add `KAFKA_BROKERS` env var to `internal/config/specs.go`
- [x] 4.2 Add Kafka broker validation in `internal/config/` configuration loading

## 5. Groups service integration

- [x] 5.1 Maintain `pkg/groups/service.go` group lifecycle operations (`CreateGroup`, `UpdateGroup`, `DeleteGroup`, `AddUsersToGroup`, `RemoveUsersFromGroup`) managing application state directly in PostgreSQL
- [x] 5.2 Wire optional `PermissionPublisherInterface` into `pkg/groups/service.go` constructor to maintain standby producer capability
- [x] 5.3 Write/update unit tests in `pkg/groups/service_test.go` covering group and membership management operations

## 6. Wire dependencies in `cmd/serve.go`

- [x] 6.1 Initialize standby `PermissionPublisher` in `cmd/serve.go` when `KAFKA_BROKERS` is configured
- [x] 6.2 Pass `PermissionPublisher` into groups service constructor
- [x] 6.3 Ensure graceful shutdown: close Kafka writer on `SIGTERM`/`SIGINT`
- [x] 6.4 Hook-service continues using its own OpenFGA instance for token hook (unchanged from current behavior)

## 7. Disable JWT authentication on admin routes

- [x] 7.1 Set `AUTHENTICATION_ENABLED=false` to disable JWT middleware on admin routes
- [x] 7.2 Ensure `Authorization` header forwarded by Istio is trusted by hook-service

## 8. Verification

- [x] 8.1 Run `go vet ./...`
- [x] 8.2 Run `go test -race ./...`
- [x] 8.3 Run `go build ./...`