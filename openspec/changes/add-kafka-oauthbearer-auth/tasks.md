## 1. Configuration & Validation

- [ ] 1.1 Extend `EnvSpec` in `internal/config/specs.go` with Kafka SASL and TLS variables: `KafkaAuthType`, `KafkaOAuthTokenURL`, `KafkaOAuthClientID`, `KafkaOAuthClientSecret`, `KafkaOAuthScopes`, `KafkaOAuthAudience`, `KafkaOAuthCAFile`, `KafkaTLSEnabled`, `KafkaTLSCAFile`, and `KafkaTLSInsecureSkipVerify`. Use `cmp.Or` for fallback defaults where applicable.
- [ ] 1.2 Implement `ValidateKafkaSecurity(specs *EnvSpec) error` in `internal/config/specs.go` accumulating all configuration errors via `errors.Join`. Add unit tests in `internal/config/specs_test.go` using Go 1.24+ `t.Context()` and `errors.Is`/`errors.AsType` covering missing OAuth credentials, unsupported auth types, and unauthenticated defaults.

## 2. SASL OAUTHBEARER Mechanism & Token Management

- [ ] 2.1 Implement the RFC 7628 SASL mechanism and state machine (`Mechanism` and `StateMachine`) in `internal/kafka/sasl/oauthbearer.go` using `fmt.Appendf` for direct byte-slice GS2 initial response formatting (`n,,\x01auth=Bearer <token>\x01\x01`) and `bytes.Cut`/`strings.CutPrefix` for broker challenge and error parsing. Add unit tests in `internal/kafka/sasl/oauthbearer_test.go` using `t.Context()` to verify RFC 7628 framing and error challenge handling.
- [ ] 2.2 Implement in-memory token source and proactive renewal wrapper (`TokenSource`) in `internal/kafka/sasl/tokensource.go` with a 60-second early renewal margin calculated using `time.Until(token.Expiry) <= refreshMargin` and bounded outbound fetch contexts using `context.WithTimeoutCause`. Add unit tests in `internal/kafka/sasl/tokensource_test.go` using `t.Context()` verifying mutex synchronization, single-flight fetching, and refresh timing.
- [ ] 2.3 Implement dedicated OAuth CA certificate pool loader in `internal/kafka/sasl/http.go` constructing an isolated `x509.NewCertPool()` for Hydra HTTP client requests, utilizing `errors.AsType[*os.PathError]` for file diagnostics. Add unit tests in `internal/kafka/sasl/http_test.go` using `t.Context()` verifying certificate pool isolation and error handling on invalid files.

## 3. Kafka TLS Configuration & Transport Builder

- [ ] 3.1 Implement dedicated broker TLS configuration builder `BuildBrokerTLSConfig(caFile string, insecureSkipVerify bool) (*tls.Config, error)` in `internal/kafka/tls.go` using `x509.NewCertPool()` for broker trust isolation and `errors.AsType[*os.PathError]` for missing file detection. Add unit tests in `internal/kafka/tls_test.go` using `t.Context()` validating truststore isolation, missing file errors, and system truststore fallback when `caFile` is empty.
- [ ] 3.2 Update `NewKafkaWriter` in `internal/kafka/producer.go` to accept an optional `*kafkago.Transport` configured with `IdleTimeout: 5 * time.Minute`, preserving existing batching defaults and completion handler. Update unit tests in `internal/kafka/producer_test.go` using `t.Context()` for test contexts and verifying transport configuration.

## 4. Application Lifecycle & Startup Wiring

- [ ] 4.1 Update `newPublisherSetup` in `cmd/serve.go` to bypass Kafka security checks and log an informational warning when `specs.KafkaBrokers` is empty, execute `ValidateKafkaSecurity` when brokers are configured, resolve defaults with `cmp.Or`, construct authenticated transport when `specs.KafkaAuthType == "oauthbearer"`, and pass the configured writer to `NewPermissionPublisher`.
- [ ] 4.2 Add unit tests for `newPublisherSetup` in `cmd/serve_test.go` using `t.Context()` validating standalone mode warnings/bypasses and publisher creation across `none` and `oauthbearer` modes.

## 5. Integration Verification

- [ ] 5.1 Implement integration tests using `testcontainers-go` in `internal/kafka/producer_integration_test.go` passing `t.Context()` to `testhelpers.SetupHydra` to issue OAuth tokens and verifying SASL_SSL message publication against an authenticated Kafka broker container. Guard with `if testing.Short() { t.Skip(...) }`.

## 6. Verification Suite

- [ ] 6.1 Execute test matrix: `go test -v -race ./...` must complete with zero diagnostic failures.
- [ ] 6.2 Execute linter checks: `golangci-lint run` must report zero errors (including Go modernization linters).

## 7. Documentation & Rollout

- [ ] 7.1 Update `AGENTS.md` and documentation with the new `KAFKA_*` environment variables, default values, and deployment instructions for Charmed Kafka with Ory Hydra.

## 8. Implementation Notes

- [ ] 8.1 Record any runtime deviations, implementation discoveries, or operational notes encountered during task execution.
