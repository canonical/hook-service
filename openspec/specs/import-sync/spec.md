## Purpose

Reconcile imported external-group state with database state so stale driver-managed groups and memberships are cleaned up safely and consistently.

This capability exists because imported identity/group data drifts over time if sync remains additive-only. The sync mode ensures the database and authorization state converge on the external source of truth without affecting unrelated local groups.

Key decisions:
- Reconciliation is opt-in via `--sync` and keeps additive import as the default behavior.
- Destructive cleanup is prefix-scoped so only driver-managed groups are eligible for deletion.
- OpenFGA tuple cleanup is coupled to stale-group deletion when OpenFGA is configured.
- Per-group failures are aggregated: processing continues, but exit status is non-zero.

Non-goals:
- This spec does not define reconciliation semantics for non-driver-managed local groups (groups outside the driver prefix).
- This spec does not require all group operations in a sync run to be atomic as a single global transaction.
- This spec does not define manual CLI membership operations (`users/*`, `groups/*`) outside import execution.

## Requirements

### Requirement: Reconcile database with driver data
The `import` command SHALL accept a `--sync` flag that, when present, reconciles the database against the current state of the external driver instead of performing an additive-only import. Reconciliation MUST:
- Create groups present in the driver but absent from the database
- Sync memberships for groups present in both the driver and the database
- Delete groups present in the database but absent from the driver (only those matching the driver's prefix)
- Never modify groups that do not match the driver's prefix

#### Scenario: New groups created
- **WHEN** operator runs `import --sync --driver salesforce ...`
- **AND** the driver returns a group that does not exist in the database
- **THEN** the group and its members are created in the database

#### Scenario: Existing group memberships reconciled
- **WHEN** operator runs `import --sync --driver salesforce ...`
- **AND** the driver returns a group that already exists in the database with different members
- **THEN** the group's members are updated to exactly match the driver's list

#### Scenario: Stale groups deleted
- **WHEN** operator runs `import --sync --driver salesforce ...`
- **AND** the database contains a prefixed group that is absent from the driver data
- **THEN** the group is deleted from the database
- **THEN** all OpenFGA authorization tuples for the deleted group are removed

#### Scenario: Non-prefixed groups untouched
- **WHEN** operator runs `import --sync --driver salesforce ...`
- **AND** the database contains a group whose name does not start with `salesforce:`
- **THEN** that group is not modified or deleted

### Requirement: Partial failure reporting
If one or more per-group operations fail during `--sync`, the `import` command SHALL exit with a non-zero exit code after completing all remaining operations. Individual failures SHALL be logged.

#### Scenario: One group fails, others succeed
- **WHEN** `import --sync` encounters a DB error for one group
- **THEN** reconciliation continues for the remaining groups
- **THEN** the command exits with a non-zero exit code

#### Scenario: All groups succeed
- **WHEN** `import --sync` completes all operations without error
- **THEN** the command exits with code 0

### Requirement: OpenFGA tuple cleanup on group deletion
When `import --sync` deletes a stale group, it SHALL also remove all associated OpenFGA authorization tuples (via the authorizer). If `--openfga-host` is not provided, the cleanup step is skipped (noop authorizer).

#### Scenario: Stale group deleted with OpenFGA configured
- **WHEN** `import --sync` deletes a stale group
- **AND** `--openfga-host` is set
- **THEN** all OpenFGA tuples for that group are removed after the DB delete

#### Scenario: Stale group deleted without OpenFGA configured
- **WHEN** `import --sync` deletes a stale group
- **AND** `--openfga-host` is not set
- **THEN** the group is deleted from the database only (noop authorizer)

#### Scenario: OpenFGA failure does not block DB cleanup
- **WHEN** `import --sync` deletes a stale group from the DB but the authorizer call fails
- **THEN** the failure is logged
- **THEN** the overall sync continues for remaining groups

### Requirement: Safe LIKE prefix matching
The `ListGroupsByPrefix` storage method SHALL escape SQL LIKE wildcard characters (`%`, `_`, `\`) in the prefix argument before constructing the query, to prevent unintended group matches.

#### Scenario: Prefix with no special characters
- **WHEN** `ListGroupsByPrefix` is called with prefix `salesforce:`
- **THEN** only groups whose names start with `salesforce:` are returned

#### Scenario: Prefix with wildcard characters
- **WHEN** `ListGroupsByPrefix` is called with a prefix containing `%` or `_`
- **THEN** those characters are treated as literals, not wildcards

### Requirement: Signal-aware context in CLI handlers
All CLI command handlers SHALL use `cmd.Context()` for database and authorizer calls so that OS signals (SIGINT/SIGTERM) cancel in-flight operations.

#### Scenario: SIGINT cancels operation
- **WHEN** operator sends SIGINT while an `import --sync` operation is running
- **THEN** the in-flight database call is cancelled

