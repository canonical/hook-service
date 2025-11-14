// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

# Hook Service - AI Coding Agent Instructions

## Project Overview

This is an identity platform webhook service that integrates with Ory Kratos for identity management, Ory Hydra for OAuth2/OIDC flows, OpenFGA for fine-grained authorization, and optional Salesforce for group management. The service acts as a token hook endpoint that enriches OAuth tokens with user groups and enforces authorization policies.

## Architecture

### Core Components

- **pkg/hooks**: OAuth token hook handlers and orchestration - the main business logic entry point
- **pkg/authorization**: App-level authorization service with gRPC handlers exposing OpenFGA operations
- **pkg/groups**: Group management service with gRPC handlers for CRUD operations
- **internal/authorization**: OpenFGA authorization model management and client wrapper
- **internal/openfga**: Low-level OpenFGA client implementation with batching and model comparison
- **internal/salesforce**: Optional external group provider integration
- **pkg/web**: HTTP router setup using chi, wires all services together with middleware
- **cmd/serve.go**: Application bootstrap - dependency injection happens here

### Service Initialization Pattern

All services follow this constructor signature:
```go
func NewService(dependencies..., tracer TracingInterface, monitor MonitorInterface, logger LoggerInterface) *Service
```

The last three parameters (tracer, monitor, logger) are **always** in this order. This is a project-wide convention.

### Interface-Driven Design

- Every package defines `interfaces.go` with all dependencies as interfaces
- Concrete implementations are in `internal/` (e.g., `openfga.Client` implements `AuthzClientInterface`)
- Noop implementations exist for optional features (e.g., `openfga.NewNoopClient`)
- Mocks are generated via `go:generate` directives in test files using `go.uber.org/mock/mockgen`

### Data Flow

1. Hydra calls `/api/v0/hook/hydra` during token issuance
2. `hooks.Service.FetchUserGroups()` queries all registered `ClientInterface` implementations (e.g., Salesforce)
3. `hooks.Service.AuthorizeRequest()` checks OpenFGA for access based on user, client, and groups
4. Response enriches token with groups or denies request

## Development Workflow

### Setup
```bash
# Regenerate mocks after interface changes
go generate ./...

# Run tests with coverage
go test ./...
```

### Local Development
```bash
# Starts docker-compose with Kratos, Hydra, OpenFGA, Postgres
# This creates an environment that resembles production
./start.sh

# Or manually
make dev
```

The `start.sh` script provides a complete local development environment:
- Launches all dependencies via `docker-compose.dev.yml` (Kratos, Hydra, OpenFGA, Postgres, Mailslurper)
- Creates a test OAuth client in Hydra
- Starts an OIDC client for testing OAuth flows
- Sets environment variables for the service
- Use this to test the full integration stack locally

### Building
```bash
make build  # Produces ./app binary
```

### Testing Strategy

- Use table-driven tests with `tests := []struct{...}{{...}}`
- **Use only the standard library `testing` package for assertions.** Do not use external assertion libraries like `testify`.
- Mock all interfaces using gomock: `NewMock<Interface>(ctrl)`
- Always expect tracer.Start() calls with the correct span name format: `"package.Type.Method"`
- Test error paths explicitly - don't just test happy paths
- See `pkg/hooks/service_test.go` for canonical examples

Example test structure:
```go
tests := []struct{
    name           string
    input          InputType
    mockedDeps     func(*gomock.Controller) DependencyType
    expectedResult ResultType
    expectedError  error
}{{
    name: "descriptive test case name",
    input: ...,
    mockedDeps: func(ctrl *gomock.Controller) DependencyType {
        mock := NewMockInterface(ctrl)
        mock.EXPECT().Method(gomock.Any(), ...).Return(value, nil)
        return mock
    },
    expectedResult: ...,
}}
```

## Code Conventions

All code in this project follows strict conventions derived from Canonical's Go best practices. These are **mandatory** and non-negotiable for code review.

### File Headers
Every file must start with:
```go
// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0
```

### Error Handling Philosophy

**Error Messages**:
- Start with lowercase verbs: "cannot", "failed to", "invalid"
- Be concise - avoid verbose explanations in library code
- Example: `fmt.Errorf("cannot fetch groups: %w", err)` not `fmt.Errorf("An error occurred while attempting to fetch groups")`

**When to Add Context (`%w` vs `%v`)**:
- Use `%w` (wrap) only when the caller should inspect the error for recovery - this makes the inner error part of your public API
- Use `%v` (paste) for unrecoverable errors - prevents caller dependency on implementation details
- Only add context when crossing abstraction boundaries (e.g., between packages), not at every function call
- Example:
  ```go
  // Good - adds context at package boundary
  if err := internal.Fetch(); err != nil {
      return fmt.Errorf("failed to fetch user data: %v", err)
  }

  // Bad - adds noise without value
  if err := helper(); err != nil {
      return fmt.Errorf("helper failed: %v", err)
  }
  ```

