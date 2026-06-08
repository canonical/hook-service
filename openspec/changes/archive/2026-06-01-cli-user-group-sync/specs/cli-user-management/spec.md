## ADDED Requirements

### Requirement: Delete user from all groups
The CLI SHALL provide a `users delete <user-id>` subcommand that removes the specified user from every group they belong to. The operation SHALL be idempotent — it succeeds even when the user has no memberships.

#### Scenario: User deleted from all groups
- **WHEN** operator runs `users delete <user-id> --dsn <dsn>`
- **THEN** the user is removed from all groups in the database
- **THEN** the command exits with code 0 and prints a confirmation message

#### Scenario: User has no memberships
- **WHEN** operator runs `users delete <user-id> --dsn <dsn>` for a user with no group memberships
- **THEN** the command exits with code 0 (idempotent)

#### Scenario: JSON output
- **WHEN** operator runs `users delete <user-id> --dsn <dsn> --format json`
- **THEN** the command outputs `{"user_id": "<user-id>", "status": "deleted"}` as valid JSON

#### Scenario: Missing DSN
- **WHEN** operator runs `users delete <user-id>` without `--dsn`
- **THEN** the command exits with a non-zero code and an error message

### Requirement: List groups for a user
The CLI SHALL provide a `users list-groups <user-id>` subcommand that prints all groups the specified user belongs to.

#### Scenario: User is in groups
- **WHEN** operator runs `users list-groups <user-id> --dsn <dsn>`
- **THEN** each group name is printed on a separate line

#### Scenario: User is in no groups
- **WHEN** operator runs `users list-groups <user-id> --dsn <dsn>` for a user with no memberships
- **THEN** the command exits with code 0 and prints nothing

#### Scenario: JSON output
- **WHEN** operator runs `users list-groups <user-id> --dsn <dsn> --format json`
- **THEN** the command outputs a JSON array of group objects (empty array when no memberships)

### Requirement: Set groups for a user
The CLI SHALL provide a `users set-groups <user-id>` subcommand that replaces the user's group memberships with the provided set. The `--group` flag SHALL be required to prevent accidental removal of all memberships.

#### Scenario: Groups replaced
- **WHEN** operator runs `users set-groups <user-id> --group <id1> --group <id2> --dsn <dsn>`
- **THEN** the user belongs exactly to the specified groups

#### Scenario: Missing --group flag
- **WHEN** operator runs `users set-groups <user-id> --dsn <dsn>` without any `--group` flag
- **THEN** the command exits with a non-zero code and an error message

#### Scenario: Context cancellation
- **WHEN** operator sends SIGINT while `users set-groups` is running
- **THEN** the in-flight database operation is cancelled and the command exits with a non-zero code

### Requirement: Exactly one positional argument required
All `users` subcommands SHALL require exactly one positional argument (the user ID). Too few or too many arguments SHALL result in a non-zero exit code.

#### Scenario: No positional argument
- **WHEN** operator runs a `users` subcommand with no positional argument
- **THEN** the command exits with a non-zero code

#### Scenario: Extra positional argument
- **WHEN** operator runs a `users` subcommand with two positional arguments
- **THEN** the command exits with a non-zero code
