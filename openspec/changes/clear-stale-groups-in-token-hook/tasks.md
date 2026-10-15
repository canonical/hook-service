# Tasks

## 1. Token Hook Group Claim Handling (`pkg/hooks`)

- [x] 1.1 Update `API.newHookResponse` in `pkg/hooks/handlers.go` to set `"groups"` to `[]string{}` when empty, and verify with unit tests in `pkg/hooks/handlers_test.go`.
- [x] 1.2 Fix `createHookResponse` and the inverted `reflect.DeepEqual` check in `pkg/hooks/handlers_test.go`, and verify `go test -race -short ./pkg/hooks/...` passes.
- [x] 1.3 Add table-driven unit tests in `pkg/hooks/handlers_test.go` for empty groups, stale refresh groups, and custom claims, and verify standard library assertions pass.
- [x] 1.4 Add Testcontainers integration test in `pkg/hooks/hooks_integration_test.go` using `internal/testhelpers` (Postgres, OpenFGA) for group removal refresh, and verify `go test -race ./pkg/hooks/...` passes.

## 2. Documentation & Rollout (`AGENTS.md`)

- [x] 2.1 Update the data flow section in `AGENTS.md` to state that `/api/v0/hook/hydra` writes `"groups": []` when a subject has zero groups, and verify by inspection.
- [x] 2.2 Verify rollout readiness in `openspec/changes/clear-stale-groups-in-token-hook/design.md` confirming no database migrations or configuration changes are required.

## 3. Verification Suite (`pkg/hooks`)

- [x] 3.1 Run `go build ./...` and `go vet ./...` across the repository, and verify both commands output zero errors.
- [x] 3.2 Run `go test -race -short ./...` for the unit test suite, and verify zero diagnostic failures.
- [x] 3.3 Run `go test -race ./pkg/hooks/...` with a container runtime active, and verify unit and Testcontainers integration tests pass.
- [x] 3.4 Run `golangci-lint run --new-from-rev=HEAD ./pkg/hooks/...`, and verify zero new linter findings.

## Implementation Notes

- Updated `createHookRequestWithExtra` in `pkg/hooks/handlers_test.go` to use an empty `&IDTokenClaims{}` instead of injecting `"email": 2134` so that `expectedResponse` assertions match `resp.Session.IDToken` after fixing the inverted `reflect.DeepEqual` check.
