## Context

The hook-service manages user-group memberships in PostgreSQL and uses OpenFGA for fine-grained authorization. The service exposes group operations via a gRPC API, but there is no command-line path for operators to inspect or repair group state, and the existing `import` command (which bulk-loads Salesforce data) can only add — it cannot remove groups that the provider has dropped.

The branch introduces two new CLI command trees (`users`, `groups`) and a `--sync` reconciliation mode on `import`. A code review identified several correctness and safety issues alongside the new features.

## Goals / Non-Goals

**Goals:**
- Define what the new `users` and `groups` CLI commands must do and how they must behave safely
- Define the `import --sync` reconciliation contract including OpenFGA side-effects
- Establish correctness requirements for `ListGroupsByPrefix` (LIKE escaping), partial-failure signalling in `Sync`, context propagation, and input validation

**Non-Goals:**
- Exposing the new operations via the gRPC/HTTP API
- Transaction atomicity for `Sync` (documented as a known trade-off)
- Pagination for list operations

## Decisions

### D1: Direct-DB access for CLI commands (not via service layer)

**Decision**: CLI commands call `storage.Storage` directly rather than the gRPC API.

**Rationale**: The CLI is an operator tool, not a user-facing API. Requiring the service to be running adds operational complexity (credentials, network, auth tokens). The direct path is acceptable for all read and additive operations. The only operation with an out-of-DB side-effect is `DeleteGroup`, which additionally requires OpenFGA tuple cleanup.

**Alternative considered**: Route everything through the gRPC API. Rejected because it requires the service to be up and adds auth complexity for a maintenance tool.

**Boundary rule**: Whenever a CLI operation matches one that `pkg/groups.Service` performs with extra side-effects (currently only `DeleteGroup`), the CLI must replicate those side-effects (call the authorizer) or refuse to perform the operation.

### D2: `AuthorizerInterface` injected into `Importer` (not full service)

**Decision**: Add a narrow `AuthorizerInterface` (one method: `DeleteGroup`) to the `importer` package rather than injecting `pkg/groups.ServiceInterface`.

**Rationale**: Injecting the full service would introduce a cross-package dependency that pulls in the entire service stack. The importer only needs to clean up authorization tuples on group deletion. A single-method interface keeps the dependency minimal and testable.

**Authorizer wiring**: When `--openfga-host` is not set, `cmd/import.go` uses `openfga.NewNoopClient`, making OpenFGA cleanup a no-op (correct when authorization is disabled).

### D3: `Sync` partial failures are non-fatal per group, but the function reports overall failure

**Decision**: Per-group errors in `Sync` are logged and the loop continues (to maximize progress), but the function returns a non-nil error at the end if any operation failed.

**Rationale**: Stopping on the first failure would leave many groups unprocessed for what may be a transient DB error. Silently returning `nil` makes CI pipelines unable to detect a broken sync. Logging each failure and returning an aggregate error at the end is the best trade-off.

### D4: LIKE prefix escaping in `ListGroupsByPrefix`

**Decision**: Escape `%`, `_`, and `\` in the prefix argument before constructing the LIKE clause, using PostgreSQL's `ESCAPE '\'` syntax.

**Rationale**: The comment on the existing function claims escaping is applied — it is not. Any prefix containing wildcard characters would match unintended groups. While the current `SalesforceDriver` prefix (`salesforce`) is safe, the `DriverInterface` is open to extension.

### D5: `cmd.Context()` for all CLI handlers

**Decision**: All CLI handler functions use `cmd.Context()` rather than `context.Background()`.

**Rationale**: Cobra propagates OS signals (SIGINT/SIGTERM) through `cmd.Context()`. Using `context.Background()` means Ctrl+C cannot cancel in-flight database operations.

## Risks / Trade-offs

- **Non-atomic Sync** → Mitigation: documented limitation; operators can re-run `--sync` safely because `SyncGroupMembers` is idempotent and `DeleteGroup` is idempotent. A future improvement could wrap the entire sync in a transaction via `db.WithTx`.
- **OpenFGA failure after DB delete** → Mitigation: the authorizer failure is logged but does not roll back the DB delete. A future improvement could run the authorizer call first (fail-fast) before the DB delete.
- **Direct DB write bypasses service validation** → Mitigation: acceptable for the identified operations; any new CLI operation that touches authz must follow the same pattern as `DeleteGroup`.

## Open Questions

(none — all questions resolved during design)
