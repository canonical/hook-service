## ADDED Requirements

### Requirement: Centralized Integration Test Helpers
Integration test helpers and container fixtures MUST be centralized in `internal/testhelpers`.

#### Scenario: Running unit tests with short flag
Given the test suite is executed with `-short`
When tests run
Then no containers are started and integration tests are skipped.
