## Purpose

Ensure that OAuth2 access tokens and OpenID Connect ID tokens issued or refreshed through Ory Hydra always carry the subject's current group memberships. During a token refresh, Ory Hydra sends the previous token session claims back to the token hook endpoint.

Previously, the token hook copied all existing session claims into the response and returned early when the subject belonged to zero groups. When an administrator removed a user from all groups, refreshed tokens kept the stale `groups` claim from the earlier session. This specification defines the authoritative group-claim contract for the token hook response so that group removals take effect immediately on token refresh.

Goals:

- Guarantee that the `groups` claim in `session.access_token` and `session.id_token` always reflects the groups resolved during the current hook invocation.
- Clear stale `groups` claims from prior sessions when a subject has zero groups at refresh time.
- Preserve all other session claims that Ory Hydra passes in the hook request payload.

Key decisions:

- The token hook unconditionally writes the `groups` claim to both `session.access_token` and `session.id_token` as a JSON array (`[]` when empty). Alternative rejected: deleting the `groups` key or omitting it when empty, because Ory Hydra merges hook response claims into the stored session and downstream relying parties expect a consistent array type for `groups`.
- The token hook serializes an empty group set as `[]` rather than `null`. Alternative rejected: returning `null` via a nil slice, which forces downstream consumers to distinguish between `null` and an empty list.
- The token hook deduplicates group names before writing the `groups` claim. Alternative rejected: preserving duplicate entries from multiple group sources, which bloats token payloads without adding authorization meaning.

Non-goals:

- Changing how the service queries storage or external providers for user groups.
- Modifying OpenFGA authorization evaluation rules or client access checks.
- Changing how the service populates or overwrites the `tenant_id` claim.

Backward compatibility:

- Token responses for subjects with one or more groups remain unchanged.
- Token responses for subjects with zero groups now explicitly include `"groups": []` in `session.access_token` and `session.id_token` instead of omitting the key or retaining stale values. This change fixes a security and correctness defect where revoked group memberships persisted across token refreshes.

## ADDED Requirements

### Requirement: Authoritative group claims in token hook responses

The system SHALL set the `groups` claim in both `session.access_token` and `session.id_token` on every successful token hook response to the deduplicated list of group names resolved for the current request, and SHALL overwrite any `groups` claim present in the incoming session payload.

#### Scenario: Subject belongs to one or more groups

- **WHEN** Ory Hydra sends a valid token hook request for an authorized subject who belongs to groups `engineering` and `platform`
- **THEN** the response status is `200 OK`
- **AND** `session.access_token.groups` contains `engineering` and `platform`
- **AND** `session.id_token.groups` contains `engineering` and `platform`

#### Scenario: Duplicate group names are deduplicated

- **WHEN** the configured group providers return multiple groups with the same name `engineering` for an authorized subject
- **THEN** the response status is `200 OK`
- **AND** `session.access_token.groups` contains `engineering` exactly once
- **AND** `session.id_token.groups` contains `engineering` exactly once

#### Scenario: Subject has zero groups on initial token issuance

- **WHEN** Ory Hydra sends a valid token hook request with no prior `groups` session claim for an authorized subject who belongs to zero groups
- **THEN** the response status is `200 OK`
- **AND** `session.access_token.groups` is an empty list `[]`
- **AND** `session.id_token.groups` is an empty list `[]`

#### Scenario: Subject loses all groups before token refresh

- **WHEN** Ory Hydra sends a token hook request carrying a prior `groups` claim of `["group-a"]` in the access token or ID token session, and the authorized subject now belongs to zero groups
- **THEN** the response status is `200 OK`
- **AND** `session.access_token.groups` is overwritten with an empty list `[]`
- **AND** `session.id_token.groups` is overwritten with an empty list `[]`

#### Scenario: Subject group membership changes before token refresh

- **WHEN** Ory Hydra sends a token hook request carrying a prior `groups` claim of `["group-a", "group-b"]` in the session, and the authorized subject now belongs only to `group-b`
- **THEN** the response status is `200 OK`
- **AND** `session.access_token.groups` contains only `["group-b"]`
- **AND** `session.id_token.groups` contains only `["group-b"]`

### Requirement: Preservation of non-hook-managed session claims

The system SHALL preserve all incoming access token and ID token session claims that are not managed by the token hook when constructing the token hook response.

#### Scenario: Custom session claims preserved when groups are cleared

- **WHEN** Ory Hydra sends a token hook request containing custom session claims alongside a stale `groups` claim, and the authorized subject now belongs to zero groups
- **THEN** the response status is `200 OK`
- **AND** `session.access_token` retains all incoming custom access token claims unchanged
- **AND** `session.id_token` retains all incoming custom ID token claims unchanged
- **AND** `session.access_token.groups` and `session.id_token.groups` are both `[]`

#### Scenario: Tenant claim preserved alongside empty group claims

- **WHEN** Ory Hydra sends a token hook request for an authorized subject with a resolved `tenant_id` and zero groups
- **THEN** the response status is `200 OK`
- **AND** `session.access_token.tenant_id` and `session.id_token.tenant_id` equal the resolved tenant identifier
- **AND** `session.access_token.groups` and `session.id_token.groups` are both `[]`

### Requirement: Validation and authorization failure handling

The system SHALL reject malformed or unauthorized token hook requests without returning a token session payload.

#### Scenario: Malformed JSON request body rejected

- **WHEN** a caller sends a request with an invalid JSON body to the token hook endpoint
- **THEN** the response status is `400 Bad Request`
- **AND** the response body is empty

#### Scenario: Unauthorized subject denied

- **WHEN** Ory Hydra sends a token hook request for a subject who fails authorization checks
- **THEN** the response status is `403 Forbidden`
- **AND** the response body is empty