**Error Return Values**:
- When error is `nil`: return valid data
- When error is non-`nil`: return zero values (e.g., `return nil, err`)
- Never return partial data with an error (exception: io.Reader patterns with explicit documentation)

**Custom Error Types**:
- Only create custom error types when callers need to handle them specially
- If an error is unrecoverable, use `errors.New()` or `fmt.Errorf()` - don't create a type

**Logging Errors**:
- Log errors only at the application edge (Handlers/Main), never in intermediate layers (Storage/Service) if the error is being returned.
- Prevents log noise (duplicate error entries) and keeps library code clean.

### Naming Conventions

**Receivers**:
- Always name receivers, even if unused: `func (s *Service) Method()` not `func (*Service) Method()`
- Use consistent receiver names across all methods of a type
- Typically use single letter or short abbreviation: `s *Service`, `a *Authorizer`

**Functions**:
- All functions (even unexported ones used across files) must have doc comments
- Doc comment format: `"FunctionName does something useful."` - start with function name
- Keep docs concise (1-2 sentences) - describe purpose, not implementation details
- Don't list parameters unless they have non-obvious behavior

**Variables**:
- Concise names without type suffixes: `needle` not `needleString`
- Exception: when disambiguating different types of the same concept: `needleString, ok := needle.(string)`
- Use US spelling throughout

### Code Structure

**Avoid Pyramids of Doom**:
```go
// Good - flat structure with early returns
func process(x string) error {
    if err := validate(x); err != nil {
        return err
    }
    if err := transform(x); err != nil {
        return err
    }
    return save(x)
}

// Bad - nested indentation
func process(x string) error {
    if err := validate(x); err == nil {
        if err := transform(x); err == nil {
            return save(x)
        } else {
            return err
        }
    } else {
        return err
    }
}
```

**Whitespace and Grouping**:
- Group strongly related code without blank lines
- Separate unrelated blocks with blank lines
- Variable declaration + immediate check = no blank line between them
- Don't interleave unrelated logic

**Variable Declaration**:
- Prefer `:=` for most declarations: `x := value`
- Use `var x Type` only when zero value is intentionally assigned before any reads
- Never use `var x = value` (verbose, no advantage over `:=`)
- Never use `var x Type = value` (maximum verbosity, no benefit)

### Interface Conventions

**Interface Declarations**:
- Always include parameter names for clarity: `Find(haystack, needle string) int`
- Every package defines `interfaces.go` with all external dependencies
- Use interface assertion where implementation is required: `var _ io.Reader = (*MyType)(nil)`

**Struct Initialization**:
- Always specify field names, never use anonymous initialization
- Match field order from type definition
- Use zero values to omit optional fields
- Example:
  ```go
  // Good
  return &Config{
      host:    "localhost",
      port:    8080,
      timeout: 30 * time.Second,
  }

  // Bad - anonymous initialization is fragile
  return &Config{"localhost", 8080, 30 * time.Second}
  ```

### Function Conventions

**Return Values**:
- Prefer returning `*Struct` over `Struct` (value)
- Name return values in signatures for clarity, but **never use bare returns**
- Example:
  ```go
  // Good - named for documentation, explicit return
  func Get(key string) (value string, err error) {
      if v, ok := cache[key]; ok {
          return v, nil
      }
      return "", fmt.Errorf("key not found: %s", key)
  }

  // Bad - bare return obscures control flow
  func Get(key string) (value string, err error) {
      if v, ok := cache[key]; ok {
          value = v
          return
      }
      err = fmt.Errorf("key not found: %s", key)
      return
  }
  ```

**Passing Structs**:
- Always pass and receive pointers to structs: `func Process(cfg *Config)` not `func Process(cfg Config)`
- Promotes consistent semantics and allows optional parameters

**Nil Checks**:
- Don't check if every argument is nil - this is caller misuse and undefined behavior
- Only check nil when nil is explicitly valid and documented
- Example:
  ```go
  // PrintFoo prints foo and optionally bar.
  // If bar is not needed, pass nil.
  func PrintFoo(foo *Foo, bar *Bar) {
      fmt.Println(foo.Name)
      if bar != nil {
          fmt.Println(bar.Name)
      }
  }
  ```

### Panic Usage

Panics are acceptable only when:
1. The fault is on the caller (API misuse): `panic("internal error: negative buffer size")`
2. Code is used where error handling isn't possible (e.g., `init()` functions)

