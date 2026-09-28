# 1. Configuration (`internal/config`)

- [x] 1.1 Add `FederatedServiceName` to `EnvSpec` in `internal/config/specs.go` with `envconfig:"federated_service_name"` and `default:"hook-service"`
- [x] 1.2 Add a destination derivation helper in `internal/config/specs.go` returning `<name>.permissions`. Do not add a group object type derivation; with nothing published it would have no reader (`design.md` §3)
- [x] 1.3 Add validation rejecting an empty or whitespace-only federated service name, following the existing `ValidateKafkaBrokers` pattern in `internal/config/specs.go`
- [x] 1.4 Unit tests in `internal/config/specs_test.go`: table-driven coverage of the default value, the derivation for `hook-service` and `portal`, and rejection of empty and whitespace-only names. Standard library assertions only

## 2. Publisher identity (`internal/kafka`)

- [x] 2.1 Replace the fixed declared identity with instance state: add a service name field to `PermissionPublisher` in `internal/kafka/producer.go` and a corresponding parameter to `NewPermissionPublisher`, placed before `tracer`, `monitor`, `logger` per the project constructor convention
- [x] 2.2 Use the instance service name when populating the envelope identity in `publish` (`internal/kafka/producer.go`), replacing the `ServiceName` constant
- [x] 2.3 Remove `internal/kafka`'s deployment-specific default identity and destination constants and use the supplied `serviceName` and `topic` verbatim (`internal/kafka/producer.go`). Do not change writer delivery settings; `RequiredAcks: RequireNone` and `Async: true` are retained deliberately (`design.md` §8)
- [x] 2.4 Leave `NoopPublisher` and `NewNoopPublisher` unchanged (`internal/kafka/noop.go`)
- [x] 2.5 Regenerate mocks: `go generate ./internal/kafka/...`
- [x] 2.6 Update unit tests in `internal/kafka/producer_test.go` to construct the publisher with an explicit service name and to assert the envelope identity reflects it, including a non-default value. Standard library assertions only
- [x] 2.7 Carry the destination as publisher state: add a topic field to `PermissionPublisher` and a corresponding parameter to `NewPermissionPublisher` in `internal/kafka/producer.go`, falling back to `DefaultPermissionsTopic` when empty, and set the `messaging.destination` span attribute in `PublishOperations` from it instead of the constant (`design.md` §3)
- [x] 2.8 Unit test in `internal/kafka/producer_test.go` asserting the `messaging.destination` span attribute equals the configured destination, including a non-default value. Record the span with the vendored OpenTelemetry span recorder rather than a noop tracer, since a noop span discards attributes. Standard library assertions only

## 3. Mode exposure (`pkg/status`)

- [x] 3.1 Add an active mode field to the status response in `pkg/status`, reporting `platform` or `standalone`, sourced from injected state rather than read from the environment so the reported mode cannot drift from what was wired at startup
- [x] 3.2 Unit tests in the corresponding `pkg/status` test file covering both `platform` and `standalone` values. Standard library assertions only

## 4. Wiring (`cmd/serve.go`, `pkg/web`)

- [x] 4.1 Derive the destination from `FederatedServiceName` in `cmd/serve.go` and pass it to both `NewKafkaWriter` and `NewPermissionPublisher`, in place of the current empty string argument
- [x] 4.2 Pass the federated service name to `NewPermissionPublisher` in `cmd/serve.go`
- [x] 4.3 Replace the broker-presence log messages in `cmd/serve.go` with explicit mode reporting: platform mode logs the federated service name, the derived destination, and the brokers; standalone mode logs that permission event publishing is disabled
- [x] 4.4 Add a startup warning in `cmd/serve.go` when the federated service name is non-default and no brokers are configured, without failing startup (`design.md` §4)
- [x] 4.5 Add the symmetric startup warning in `cmd/serve.go` when brokers are configured under the default federated service name, without failing startup. Mode is derived from broker presence while the name is configured separately, so both disagreements must be observable (`design.md` §4)
- [x] 4.6 Pass the active mode to the status API construction in `pkg/web/router.go`
- [x] 4.7 Verify graceful shutdown is unaffected: the publisher close deferral in `cmd/serve.go` must still run after the error group returns
- [x] 4.8 Confirm `groups.NewService` call sites in `cmd/serve.go` and `pkg/web/router.go` are unchanged. This change threads nothing into `pkg/groups`
- [x] 4.9 Unit tests: extend existing `pkg/web/router` tests to cover construction with both a default and a non-default federated service name. Standard library assertions only

