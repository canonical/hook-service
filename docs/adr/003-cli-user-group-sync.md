# ADR-003: CLI User/Group Operations and Import Sync Mode

**Status**: Accepted  
**Date**: 2026-05-04  
**Deciders**: Development Team  
**Issue**: [#194](https://github.com/canonical/hook-service/issues/194)

## Context

After decoupling Salesforce from the token hook hot path (ADR-001), all group data lives in the local database. The `import` command can populate groups from Salesforce but only ever adds — it never removes. This means:

- Users who leave the organisation remain in groups indefinitely.
- Department transfers cannot be reconciled without manual SQL.
- The database drifts from the source of truth over time.
- There is no way to perform any user/group membership operations without the server running.

## Decision

1. Add **CLI subcommands** for manual user and group membership management:
   - `hook-service users delete <user-id>` — remove a user from all groups
   - `hook-service users list-groups <user-id>` — list all groups a user belongs to
   - `hook-service users set-groups <user-id> --group ...` — replace a user's group memberships
   - `hook-service groups add-users <group-id> --user ...` — add users to a group
   - `hook-service groups remove-users <group-id> --user ...` — remove users from a group
   - `hook-service groups list-users <group-id>` — list all users in a group

2. Add a **sync mode** to the existing `import` command via `--sync`:  
   `hook-service import --driver salesforce --sync --dsn ...`  
   Sync reconciles the database with the driver, removing stale memberships and groups.

## Rationale

### User identification by email

Users are identified by **email** throughout the system. There is no `users` table — users exist implicitly via `group_members.user_id` rows. The Salesforce driver stores `fHCM2__Email__c` as `UserID`, and the token hook looks up groups by `user.Email` from the Hydra session. CLI commands therefore accept email addresses as user IDs.

### Sync scope

**Only groups prefixed with the driver's prefix are affected by `--sync`.** Local/admin-created groups are never touched. This is the critical safety invariant.

The prefix (`salesforce:`) is provided by the `DriverInterface.Prefix()` method. The `ListGroupsByPrefix` storage operation uses a lexicographic range query (`name >= prefix AND name < successor`) to guarantee B-tree index usage regardless of database collation. The successor is computed by incrementing the last byte of the prefix (e.g., `":"` (0x3A) → `";"` (0x3B)).

### Sync algorithm

```
Sync(ctx):
  1. FetchAllUserGroups(ctx) from driver → mappings
  2. Build driverGroups map[prefixedName][]userID
  3. ListGroupsByPrefix(ctx, prefix+":", tenantID) → existingGroups
  4. Build existingByName map[name]*Group for O(1) lookup

  5. For each (name, userIDs) in driverGroups:
     if exists in existingByName → SyncGroupMembers (reconcile membership)
     else → CreateGroup then AddUsersToGroup (new group)
     On error: log and continue (non-fatal)

  6. For each group in existingGroups NOT in driverGroups:
     DeleteGroup (stale group — remove)
     On error: log and continue (non-fatal)

  7. Log summary: synced, created, deleted counts
```

### Email change handling

When a user's email changes in Salesforce, sync handles it naturally:

- Old email: removed from all `salesforce:*` groups (no longer in driver data)
- New email: added to appropriate `salesforce:*` groups

Local group memberships with the old email become stale but harmless. Admins can clean up with `users delete <old-email>`.

### OpenFGA consistency

All stored OpenFGA tuples are group-level (`group:<id>#member → can_access → client:<id>`). User-group membership is passed as contextual tuples at check time only. This means:

- **Removing a user from groups requires zero OpenFGA cleanup** — no per-user tuples exist.
- **Deleting a group** normally requires OpenFGA cleanup. However, the importer is a CLI tool without an OpenFGA client — it calls `storage.DeleteGroup` (DB-level only), not `pkg/groups.Service.DeleteGroup` (which also cleans up OpenFGA tuples).

The orphaned OpenFGA tuples for deleted groups are harmless dead data: `FetchUserGroups` (DB query) always runs before `AuthorizeRequest`, so no contextual tuples will ever be built for a deleted group's ID.

### Index strategy

The `ListGroupsByPrefix` query uses `LIKE 'prefix%'` for prefix matching. This is semantically correct across all PostgreSQL collations, including `en_US.UTF-8` where a byte-level lexicographic range approach (`name >= prefix AND name < successor`) does not work correctly because letters sort differently relative to punctuation characters (e.g., `:` vs `;`) under locale-aware collation. The query relies on the existing `idx_groups_name(tenant_id, name)` composite index for the `tenant_id` equality predicate; the LIKE scan operates on the narrowed result set. At current scale (hundreds of groups per tenant), this is efficient. A dedicated `text_pattern_ops` index can be added in future if prefix scans become a bottleneck.

### Idempotence

All operations are designed to be idempotent:
- `users delete` succeeds even if the user has no memberships
- `groups remove-users` succeeds even if users are not members
- `import --sync` is safe to run multiple times

## Alternatives Considered

### Modify `Run()` to also remove stale data
- ❌ Breaking change for existing users who rely on additive-only behaviour
- ❌ Destroying data in a background operation is not safe without explicit opt-in
- ✅ Keeping `Run()` unchanged and adding `Sync()` preserves backward compatibility

### gRPC API for user/group management
- ✅ Server-side validation and authorization
- ❌ Requires the server to be running
- ❌ Over-engineered for offline administrative operations
- ✅ CLI commands work directly against the database, suitable for operator tooling

### Per-user OpenFGA cleanup in importer sync
- ❌ Requires wiring an OpenFGA client into the CLI tool
- ❌ Complex dependency for a batch operation
- ✅ Orphaned tuples are harmless; full cleanup available via gRPC API's `DeleteGroup` endpoint

## Consequences

### Positive
- Operators can manage group memberships without running the server
- Import `--sync` allows automated reconciliation with external sources
- Data staleness from the original `import` command is now fully addressable
- All operations are idempotent and safe to retry

### Negative
- **Known limitation**: `import --sync` does not clean up OpenFGA tuples for deleted groups. A future enhancement could add an OpenFGA cleanup pass (or admins can use the gRPC API's `DeleteGroup` endpoint, which does full cleanup).
- **`users delete` removes from ALL groups** — not just driver-prefixed ones. This is intentional (explicit admin action) but must be documented for operators.

### Neutral
- Local groups are never affected by `import --sync`.
- `users delete` + `import --sync` are complementary: sync handles bulk reconciliation, `users delete` handles individual user offboarding.

## Implementation Notes

### New storage operations

```go
RemoveUserFromAllGroups(ctx context.Context, userID string) error
ListGroupsByPrefix(ctx context.Context, prefix, tenantID string) ([]*types.Group, error)
SyncGroupMembers(ctx context.Context, groupID string, userIDs []string) error
```

### CLI usage

```bash
# Remove a user from all groups
hook-service users delete alice@example.com --dsn "postgres://..."

# List groups for a user (JSON output)
hook-service users list-groups alice@example.com --dsn "postgres://..." --format json

# Replace a user's group memberships
hook-service users set-groups alice@example.com --group group-id-1 --group group-id-2 --dsn "postgres://..."

# Add users to a group
hook-service groups add-users group-id-1 --user alice@example.com --user bob@example.com --dsn "postgres://..."

# Run sync (reconcile DB with Salesforce)
hook-service import --driver salesforce --sync --dsn "postgres://..." \
  --domain sf.example.com --consumer-key KEY --consumer-secret SECRET
```