For init-time convenience, provide both variants:
```go
// Foo parses input and returns error on failure.
func Foo(input string) (*Result, error)

// MustFoo parses input and panics on failure. Use in init() or globals.
func MustFoo(input string) *Result {
    result, err := Foo(input)
    if err != nil {
        panic(err)
    }
    return result
}
```

## Database and Storage Layer

### Architecture

The project uses a 3-layer architecture for data access:

1. **Storage Layer** (`internal/storage/`): Database operations using Squirrel SQL builder
2. **Service Layer** (`pkg/*/service.go`): Business logic that maps storage errors to domain errors
3. **Handler Layer** (`pkg/*/grpc_handlers.go`): Maps domain errors to HTTP/gRPC status codes

### Storage Layer Conventions

**Database Access**:
- Use `s.db.Statement(ctx)` to get a Squirrel StatementBuilder
- Automatically uses transactions if present in context (via transaction middleware)
- Always use `defer rows.Close()` after querying rows
- Use `sq.Eq{"field": value}` for WHERE clauses, never string concatenation

**Time Handling**:
- Always use `time.Now().UTC()` when generating timestamps for storage
- Never use `time.Now()` directly for persistence to avoid timezone inconsistencies

**Error Detection**:
- Use `internal/db` package functions to detect PostgreSQL errors:
  - `db.IsDuplicateKeyError(err)` for unique constraint violations (23505)
  - `db.IsForeignKeyViolation(err)` for foreign key violations (23503)
- Return wrapped errors with context: `db.WrapDuplicateKeyError(err, "context")`
- Use `sql.ErrNoRows` detection for not-found cases: `if err == sql.ErrNoRows { return nil, db.ErrNotFound }`

**Sentinel Errors**:
```go
// internal/storage/errors.go defines:
var (
    ErrNotFound            = errors.New("resource not found")
    ErrDuplicateKey        = errors.New("duplicate key violation")
    ErrForeignKeyViolation = errors.New("foreign key violation")
)
```

**DELETE Operation Guidelines**:
- DELETE operations should be **idempotent** - always return success even if nothing was deleted
- This follows REST best practices and enables safe retries
- Do NOT check `RowsAffected()` to return errors when 0 rows deleted
- Exception: GET/UPDATE operations should return `ErrNotFound` for missing resources
- DELETE operations using `DELETE...RETURNING` naturally return empty slices when nothing deleted (correct behavior)

```go
// Good - idempotent DELETE
func (s *Storage) DeleteGroup(ctx context.Context, id string) error {
    _, err := s.db.Statement(ctx).
        Delete("groups").
        Where(sq.Eq{"id": id}).
        ExecContext(ctx)
    if err != nil {
        return fmt.Errorf("failed to delete group: %v", err)
    }
    return nil  // Success even if group didn't exist
}

// Bad - not idempotent
func (s *Storage) DeleteGroup(ctx context.Context, id string) error {
    result, err := s.db.Statement(ctx).
        Delete("groups").
        Where(sq.Eq{"id": id}).
        ExecContext(ctx)
    if err != nil {
        return fmt.Errorf("failed to delete group: %v", err)
    }
    affected, _ := result.RowsAffected()
    if affected == 0 {
        return ErrNotFound  // Wrong - breaks idempotence
    }
    return nil
}
```

### Error Propagation Pattern

**Storage → Service → Handler**:

```go
// Storage Layer (internal/storage/groups.go)
func (s *Storage) CreateGroup(ctx context.Context, group *types.Group) (*types.Group, error) {
    // ... SQL execution ...
    if err != nil {
        if db.IsDuplicateKeyError(err) {
            return nil, db.WrapDuplicateKeyError(err, "group name already exists")
        }
        return nil, fmt.Errorf("failed to insert group: %v", err)
    }
}

// Service Layer (pkg/groups/service.go)
func (s *Service) CreateGroup(ctx context.Context, name, org, desc string, gType types.GroupType) (*types.Group, error) {
    group, err := s.db.CreateGroup(ctx, &types.Group{...})
    if err != nil {
        if errors.Is(err, db.ErrDuplicateKey) {
            return nil, ErrDuplicateGroup  // Domain error
        }
        return nil, err  // Propagate unrecoverable errors
    }
    return group, nil
}

// Handler Layer (pkg/groups/grpc_handlers.go)
if errors.Is(err, ErrDuplicateGroup) {
    return nil, status.Error(codes.AlreadyExists, err.Error())
}
```

**Key Rules**:
- Storage layer: Detect and wrap PostgreSQL errors with context
- Service layer: Map database errors to domain errors (ErrDuplicateGroup, ErrGroupNotFound)
- Handler layer: Map domain errors to HTTP/gRPC status codes
- Use `%v` (not `%w`) for unrecoverable errors - prevents implementation leakage
- Use `%w` only when caller needs to inspect/recover from the specific error

### Transaction Management

