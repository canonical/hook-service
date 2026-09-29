# Purpose

Allow one hook-service build to serve two deployments. The Canonical Identity Platform standalone deployment publishes no permission events. The Canonical Portal deployment will publish permission events to its own destination under its own identity, once its authorization model is defined.

This capability settles two questions that do not depend on that model: how a deployment is told which identity and destination to use, and how an operator determines which mode a running instance is in. It publishes no events. The event publisher remains without callers, exactly as it is today, and group management behavior is untouched.

The difference between the two deployments is additive and side-effect-only. The Portal deployment performs every action the standalone deployment performs and additionally publishes events. It changes no read, no validation, no response, and no stored data. Because the difference is a strict, side-effect-only superset, a single code path with a discarding sink reproduces both deployments, and the active deployment is selected from configuration at startup rather than at compile time.

Key decisions:

- The deployment mode is derived solely from whether message broker addresses are configured. The mode is named `platform` when they are and `standalone` when they are not. Alternative rejected: compile-time selection using build tags, which leaves no valid default build, conflicts with the requirement that static analysis pass unconditionally, and doubles the build, release, and test surface.
- A single configuration value, the federated service name, determines both the destination and the identity each event declares. Alternative rejected: independent values for each, which permits combinations that disagree and fail asynchronously inside Authorization Service.
- The destination is carried as publisher state so that publish telemetry reports it rather than a fixed constant. Alternative rejected: leaving the trace attribute set to the default destination, which would misreport every non-default deployment.
- The mode and the federated service name are configured independently and may disagree. Neither combination is rejected, because broker presence is the only signal that determines behavior. Both are warned at startup so the disagreement is observable. Alternative rejected: deriving the mode from the name, which would make the name a fail-fast signal and would change behavior for existing deployments that set neither value.
- Group object naming is not derived. Alternative rejected: deriving it now for later use, which would add a configured value with no reader.
- The active mode is reported at startup and on the status endpoint. Because the code path is identical in both modes, these are the only means of distinguishing a running instance, which makes them part of the operational contract rather than incidental logging.

Non-goals:

- Publishing any permission event, for group creation or for anything else.
- Defining Portal's group authorization model, route rules, or schema.
- Any change to group management behavior, to any API request or response, or to stored data.
- Deriving or exposing group object naming.
- Failing startup when a platform deployment lacks broker configuration.

Backward compatibility: standalone behavior is unchanged. The default federated service name reproduces the existing destination and declared identity exactly. No schema change, no migration, no change to stored data, and no change to any API request or response. The only changes visible to a standalone deployment are the wording of startup log messages and a new status field. This capability introduces no breaking changes.

## ADDED Requirements

### Requirement: Deployment mode derived from broker configuration

The system SHALL derive the deployment mode at startup from whether message broker addresses are configured, and SHALL NOT consider any other input when determining it.

#### Scenario: Platform mode active

- **WHEN** hook-service starts with one or more broker addresses configured
- **THEN** the active mode is `platform`
- **THEN** the event publisher is initialized against the configured brokers and the derived destination
- **THEN** startup succeeds

#### Scenario: Standalone mode active

- **WHEN** hook-service starts with no broker addresses configured
- **THEN** the active mode is `standalone`
- **THEN** a discarding publisher is used in place of the event publisher
- **THEN** startup succeeds

#### Scenario: Invalid broker addresses rejected

- **WHEN** hook-service starts with broker addresses that are not valid host and port pairs
- **THEN** startup fails with an error identifying the invalid address
- **THEN** no partially initialized publisher is left in place

#### Scenario: No events published in either mode

- **WHEN** any group is created, updated, or deleted, or users are added to or removed from a group, in either mode
- **THEN** no permission event is published
- **THEN** the operation's behavior, response, and stored result are unchanged from before this capability existed

### Requirement: Destination and declared identity derived from the federated service name

The system SHALL derive the permission event destination and the identity declared in each published event from a single configured federated service name, so that the two cannot be configured to disagree.

#### Scenario: Default federated service name

- **WHEN** no federated service name is configured
- **THEN** the name defaults to `hook-service`
- **THEN** the destination is `hook-service.permissions`
- **THEN** the publisher declares its identity as `hook-service`, matching the value used before this capability existed

#### Scenario: Portal federated service name

- **WHEN** the federated service name is configured as `portal`
- **THEN** the destination is `portal.permissions`
- **THEN** the publisher declares its identity as `portal`

#### Scenario: Empty federated service name rejected

- **WHEN** the federated service name is configured as an empty or whitespace-only value
- **THEN** startup fails with an error identifying the invalid name
- **THEN** no destination is derived from it

#### Scenario: Federated service name yielding an unusable destination rejected

- **WHEN** the federated service name contains characters that are not permitted in a destination name, or is long enough that the derived destination exceeds the maximum permitted length
- **THEN** startup fails with an error identifying the invalid name
- **THEN** the failure occurs at startup rather than asynchronously at the broker

#### Scenario: Name validated regardless of the active mode

- **WHEN** the federated service name is invalid and no brokers are configured
- **THEN** startup still fails, because an invalid name is a configuration error whether or not the active mode consumes it
- **THEN** the failure is not deferred until brokers are first configured

#### Scenario: Telemetry reports the configured destination

- **WHEN** a publish is attempted
- **THEN** the trace span for that publish reports the destination the publisher was configured with
- **THEN** it does not report the default destination when a non-default federated service name is configured

### Requirement: Active mode reported to operators

The system SHALL report the active mode at startup and on the status endpoint, so that the behavior of a running instance can be determined without access to its configuration.

#### Scenario: Platform mode reported

- **WHEN** hook-service starts in platform mode
- **THEN** the startup log states that platform mode is active and names the federated service name, the derived destination, and the configured brokers
- **THEN** the status endpoint reports the active mode as `platform`

#### Scenario: Standalone mode reported

- **WHEN** hook-service starts in standalone mode
- **THEN** the startup log states that standalone mode is active and that permission event publishing is disabled
- **THEN** the status endpoint reports the active mode as `standalone`

#### Scenario: Reported mode matches the initialized publisher

- **WHEN** the status endpoint reports an active mode
- **THEN** that mode reflects the publisher selected at startup rather than a value read from the environment when the endpoint is served

### Requirement: Disagreement between the configured name and the active mode warned

The system SHALL warn at startup when the configured federated service name and the active mode disagree, in either direction, and SHALL start successfully regardless.

#### Scenario: Named deployment without brokers

- **WHEN** hook-service starts with a non-default federated service name and no broker addresses configured
- **THEN** a warning is logged stating that a named deployment is configured but the instance will publish nothing
- **THEN** startup succeeds and the active mode is `standalone`

#### Scenario: Default name with brokers

- **WHEN** hook-service starts with broker addresses configured and the default federated service name
- **THEN** a warning is logged stating that platform mode is active under the default federated service name
- **THEN** startup succeeds and the active mode is `platform`

#### Scenario: Agreement produces no warning

- **WHEN** the configured federated service name and the active mode agree, being either a non-default name with brokers configured or the default name with none
- **THEN** no disagreement warning is logged
