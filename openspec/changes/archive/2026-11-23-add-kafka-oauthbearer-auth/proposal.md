## Why

When operating in platform deployment mode, `hook-service` publishes permission synchronization events to Kafka. In production environments secured by the Canonical Identity Platform, Kafka brokers enforce SASL/OAUTHBEARER authentication and TLS encryption; without this capability, `hook-service` cannot connect to secured clusters.

## What Changes

- **Kafka SASL/OAUTHBEARER Support**: Enables `hook-service` to authenticate against Kafka clusters using OAuth2 bearer tokens minted by Ory Hydra via machine-to-machine (client credentials) flow.
- **Automated In-Memory Token Management**: Automatically handles OAuth token acquisition, thread-safe caching, and proactive renewal before token expiration.
- **Kafka TLS Encryption Configuration**: Allows loading trusted CA certificates to establish secure TLS/SSL connections to Kafka brokers.
- **Extended Configuration Options**: Adds environment variables for SASL authentication type, OAuth2 client credentials, token endpoints, audience, and TLS certificates.

## Capabilities

### New Capabilities
- `kafka-client-authentication`: Client-side SASL/OAUTHBEARER authentication and TLS transport encryption for Kafka event publishing.

### Modified Capabilities
- `authorization-service-kafka-permission-pipeline`: Extends the permission publisher initialization to negotiate authenticated and encrypted transports when configured.

## Non-goals

- Replacing the underlying `segmentio/kafka-go` library with an alternative client driver.
- Provisioning or managing OAuth2 client credentials or ACLs within Ory Hydra or Kafka.
- Implementing in-band KIP-368 SASL re-authentication over persistent, uninterrupted TCP sessions.

## Success Metrics

- **Zero-Failure Publication to Secured Clusters**: `hook-service` successfully establishes connections and publishes permission updates to Kafka brokers requiring `SASL_SSL` with `OAUTHBEARER`.
- **Uninterrupted Token Lifecycle**: Long-running publisher instances seamlessly publish messages across token expiration windows without manual restarts or failed deliverability.
- **Backward Compatibility**: Deployments with unauthenticated Kafka (`none`) or standalone mode continue to operate without behavior changes or regressions.

## Impact

- **Affected Packages**:
  - `internal/config`: Configuration schema and validation for Kafka SASL/TLS settings.
  - `internal/kafka`: Implementation of the RFC 7628 SASL mechanism, token source wrapper, and writer transport configuration.
  - `cmd`: Initialization and wiring of authenticated Kafka transport in `cmd/serve.go`.
- **Dependencies**: Uses `golang.org/x/oauth2` for token lifecycle management.
- **Operational Impact**: Operators can deploy `hook-service` against production Charmed Kafka clusters secured by Canonical Identity Platform.