All HTTP requests are automatically wrapped in database transactions via `db.TransactionMiddleware`:
- Transaction created **lazily** on first `s.db.Statement(ctx)` call
- If no database operations occur, no transaction is created or committed
- Committed automatically if handler completes with status < 400
- Rolled back automatically on error or status >= 400
- Operations within same HTTP request are atomic
- Transaction context is propagated through `context.Context`

**Implementation Details**:
```go
// Middleware wires into router (pkg/web/router.go)
middlewares = append(middlewares, db.TransactionMiddleware(dbClient, logger))

// Storage layer automatically uses transaction if present
func (s *Storage) CreateGroup(ctx context.Context, group *types.Group) {
    // This will use transaction from middleware if in HTTP request context
    stmt := s.db.Statement(ctx)
    // ... query execution
}
```

**Example Scenarios**:
- `CreateGroup` + `AddUsersToGroup` in same HTTP request → atomic
- Service returns error after CreateGroup → entire operation rolled back
- Handler returns 404 or 500 → any DB changes rolled back
- Multiple database operations in same request → all or nothing

**Important Notes**:
- Don't manually create transactions in service or storage layers
- Let middleware handle transaction lifecycle
- Use `WithTx()` only for non-HTTP contexts (e.g., background jobs)
- Nested calls within same request share the same transaction

### Database Migrations

**Tooling**:
- Use `goose` for migration management
- Prefer `goose.NewProvider` over global `goose` functions to avoid global state
- CLI commands must support `--format json` for programmatic consumption (CI/CD)

**Commands**:
- `migrate check`: Verifies if migrations are pending without applying them (returns error if pending)
- `migrate status`: Displays migration history (supports JSON output)
- `migrate up/down`: Applies or rolls back migrations (supports JSON output)

**Output Standards**:
- Text format (default): Human-readable, uses `time.ANSIC` for timestamps
- JSON format (`--format json`): Structured output, returns empty list `[]` instead of `null` for empty results

## Key Environment Variables

See `internal/config/specs.go` for complete list:
- `AUTHORIZATION_ENABLED`: Enable OpenFGA checks (default: false)
- `OPENFGA_API_HOST`, `OPENFGA_STORE_ID`, `OPENFGA_API_TOKEN`: OpenFGA connection
- `SALESFORCE_ENABLED`: Enable Salesforce group provider (default: true)
- `SALESFORCE_DOMAIN`, `SALESFORCE_CONSUMER_KEY`, `SALESFORCE_CONSUMER_SECRET`: Salesforce OAuth
- `API_TOKEN`: Optional bearer token for webhook endpoint protection
- `LOG_LEVEL`: debug, info, error (default: error)
- `TRACING_ENABLED`: Enable OpenTelemetry (default: true)

## Common Patterns

### Adding a New Service
1. Create `pkg/newservice/interfaces.go` with `ServiceInterface`, `DatabaseInterface`, etc.
2. Implement `pkg/newservice/service.go` with `NewService()` constructor
3. Add gRPC handlers in `pkg/newservice/grpc_handlers.go` if exposing via API
4. Wire into `pkg/web/router.go` using `NewService()` and `NewGrpcServer()`
5. Add `//go:generate mockgen` directives in test files
6. Run `make mocks`

### Adding Authorization Checks
```go
// Check single object access
allowed, err := authorizer.Check(ctx, "user:123", "can_access", "client:abc")

// List all allowed objects of type
objects, err := authorizer.ListObjects(ctx, "user:123", "can_access", "client")

// Filter existing list
filtered, err := authorizer.FilterObjects(ctx, "user:123", "can_access", "client", candidateIDs)
```

### Adding Tracing
Every public method should start with:
```go
ctx, span := s.tracer.Start(ctx, "package.Service.MethodName")
defer span.End()
```

## Troubleshooting

- **Missing mocks**: Run `make mocks` - generates all `mock_*.go` files
- **Test failures**: Check mock expectations match actual calls, especially tracer.Start() span names
- **Docker issues**: `docker compose -f docker-compose.dev.yml down -v` to reset state

## What NOT to Do

- Don't create global mutable state - pass dependencies explicitly
- Don't use bare returns even with named return values
- Don't add context to every error - only at abstraction boundaries
- Don't nil-check arguments unless nil is valid (document if so)
- Don't mix `var` and `:=` declarations - use `:=` unless zero value isn't read
- Don't create custom error types unless caller needs to handle them specially

## Documentation Maintenance

**Self-Correction Directive**:
- If you establish a new pattern or convention during a conversation that isn't documented here, **you must update this file**.
- This ensures the instructions remain a living document and the single source of truth for project standards.
- Examples of updates: new error handling patterns, CLI output standards, testing conventions, or architectural decisions.
