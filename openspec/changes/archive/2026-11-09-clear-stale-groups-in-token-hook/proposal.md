## Why

Ory Hydra calls the token hook endpoint during initial token issuance and subsequent token refresh flows. During a token refresh, Ory Hydra forwards the session claims from the previous token inside the hook request payload.

When a user loses all group memberships before a token refresh, the hook service fetches zero groups for that user. However, the hook handler preserves all incoming session claims and returns early when the resolved group list is empty. As a result, refreshed access tokens and ID tokens retain the stale `groups` claim from the previous token.

## What Changes

- Always write the `groups` claim in both `session.access_token` and `session.id_token` within the token hook response.
- Set `groups` to an empty JSON array (`[]`) when the hook resolves zero groups for the subject.
- Overwrite any pre-existing `groups` value carried in the incoming Hydra session payload during token refresh.
- Preserve all other non-hook-managed session claims in `session.access_token` and `session.id_token`.
- Fix the inverted session comparison assertion and ID token group expectations in the `pkg/hooks` unit test suite.

## Non-goals

- Modifying group resolution logic in `internal/storage` or external group providers.
- Changing OpenFGA authorization policies or evaluation rules in `internal/authorization` and `internal/openfga`.
- Altering how `tenant_id` or other non-group session claims populate the token response.

## Capabilities

### New Capabilities
- `token-hook-group-claims`: Defines the contract for populating, deduplicating, and clearing the `groups` claim in access and ID token sessions returned by the Hydra token hook endpoint.

### Modified Capabilities
- None.

## Success Metrics

- Refreshed tokens for users who lose all group memberships contain `"groups": []` in both `session.access_token` and `session.id_token`.
- Initial tokens for users with zero groups contain `"groups": []` in both `session.access_token` and `session.id_token`.
- Unrelated session claims in `req.Session.Extra` and `req.Session.DefaultSession.Claims.Extra` remain unchanged across token refreshes.
- Unit and integration tests in `pkg/hooks` pass with `-race` enabled and zero linter warnings.

## Impact

- **`pkg/hooks`**: Updates `API.newHookResponse` in `pkg/hooks/handlers.go` to unconditionally set `groups` on both token maps as a non-nil slice. Updates unit tests in `pkg/hooks/handlers_test.go` and adds container-backed integration coverage in `pkg/hooks/hooks_integration_test.go`.