## 5. Test harness (`scripts/`)

- [x] 5.1 Remove `KAFKA_BROKERS="localhost:9092"` from the hook-service invocation in `scripts/setup-centralized-authz-e2e.sh`. It currently causes the standalone harness to report platform mode while asserting standalone role-membership behavior (`design.md` §12)
- [x] 5.2 Confirm `scripts/test-centralized-authz-e2e.sh` passes unchanged. It asserts role-membership behavior only and is unaffected

## 6. Documentation and rollout

- [x] 6.1 Retire the rejected build-tag architecture design so that it is no longer the recorded plan, either by removing it or by redirecting it to this change. Leaving it in place risks it being implemented later by someone encountering it in isolation (`design.md` §5)
- [x] 6.2 Record `FEDERATED_SERVICE_NAME` in the project's environment variable reference, including its default and both derived values
- [x] 6.3 Record `KAFKA_BROKERS` in the project's environment variable reference, and correct the stale entry for a Salesforce enablement toggle that has no corresponding field in `internal/config/specs.go`
- [x] 6.4 Record the two mode names and how each is selected in the project's operator-facing documentation
- [x] 6.5 Correct any existing statement in project documentation claiming that permission events are published for group creation, deletion, or membership changes. No events are published
- [x] 6.6 Rollout: standalone deployments require no configuration change. The default federated service name reproduces the existing destination and declared identity, and the absence of broker configuration preserves current behavior. No migration and no schema change

## 7. Verification suite

- [x] 7.1 `go build ./...` completes with no errors and without build tags
- [x] 7.2 `go vet ./...` completes with no diagnostics
- [x] 7.3 `golangci-lint run` introduces no new findings. The repository carries 61 pre-existing findings at the base revision; verification is `golangci-lint run --new-from-rev=HEAD` reporting zero, and the total remaining at 61
- [x] 7.4 `go test -race ./...` reports zero failures
- [x] 7.5 `go test ./... -cover -coverprofile=coverage.out` shows no package regressing more than five percent against the pre-change baseline
- [x] 7.6 `go test ./... -short` passes, confirming no new container dependency was introduced
- [x] 7.7 Confirm by inspection that no non-test file under `pkg/groups/` changes, that `groups.Service` gains no field or parameter, and that `groups.NewService` call sites are unchanged. Test-only call sites of `web.NewRouter` under `pkg/groups/` may gain the deployment mode argument, since that is a compile consequence of a signature change in another package
- [x] 7.8 Confirm by inspection that no call site of `PublishWrite`, `PublishDelete`, or `PublishOperations` exists outside `internal/kafka` and its tests

## 9. Review follow-up

Resolutions of the automated review on the pull request. Findings assessed as invalid are recorded in Implementation Notes with reasons rather than actioned.

