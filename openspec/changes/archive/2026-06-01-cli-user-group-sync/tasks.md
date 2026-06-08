## 1. Storage Layer

- [x] 1.1 Add `RemoveUserFromAllGroups` to `internal/storage/groups.go`
- [x] 1.2 Add `ListGroupsByPrefix` to `internal/storage/groups.go`
- [x] 1.3 Add `SyncGroupMembers` to `internal/storage/groups.go`
- [x] 1.4 Extend `StorageInterface` in `internal/storage/interfaces.go` with the three new methods
- [x] 1.5 Escape LIKE wildcards (`%`, `_`, `\`) in `ListGroupsByPrefix` before constructing the query (add PostgreSQL `ESCAPE '\'` clause)

## 2. Importer — Sync

- [x] 2.1 Add `AuthorizerInterface` (single method: `DeleteGroup`) to `internal/importer/interfaces.go`
- [x] 2.2 Add `authz AuthorizerInterface` field to `Importer`; update `NewImporter` signature
- [x] 2.3 Implement `Importer.Sync()` — create/sync/delete loop over driver-prefixed groups
- [x] 2.4 Call `authz.DeleteGroup` after `storage.DeleteGroup` in the stale-group deletion loop
- [x] 2.5 Track per-group failures in `Sync` and return a non-nil error at the end if any operation failed (currently always returns `nil`)
- [x] 2.6 Add `mock_authorizer.go` for `AuthorizerInterface`
- [x] 2.7 Update `TestImporterSync` table to include `*MockAuthorizerInterface` and add `authz.EXPECT().DeleteGroup` expectation for the "stale groups deleted" case

## 3. CLI — `users` Command

- [x] 3.1 Create `cmd/users.go` with `users delete`, `users list-groups`, `users set-groups` subcommands
- [x] 3.2 Implement `newStorageFromCmd` helper (shared by `users` and `groups`)
- [x] 3.3 Mark `--group` as required on `users set-groups` (currently optional; omitting it silently removes user from all groups)
- [x] 3.4 Replace `context.Background()` with `cmd.Context()` in all three `users` handlers

## 4. CLI — `groups` Command

- [x] 4.1 Create `cmd/groups_cmd.go` with `groups add-users`, `groups remove-users`, `groups list-users` subcommands
- [x] 4.2 Mark `--user` as required on `groups add-users` (currently optional; omitting it is a silent no-op)
- [x] 4.3 Replace `context.Background()` with `cmd.Context()` in all three `groups` handlers

## 5. CLI — `import --sync`

- [x] 5.1 Add `--sync` flag to `importCmd`
- [x] 5.2 Add `--openfga-host`, `--openfga-store-id`, `--openfga-token`, `--openfga-model-id` flags to `importCmd`
- [x] 5.3 Implement `buildAuthorizer` in `cmd/import.go` (noop when `--openfga-host` is empty)
- [x] 5.4 Pass authorizer to `NewImporter` in `runImport`
- [x] 5.5 Replace `context.Background()` with `cmd.Context()` in `runImport`

## 6. Correctness Fixes

- [x] 6.1 Fix SPDX identifier in `cmd/groups_cmd.go`: `AGPL-3.0` → `AGPL-3.0-only`
- [x] 6.2 Fix SPDX identifier in `cmd/users.go`: `AGPL-3.0` → `AGPL-3.0-only`
- [x] 6.3 Fix SPDX identifier in `cmd/groups_cmd_test.go`: `AGPL-3.0` → `AGPL-3.0-only`
- [x] 6.4 Fix SPDX identifier in `cmd/users_test.go`: `AGPL-3.0` → `AGPL-3.0-only`

## 7. Tests

- [x] 7.1 `TestImporterRun` unit tests exist and pass
- [x] 7.2 `TestImporterSync` unit tests exist and pass (including partial-failure cases)
- [x] 7.3 Integration tests `TestImporterRunIntegration` and `TestImporterSyncIntegration` exist
- [x] 7.4 Add test for `Sync` partial-failure return value once task 2.5 is implemented
- [x] 7.5 Add test cases for `--user` required validation (task 4.2) and `--group` required validation (task 3.3)
- [x] 7.6 `TestGroupsAddUsersRequiresDSN`, `TestGroupsRemoveUsersRequiresDSN`, `TestGroupsListUsersRequiresDSN` exist
- [x] 7.7 `TestUsersDeleteRequiresDSN`, `TestUsersListGroupsRequiresDSN`, `TestUsersSetGroupsRequiresDSN` exist
