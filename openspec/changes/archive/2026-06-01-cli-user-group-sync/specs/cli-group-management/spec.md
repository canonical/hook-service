## ADDED Requirements

### Requirement: Add users to a group
The CLI SHALL provide a `groups add-users <group-id>` subcommand that adds the specified users to a group. The `--user` flag SHALL be required and repeatable.

#### Scenario: Users added
- **WHEN** operator runs `groups add-users <group-id> --user <id1> --user <id2> --dsn <dsn>`
- **THEN** the specified users are members of the group
- **THEN** the command exits with code 0

#### Scenario: Missing --user flag
- **WHEN** operator runs `groups add-users <group-id> --dsn <dsn>` without any `--user` flag
- **THEN** the command exits with a non-zero code and an error message

#### Scenario: JSON output
- **WHEN** operator runs `groups add-users <group-id> --user <id1> --dsn <dsn> --format json`
- **THEN** the command outputs `{"group_id": "<group-id>", "users_added": 1}` as valid JSON

### Requirement: Remove users from a group
The CLI SHALL provide a `groups remove-users <group-id>` subcommand that removes the specified users from a group. The operation SHALL be idempotent.

#### Scenario: Users removed
- **WHEN** operator runs `groups remove-users <group-id> --user <id1> --dsn <dsn>`
- **THEN** the specified users are no longer members of the group

#### Scenario: User not in group
- **WHEN** operator runs `groups remove-users <group-id> --user <id1> --dsn <dsn>` for a user not in the group
- **THEN** the command exits with code 0 (idempotent)

#### Scenario: JSON output
- **WHEN** operator runs `groups remove-users <group-id> --user <id1> --dsn <dsn> --format json`
- **THEN** the command outputs `{"group_id": "<group-id>", "users_removed": 1}` as valid JSON

### Requirement: List users in a group
The CLI SHALL provide a `groups list-users <group-id>` subcommand that prints all members of the specified group.

#### Scenario: Group has members
- **WHEN** operator runs `groups list-users <group-id> --dsn <dsn>`
- **THEN** each user ID is printed on a separate line

#### Scenario: Group has no members
- **WHEN** operator runs `groups list-users <group-id> --dsn <dsn>` for an empty group
- **THEN** the command exits with code 0 and prints nothing

#### Scenario: JSON output
- **WHEN** operator runs `groups list-users <group-id> --dsn <dsn> --format json`
- **THEN** the command outputs a JSON array of user ID strings (empty array when no members)

### Requirement: Exactly one positional argument required
All `groups` subcommands SHALL require exactly one positional argument (the group ID).

#### Scenario: No positional argument
- **WHEN** operator runs a `groups` subcommand with no positional argument
- **THEN** the command exits with a non-zero code

#### Scenario: Extra positional argument
- **WHEN** operator runs a `groups` subcommand with two positional arguments
- **THEN** the command exits with a non-zero code