- [x] 9.1 Remove the duplicated default identity and destination constants from `internal/kafka` rather than adding a test to keep them in step with `internal/config`. This resolves the duplicated-constant finding and the dead-fallback finding together, since both were symptoms of `internal/kafka` holding one deployment's name. `serviceName` and `topic` become required and are used verbatim; `internal/kafka/integration_test.go` derives them from `internal/config` (`design.md` §3)
- [x] 9.2 Bound the derived destination length in `ValidateFederatedServiceName` (`internal/config/specs.go`) so an over-long federated service name fails at startup rather than asynchronously at the broker. Unit tests in `internal/config/specs_test.go` cover the longest legal name and the first illegal one
- [x] 9.3 Introduce `config.DeploymentMode` as a named type for the mode (`internal/config/specs.go`) and thread it through `pkg/status`, `pkg/web/router.go`, and `cmd/serve.go` so an arbitrary string cannot reach the status contract. Unit tests updated in `pkg/status/handlers_test.go` and `pkg/web/router_test.go`
- [x] 9.4 Extract publisher selection and mode derivation into `newPublisherSetup` (`cmd/serve.go`) returning both together, and cover the specification scenario requiring the reported mode to match the initialized publisher with unit tests in `cmd/serve_test.go`, including the two disagreement warnings and the configuration rejection paths
- [x] 9.5 Consume the envelope schema from its owner instead of redefining it: import `github.com/canonical/authorization-service/api/v1` in `internal/kafka` and delete the local copies `proto/authorization/service/api/v1/messages.proto` and `gen/authorization/service/api/v1/messages.pb.go`. Authorization Service produces the schema and consumes the events, so a second definition in this repository can diverge from the contract it is meant to satisfy with nothing detecting it

## 10. Implementation Notes

Deviations from project conventions, agreed before implementation:

- **No integration or end-to-end coverage.** Project convention requires integration tests using testcontainers for external dependencies. This change adds no behavior reachable through an external dependency, because nothing is published. Coverage is at the construction boundary via unit tests. A platform-mode end-to-end harness is additionally blocked on Portal's model existing in the Authorization Service repository.
- **Documentation tasks name no files.** Project convention requires each task to reference the files it touches. The tasks in §6 describe documentation targets by purpose rather than by path, so that these artifacts do not depend on documentation that may be moved, renamed, or removed.
- **A pre-existing convention violation in `internal/kafka/integration_test.go` is left in place.** That test skips rather than fails when its container cannot start, and it lacks a short-mode guard. Both are contrary to project test conventions. It is out of scope here and should not be used as a pattern by task 2.6.
- **This change is preparatory and publishes nothing.** Its rationale is recorded in `design.md` §5. The concerns that apply once publishing is introduced are recorded in `design.md` §8 so that the later change inherits them.

Record any further deviations discovered during implementation below.

- **Generating mocks into `cmd` lowers that package's reported coverage.** `cmd/mock_logger.go` contributes 130 coverage blocks, more than `cmd/serve.go` itself, because `LoggerInterface` together with `SecurityLoggerInterface` is 34 methods. `cmd` therefore reports 18.3 percent against a 20.6 percent baseline despite `newPublisherSetup` being covered at 100 percent. The mocks are placed as `mock_*.go` in package `cmd`, matching every other package in the repository; naming them `mock_*_test.go` would exclude them from the denominator but would diverge from that convention. The drop is an artifact of the denominator, not a loss of coverage, and is within the five percent guard.
- **Depending on Authorization Service for the envelope schema carries module-graph consequences, accepted as the cost of a single definition.** `github.com/canonical/authorization-service@v1.0.0` declares `go 1.26.1`, so this module's `go` directive rises from `1.26.0` to `1.26.1`; CI already runs Go 1.27. Minimal version selection against that module's requirements also upgrades shared indirect dependencies: `openfga/go-sdk` 0.7.1 to 0.7.5, `segmentio/kafka-go` 0.4.47 to 0.4.51, and `spf13/viper` 1.18.2 to 1.21.0 with its transitive set. The imported package `api/v1` is one Go package containing the service's gateway and gRPC stubs alongside the messages, so `grpc-gateway/v2` and `grpc` are linked through it; both were already direct dependencies. No new code is compiled beyond the generated types. `go build`, `go vet`, `go test -short ./...`, and `go test -race ./internal/kafka/...` pass, and the fully linked binary starts, confirming no duplicate protobuf registration remains.

