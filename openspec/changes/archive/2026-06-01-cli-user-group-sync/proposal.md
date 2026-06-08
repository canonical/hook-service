## Why

Operators need to manage user-group memberships and reconcile external provider data (Salesforce) without running the full service. There is no mechanism today to inspect or correct group state directly from the command line, and the `import` command can only add data — it cannot remove stale groups when a provider removes them.

## What Changes

- **New CLI command `users`** with subcommands:
  - `users delete <user-id>` — remove a user from all groups
  - `users list-groups <user-id>` — list all groups a user belongs to
  - `users set-groups <user-id>` — replace a user's group memberships
- **New CLI command `groups`** with subcommands:
  - `groups add-users <group-id>` — add one or more users to a group
  - `groups remove-users <group-id>` — remove one or more users from a group
  - `groups list-users <group-id>` — list all members of a group
- **New `--sync` flag on the existing `import` command** — reconciles the database against the external driver, creating missing groups, syncing memberships, and deleting stale groups
- **New storage methods**: `RemoveUserFromAllGroups`, `ListGroupsByPrefix`, `SyncGroupMembers`
- **OpenFGA tuple cleanup on group deletion** — `Sync` now calls the authorizer to remove authorization tuples when it deletes a stale group, preventing privilege escalation via orphaned tuples
- **Fix: LIKE wildcard escaping** in `ListGroupsByPrefix` — prefix is escaped before use in SQL `LIKE` to prevent incorrect matches
- **Fix: `Sync` partial-failure reporting** — returns an error if any per-group operation fails rather than always returning `nil`
- **Fix: signal-aware context** in all CLI handlers — use `cmd.Context()` instead of `context.Background()`
- **Fix: input validation** — `--user` flag required on `groups add-users`; `--group` flag required on `users set-groups` to prevent accidental removal of all memberships
- **Fix: SPDX identifiers** in new files corrected from `AGPL-3.0` to `AGPL-3.0-only`

## Capabilities

### New Capabilities

- `cli-user-management`: Direct CLI management of user group memberships (delete, list-groups, set-groups)
- `cli-group-management`: Direct CLI management of group members (add-users, remove-users, list-users)
- `import-sync`: Reconciliation mode for the import command that removes stale driver-managed groups and their OpenFGA authorization tuples

### Modified Capabilities

(none — no existing spec files)

## Impact

- **`cmd/`**: two new files (`users.go`, `groups_cmd.go`) and modifications to `import.go`
- **`internal/importer/`**: new `Sync()` method, new `AuthorizerInterface`, updated `NewImporter` signature
- **`internal/storage/groups.go`**: three new methods added to `Storage` and `StorageInterface`
- **`internal/storage/interfaces.go`**: `StorageInterface` extended
- **No API surface change**: all new operations are CLI-only and bypass the gRPC API intentionally; the one exception is the authorizer call on group deletion which mirrors `pkg/groups.Service.DeleteGroup`
- **Operators** require a PostgreSQL DSN (`--dsn`) for all new commands; `--openfga-host` is optional for `import --sync` to enable OpenFGA cleanup
