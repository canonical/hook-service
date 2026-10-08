## Purpose

Enable `hook-service` to connect to Kafka clusters enforcing SASL/OAUTHBEARER authentication and TLS transport encryption. When operating in platform mode within the Canonical Identity Platform, Kafka brokers validate client identity via OAuth2 bearer tokens minted by Ory Hydra using the client credentials grant (`grant_type=client_credentials`), containing audience claim `aud: ["kafka"]`.

Key decisions:
- Custom SASL/OAUTHBEARER mechanism implementing RFC 7628 GS2 framing (`n,,\x01auth=Bearer <token>\x01\x01`) on top of `segmentio/kafka-go`'s `sasl.Mechanism` interface (alternative: migrating to `twmb/franz-go` rejected to avoid breaking existing writer contracts and mock suites).
- Automated in-memory token lifecycle management using Go's `golang.org/x/oauth2/clientcredentials` with thread-safe single-flight caching and proactive token renewal 60 seconds before expiration.
- Periodic connection recycling via `kafkago.Transport.IdleTimeout` to rotate TCP connections and pick up renewed tokens without requiring in-band KIP-368 re-authentication.
- Strict TLS trust isolation: `KAFKA_TLS_CA_FILE` loads into a dedicated `x509.NewCertPool()` containing solely the Kafka broker CA; `KAFKA_OAUTH_CA_FILE` loads into an independent `x509.NewCertPool()` for the Hydra HTTP client. Neither mutates or pollutes the host system certificate pool.
- Scopes default to `""` (empty string) to prevent `invalid_scope` rejections in Hydra when clients are registered without explicit scopes.

Non-goals:
- In-band KIP-368 SASL re-authentication over persistent, uninterrupted TCP sessions.
- Provisioning OAuth2 clients or managing ACLs in Kafka or Hydra.
- Replacing the underlying `segmentio/kafka-go` driver.

Backward compatibility:
- When `KAFKA_AUTH_TYPE` is unset or set to `none`, `hook-service` preserves unauthenticated plain TCP transport with zero breaking changes to existing deployments.

## ADDED Requirements

### Requirement: SASL/OAUTHBEARER mechanism for Kafka transport
The system SHALL provide a SASL/OAUTHBEARER mechanism implementation for `segmentio/kafka-go` compliant with RFC 7628 that negotiates authentication with Kafka brokers using OAuth2 bearer tokens.

#### Scenario: Successful authentication with valid cached token
- **WHEN** the Kafka transport dials a broker connection and the token source holds a valid cached access token with remaining lifetime exceeding 60 seconds
- **THEN** the client transmits an RFC 7628 GS2 initial response containing the bearer token
- **THEN** the SASL handshake completes successfully without invoking Hydra's token endpoint

#### Scenario: Automatic token acquisition via client credentials
- **WHEN** the Kafka transport dials a broker connection and no valid token is cached
- **THEN** the token source executes an HTTP POST to the configured `KAFKA_OAUTH_TOKEN_URL` requesting `grant_type=client_credentials` with `audience=kafka`
- **THEN** the acquired token is cached in memory and formatted into the SASL initial response

#### Scenario: Proactive token renewal before expiration
- **WHEN** the Kafka transport initiates a connection dial and the cached token has 60 seconds or less of remaining validity
- **THEN** the token source automatically fetches a fresh access token from Hydra before completing the SASL handshake
- **THEN** the fresh token replaces the expired token in the cache

#### Scenario: Authentication failure handling
- **WHEN** Hydra rejects the client credentials request or the Kafka broker rejects the bearer token
- **THEN** the handshake fails with an explicit descriptive error and does not leak client secrets or raw tokens in logs

### Requirement: Isolated TLS transport encryption for Kafka brokers
The system SHALL support TLS transport encryption for Kafka broker connections with dedicated CA certificate isolation.

#### Scenario: Dedicated CA certificate pool configuration
- **WHEN** `KAFKA_TLS_CA_FILE` is configured with a valid PEM certificate file
- **THEN** a dedicated certificate pool is created containing only the specified certificate(s)
- **THEN** the Kafka broker transport uses this pool to verify broker certificates without polluting the host system certificate pool

#### Scenario: Missing or unreadable CA certificate file
- **WHEN** `KAFKA_TLS_CA_FILE` specifies a path that does not exist or cannot be read
- **THEN** the service fails startup immediately with an actionable configuration error

#### Scenario: System truststore fallback
- **WHEN** `KAFKA_TLS_ENABLED=true` but `KAFKA_TLS_CA_FILE` is empty
- **THEN** the Kafka broker transport uses the host system truststore for TLS verification

#### Scenario: Insecure skip verify in test mode
- **WHEN** `KAFKA_TLS_INSECURE_SKIP_VERIFY=true`
- **THEN** the Kafka transport skips broker certificate verification and logs a warning

### Requirement: Independent CA certificate support for Hydra OAuth endpoint
The system SHALL support custom CA certificate verification for the OAuth2 token endpoint independently from the Kafka broker CA.

#### Scenario: Dedicated OAuth CA certificate pool configuration
- **WHEN** `KAFKA_OAUTH_CA_FILE` is configured with a valid PEM certificate file
- **THEN** an isolated certificate pool is created containing only the specified OAuth CA certificate(s)
- **THEN** the HTTP client communicating with Hydra uses this pool to verify Hydra's TLS certificate

#### Scenario: Standard system truststore fallback for OAuth endpoint
- **WHEN** `KAFKA_OAUTH_CA_FILE` is empty
- **THEN** the OAuth HTTP client verifies Hydra's TLS certificate using the host system truststore

### Requirement: Kafka client configuration schema and validation
The system SHALL validate Kafka authentication and encryption environment variables at startup.

#### Scenario: Valid OAuthBearer configuration
- **WHEN** `KAFKA_AUTH_TYPE=oauthbearer` and valid `KAFKA_OAUTH_TOKEN_URL`, `KAFKA_OAUTH_CLIENT_ID`, and `KAFKA_OAUTH_CLIENT_SECRET` are provided
- **THEN** startup validation succeeds and the authenticated transport is initialized

#### Scenario: Missing required OAuthBearer credentials
- **WHEN** `KAFKA_AUTH_TYPE=oauthbearer` but any of `KAFKA_OAUTH_TOKEN_URL`, `KAFKA_OAUTH_CLIENT_ID`, or `KAFKA_OAUTH_CLIENT_SECRET` is missing or empty
- **THEN** startup validation fails immediately with an error detailing the missing configuration parameters

#### Scenario: Unauthenticated default mode
- **WHEN** `KAFKA_AUTH_TYPE` is unset or set to `none`
- **THEN** startup validation succeeds and the publisher operates with unauthenticated transport

#### Scenario: Standalone mode without brokers configured
- **WHEN** hook-service starts without `KAFKA_BROKERS` configured
- **THEN** Kafka security validation is bypassed and the service initializes in standalone mode with a noop publisher
- **THEN** an informational warning is logged if Kafka authentication or TLS variables were configured

