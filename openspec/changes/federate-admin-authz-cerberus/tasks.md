## 1. Cerberus hook-service module and rules.yaml (Cerberus repository)

- [ ] 1.1 Create `authz/model/modules/hook-service.fga` with the `group-in-claim` type defining `admin`, `owner`, `member` relations using `user with tenant_match`
- [ ] 1.2 Create `authz/model/services/hook-service/rules.yaml` with route-to-permission mappings per `cerberus-model-federation/spec.md`
- [ ] 1.3 Seed initial admin tuple: `user:<admin_id> → admin → group-in-claim:__domain__` in the Cerberus shared OpenFGA store
- [ ] 1.4 Open PR to Cerberus repository and get review/merge

## 2. Protobuf schema and generation

- [ ] 2.1 Add `PermissionUpdateEnvelope` proto definition from Cerberus (`authorization-service/api/proto/v1/messages.proto`) to `proto/` directory
- [ ] 2.2 Generate Go protobuf code: `buf generate` or equivalent, verify output in `gen/`
- [ ] 2.3 Add `segmentio/kafka-go` dependency to `go.mod`

## 3. Kafka permission publisher (`internal/kafka/`)

- [ ] 3.1 Create `internal/kafka/producer.go` — `PermissionPublisher` struct with `NewPermissionPublisher`, `PublishWrite`, `PublishDelete` (at-least-once delivery with `requiredAcks=RequireAll`, `maxAttempts=3`, idempotency keys; DB transaction is never rolled back on Kafka failure)
- [ ] 3.2 Create `internal/kafka/interfaces.go` — `PermissionPublisherInterface`
- [ ] 3.3 Add tracing spans and metrics (publish count, duration) per project conventions
- [ ] 3.4 Add `//go:generate mockgen` directive and generate mocks
- [ ] 3.5 Write unit tests for `internal/kafka/producer.go`

## 4. Kafka configuration

- [ ] 4.1 Add `KAFKA_BROKERS` env var to `internal/config/specs.go`
- [ ] 4.2 Add Kafka broker validation in `internal/config/` configuration loading

## 5. Integrate Kafka publisher in groups service

- [ ] 5.1 Update `pkg/groups/service.go` `CreateGroup` — record creator in PostgreSQL with owner role and publish `owner` tuple for creator on `group-in-claim:<group_id>` after successful DB insert (at-least-once delivery; DB transaction is not rolled back on Kafka failure)
- [ ] 5.2 Update `pkg/groups/service.go` `AddUsersToGroup` — publish `member` tuple for each added user on `group-in-claim:<group_id>` (at-least-once; DB not affected)
- [ ] 5.3 Update `pkg/groups/service.go` `RemoveUsersFromGroup` — publish delete `member` tuple for each removed user on `group-in-claim:<group_id>` (at-least-once; DB not affected)
- [ ] 5.4 Update `pkg/groups/service.go` `DeleteGroup` — retrieve owner and member IDs from PostgreSQL, execute DB deletion, and publish `PERMISSION_OP_DELETE` tuples for owner and all members on `group-in-claim:<group_id>`
- [ ] 5.5 Update `pkg/groups/interfaces.go` — add `PermissionPublisherInterface` dependency
- [ ] 5.6 Wire `PermissionPublisher` into groups `Service` constructor
- [ ] 5.7 Write/update unit tests for `pkg/groups/service_test.go`

## 6. Integration tests for Kafka pipeline

- [ ] 6.1 Write integration test: create group → verify Kafka message published with correct `owner` tuple on `group-in-claim:<group_id>`
- [ ] 6.2 Write integration test: add user to group → verify Kafka message published with correct `member` tuple on `group-in-claim:<group_id>`
- [ ] 6.3 Write integration test: remove user from group → verify Kafka message published with correct delete `member` tuple
- [ ] 6.4 Write integration test: delete group → verify Kafka delete messages published for owner and all members
- [ ] 6.5 Write integration test: Kafka publish failure → verify DB write/delete succeeds and no error returned

## 7. Wire dependencies in `cmd/serve.go`

- [ ] 7.1 Create `PermissionPublisher` instance in `cmd/serve.go` using `KAFKA_BROKERS` config
- [ ] 7.2 Pass `PermissionPublisher` into groups service constructor
- [ ] 7.3 Ensure graceful shutdown: close Kafka writer on `SIGTERM`/`SIGINT`
- [ ] 7.4 Hook-service continues using its own OpenFGA instance (unchanged from current behavior)

## 8. Disable JWT authentication on admin routes

- [ ] 8.1 Set `AUTHENTICATION_ENABLED=false` to disable JWT middleware on admin routes
- [ ] 8.2 Ensure `Authorization` header forwarded by Istio is trusted by hook-service

## 9. Infrastructure configuration

- [ ] 9.1 Deploy Cerberus with `KAFKA_FEDERATED_SERVICES=hook-service` and `hook-service.permissions` topic
- [ ] 9.2 (Out of scope — authorization service responsibility) Istio `AuthorizationPolicy` and `EnvoyFilter` for Cerberus extAuthz
- [ ] 9.3 (Out of scope — handled in federation PR) Run `ensure-topics --services hook-service` in Cerberus to create Kafka topic

## 10. Verification

- [ ] 10.1 Run `go vet ./...` — must pass
- [ ] 10.2 Run `go test -race ./...` — must pass with zero race diagnostics
- [ ] 10.3 Run `go test ./... -cover -coverprofile=coverage.out` — ensure no >5% coverage drop in any package
- [ ] 10.4 Run `golangci-lint run` — must pass
- [ ] 10.5 Run `go build ./...` — must compile cleanly
- [ ] 10.6 Test end-to-end: create group → verify Kafka message → verify Cerberus writes tuple → admin API request with extAuthz