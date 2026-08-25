## Purpose

Add a Kafka-based asynchronous permission update pipeline for `group-in-claim` tuples. Hook-service publishes `PermissionUpdateEnvelope` messages to the `hook-service.permissions` Kafka topic; Cerberus's listener and worker pipeline consumes these messages and applies the corresponding tuples to the shared OpenFGA store, enabling Cerberus extAuthz to enforce per-group access control on admin API endpoints.

The `group-in-claim` type has an `admin` → `owner` role hierarchy and a decoupled `member` relation. On group creation, the creator is recorded in PostgreSQL with the owner role and receives an `owner` tuple. The per-group `admin` relation is never published — it exists only for the `__domain__` object. On user addition, `member` is published. On user removal, a `member` delete is published. On group deletion, the service queries PostgreSQL for the group's owner and members, executes the database deletion, and publishes `PERMISSION_OP_DELETE` events for the owner and all members to keep the OpenFGA store clean and synchronized.

Key decisions:
- Use `segmentio/kafka-go` as the Kafka producer library to match Cerberus's own choice.
- Publish protobuf `PermissionUpdateEnvelope` messages defined in the Cerberus repository. Hook-service only needs the message types, not the Cerberus gRPC service definitions.
- At-least-once delivery with idempotency keys (`subject:relation:object:op`) ensures duplicates are safe — Cerberus's worker uses `ON DUPLICATE WRITES IGNORE` and `ON MISSING DELETES IGNORE`.
- The producer uses `requiredAcks=RequireAll` for durability; `maxAttempts=3` for transient retries.
- Kafka delivery failures do not affect the database transaction — the DB write succeeds regardless. The database record is the source of truth.
- Existing direct OpenFGA write methods are preserved for app-to-group authorization which is not in scope. The Kafka publisher is additive, covering only `group-in-claim` tuples for admin API authorization.
- User-group membership is published to Kafka (for Cerberus extAuthz enforcement) AND remains in PostgreSQL `group_members` table (for the token hook's contextual tuples and group lifecycle management). Both systems need this data for their respective purposes.
- Group deletion cleans up all associated OpenFGA tuples (owner and members) via Kafka events.

Non-goals:
- This spec does not cover publishing app-to-group authorization tuples (`POST /groups/{id}/apps`). That remains out of scope.
- This spec does not define the Cerberus-side listener/worker pipeline — only the hook-service publisher side.
- This spec does not define the Kafka infrastructure deployment — topic creation, broker configuration, and the `KAFKA_FEDERATED_SERVICES` env var in Cerberus are assumed to be handled by operators.

## Requirements

### Requirement: Kafka permission publisher
The system SHALL provide a Kafka producer in `internal/kafka/` that publishes `PermissionUpdateEnvelope` protobuf messages to the `hook-service.permissions` topic.

#### Scenario: Publish a write operation
- **WHEN** `PublishWrite(ctx, subject, relation, object)` is called
- **THEN** a `PermissionUpdateEnvelope` with `PERMISSION_OP_WRITE` is published to Kafka
- **THEN** the message includes a unique `message_id`, an `idempotency_key` derived from `subject:relation:object:op`, and the current timestamp

#### Scenario: Publish a delete operation
- **WHEN** `PublishDelete(ctx, subject, relation, object)` is called
- **THEN** a `PermissionUpdateEnvelope` with `PERMISSION_OP_DELETE` is published to Kafka

#### Scenario: Kafka publish failure
- **WHEN** the Kafka broker is unreachable
- **THEN** the publish call retries up to `maxAttempts` times and returns an error if all attempts fail
- **THEN** the service layer logs a warning and continues (the database operation is not affected)

### Requirement: Group creation publishes owner tuple
The system SHALL publish an `owner` tuple for the group creator when a group is created. The per-group `admin` relation is never published.

#### Scenario: Group created
- **WHEN** `CreateGroup` is called successfully
- **THEN** the system publishes `PERMISSION_OP_WRITE` for `user:<creator_id> → owner → group-in-claim:<group_id>`

#### Scenario: Kafka publish fails during group creation
- **WHEN** `CreateGroup` succeeds in the database but the Kafka publish fails
- **THEN** the group creation succeeds (the database record is committed)
- **THEN** a warning is logged about the Kafka publish failure

### Requirement: User addition publishes member tuple
The system SHALL publish a `member` tuple when a user is added to a group.

#### Scenario: User added to group
- **WHEN** `AddUsersToGroup` is called successfully
- **THEN** the system publishes `PERMISSION_OP_WRITE` for `user:<user_id> → member → group-in-claim:<group_id>`

### Requirement: User removal publishes delete tuple
The system SHALL publish a delete tuple when a user is removed from a group.

#### Scenario: User removed from group
- **WHEN** `RemoveUserFromGroup` is called successfully
- **THEN** the system publishes `PERMISSION_OP_DELETE` for `user:<user_id> → member → group-in-claim:<group_id>`

### Requirement: Group deletion publishes delete tuples for owner and all members
The system SHALL publish delete tuples for the group's owner and all its members when a group is deleted.

#### Scenario: Group deletion publishes owner delete tuple
- **WHEN** `DeleteGroup` is called successfully
- **THEN** the system publishes `PERMISSION_OP_DELETE` for `user:<owner_id> → owner → group-in-claim:<group_id>`

#### Scenario: Group deletion publishes member delete tuples
- **WHEN** `DeleteGroup` is called successfully
- **THEN** the system publishes `PERMISSION_OP_DELETE` for each member `user:<member_id> → member → group-in-claim:<group_id>`

#### Scenario: Kafka publish fails during group deletion
- **WHEN** `DeleteGroup` succeeds in the database but one or more Kafka publishes fail
- **THEN** the group deletion succeeds (the database records are deleted)
- **THEN** a warning is logged about the Kafka publish failure