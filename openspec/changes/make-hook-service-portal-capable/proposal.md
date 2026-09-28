# Why

hook-service runs today as the Canonical Identity Platform standalone service. Administrative access is governed centrally by Authorization Service through role membership, and no permission events are published. A second deployment, Canonical Portal, will reuse the same codebase and will need to publish permission events to its own destination under its own identity.

Portal's group authorization model is not yet defined, so what those events should say is unknown. Two things can be settled now without knowing it: how a deployment is told which identity and destination to use, and how an operator determines which mode a running instance is in.

An earlier design proposed compiling one binary per deployment using build tags. This change instead selects behavior from configuration at startup, keeping one binary, one image, and one test suite. Recording that decision is the main purpose of this change.

## What Changes

- Introduce a single configuration value, the federated service name, which determines the permission event destination and the identity each event declares. It defaults to the current value, so standalone deployments need no configuration change.
- Name the two deployment modes: `platform` when a message broker is configured, `standalone` when none is.
- Report the active mode at startup and expose it on the status endpoint, so an operator can determine the behavior of a running instance.
- Warn at startup when the configured identity and the active mode disagree, in either direction.
- Remove the hardcoded service identity from the event publisher so a platform deployment can declare its own.

## Non-goals

- Publishing any permission event. The publisher remains without callers, as it is today. Group creation, deletion, and membership changes are unchanged.
- Defining Portal's group authorization model, route rules, or schema. These live in the Authorization Service repository.
- Any change to `pkg/groups`, to group management behavior, or to any API request or response.
- Any database, schema, or stored data change.
- Deriving group object naming. With no event published there is nothing to name, so the derivation is deliberately omitted rather than added unused.
- Failing startup when a platform deployment is missing broker configuration.
- Portal deployment, database, image, or continuous integration configuration.

## Capabilities

### New Capabilities

- `platform-deployment-mode`: Select the deployment mode from broker configuration, derive the permission event destination and declared identity from a single configured name, and report the active mode to operators.

### Modified Capabilities

- `authorization-service-kafka-permission-pipeline` (defined in the `onboard-admin-authz-service` change, not yet archived): its standby-mode requirement is unchanged in substance. The publisher still has no callers. Its destination and declared identity become configurable rather than fixed.

## Success Metrics

- A single binary serves both modes. `go build ./...` and `go vet ./...` pass without build tags.
- Every running instance reports its active mode in startup logs and on the status endpoint.
- Standalone deployments require no configuration change and publish zero events, unchanged from current behavior.
- A configured identity that disagrees with the active mode produces a startup warning rather than silent divergence.

## Impact

- `internal/config`: new federated service name value, destination derivation, and validation.
- `internal/kafka`: declared identity becomes instance state instead of a fixed constant.
- `pkg/status`: exposes the active mode.
- `cmd/serve.go`: derives configuration, reports the mode, warns on disagreement.
- `pkg/web/router.go`: passes the active mode to the status API.
- `scripts/setup-centralized-authz-e2e.sh`: stops configuring a broker for the standalone harness, which currently causes it to report platform mode.
- Project documentation: records the new configuration value, both derived values, and the two mode names; the rejected build-tag design stops being the recorded plan.
- `pkg/groups`: unchanged.