### Review findings assessed as not requiring action

- **Unconditional name validation is not a regression and not a specification conflict.** The review reported it as blocking on two grounds, both incorrect. First, `FEDERATED_SERVICE_NAME` does not exist at the base revision, so no existing deployment can set it and none can regress; unset resolves to `hook-service`, which is valid. Second, the non-goal cited as "Failing startup" is in full "Failing startup when a platform deployment lacks broker configuration", which concerns absent brokers, not name validation; the specification separately and unconditionally requires an invalid name to fail startup. The proposed remedy would also defer the failure to the moment brokers are first configured, which is worse. Rationale recorded in `design.md` §3.
- **Two sources for the destination is the recorded decision, not an oversight.** The review's two suggested alternatives, reading the destination back from the writer and re-deriving it from the declared identity, are precisely the alternatives evaluated and rejected in `design.md` §3. The accepted form passes one derived value to both the writer and the publisher from the single place that derives it.
- **The dead-fallback finding was initially rejected in error and has since been actioned.** It was dismissed on the grounds that the fallback was exercised by tests and mandated by task 2.3. Both grounds were weak: the only tests exercising the fallback existed to exercise it, and task 2.3 was written as part of this change rather than being an independent constraint. The finding and the duplicated-constant finding shared one root cause, `internal/kafka` holding a deployment-specific default. Removing the constants resolves both, and made the guarding test added for task 9.1 unnecessary; it was deleted.
- **The status handler writing its header before encoding is pre-existing and out of scope.** The review reached the same conclusion.

- **Task 4.9 created a test file rather than extending one.** `pkg/web` had no test files, so `pkg/web/router_test.go` is new. It asserts the mode through the router's status endpoint rather than only at construction, which also covers task 4.6's wiring and the specification scenario requiring the reported mode to match the initialized publisher.
- **Task 7.3 was reworded during implementation.** As originally written it required `golangci-lint run` to report no findings. The base revision already carries 61 findings across packages this change does not touch, so the criterion was unsatisfiable without unrelated remediation. It now requires no *new* findings. Measured against a clean worktree of the base revision with mocks generated: 61 findings before and 61 after, with `--new-from-rev=HEAD` reporting zero. Note that the total is not a stable metric while editing: `golangci-lint` defaults cap repeated identical messages at three, so unrelated counts shift as line numbers move. The authoritative checks are the zero new-issue count and direct confirmation that the flagged lines fall outside this change's diff.
- **Task 7.7 was reworded during implementation.** As originally written it asserted that no file under `pkg/groups/` changes. Adding the deployment mode parameter to `web.NewRouter` breaks compilation of three integration tests that construct the router, one of which is `pkg/groups/groups_integration_test.go`. The fix is one added argument per call site with no behavior change. The task now asserts the intended property: no non-test file under `pkg/groups/` changes, `groups.Service` gains no field or parameter, and `groups.NewService` call sites are unchanged.
- **`internal/config/specs.go` and `pkg/status/handlers_test.go` carried pre-existing formatting defects.** Both were not `gofmt`-clean before this change. Editing them surfaced the misalignment, so the diff includes incidental realignment of unrelated struct fields in `internal/config/specs.go`. Reverting it would leave the file unformatted.
- **`ValidateFederatedServiceName` also rejects characters invalid in a topic name.** The specification requires rejecting empty and whitespace-only names. Validation additionally rejects any name containing characters outside letters, digits, dots, underscores, and hyphens, because the name is concatenated into a topic and an otherwise-accepted name such as `portal ` would produce a topic that fails asynchronously at the broker with automatic topic creation disabled.
- **The `messaging.destination` attribute key is unchanged.** The OpenTelemetry messaging conventions superseded the bare `messaging.destination` with `messaging.destination.name`. Because nothing is published, no consumer can depend on either spelling and renaming would be free, but it is a separate observability decision and was not taken. Recorded in `design.md` §3.
