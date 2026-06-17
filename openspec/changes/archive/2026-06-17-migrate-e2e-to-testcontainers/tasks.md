## 1. Groups Package Integration Tests

- [x] 1.1 Add Postgres testcontainers setup helper to [pkg/groups/groups_integration_test.go](file:///home/shipperizer/shipperizer/hook-service/pkg/groups/groups_integration_test.go)
- [x] 1.2 Port `TestGroupLifecycle` and `TestUserMembership` from [tests/e2e/e2e_test.go](file:///home/shipperizer/shipperizer/hook-service/tests/e2e/e2e_test.go) into [pkg/groups/groups_integration_test.go](file:///home/shipperizer/shipperizer/hook-service/pkg/groups/groups_integration_test.go), utilizing `httptest.NewServer`

## 2. Authorization Package Integration Tests

- [x] 2.1 Add Postgres and OpenFGA testcontainers setup helper to [pkg/authorization/authorization_integration_test.go](file:///home/shipperizer/shipperizer/hook-service/pkg/authorization/authorization_integration_test.go)
- [x] 2.2 Port `TestAppAuthorization` from [tests/e2e/e2e_test.go](file:///home/shipperizer/shipperizer/hook-service/tests/e2e/e2e_test.go) into [pkg/authorization/authorization_integration_test.go](file:///home/shipperizer/shipperizer/hook-service/pkg/authorization/authorization_integration_test.go), utilizing `httptest.NewServer`

## 3. Authentication Package Integration Tests

- [x] 3.1 Add Postgres and Ory Hydra testcontainers setup helper to [pkg/authentication/authentication_integration_test.go](file:///home/shipperizer/shipperizer/hook-service/pkg/authentication/authentication_integration_test.go)
- [x] 3.2 Port `TestJWTAuthentication` from [tests/e2e/e2e_test.go](file:///home/shipperizer/shipperizer/hook-service/tests/e2e/e2e_test.go) into [pkg/authentication/authentication_integration_test.go](file:///home/shipperizer/shipperizer/hook-service/pkg/authentication/authentication_integration_test.go), utilizing `httptest.NewServer`

## 4. Cleanup and Verification

- [x] 4.1 Delete legacy end-to-end test files: [tests/e2e/e2e_test.go](file:///home/shipperizer/shipperizer/hook-service/tests/e2e/e2e_test.go) and [tests/e2e/setup_test.go](file:///home/shipperizer/shipperizer/hook-service/tests/e2e/setup_test.go)
- [x] 4.2 Run static analysis (`go vet ./...`) and the test suite with race detection (`go test -race ./...`) to ensure compilation is clean and all tests succeed with zero diagnostic failures
