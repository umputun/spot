# Spot Development Guide

## Project Overview

**Spot** is a powerful deployment and configuration management tool written in Go. It allows users to define playbooks with tasks and execute them on remote hosts concurrently. Key features include:

- Task execution with multiple command types (script, copy, sync, delete, echo, wait)
- Concurrent execution with configurable parallelism
- Built-in secrets management with multiple providers
- Inventory management with groups and tags
- Dry-run mode for testing
- Simple design with minimal dependencies

## Architecture Overview

### Core Components

1. **Entry Points** (`cmd/`)
   - `spot/main.go` - Main CLI application for running playbooks and ad-hoc commands
   - `secrets/main.go` - Separate tool for managing secrets (spot-secrets)

2. **Core Packages** (`pkg/`)
   - **config/** - Handles playbook parsing, inventory management, and configuration structures
   - **executor/** - Provides execution interfaces with implementations for:
     - Remote execution over SSH (`remote.go`)
     - Local execution (`local.go`)
     - Dry-run mode (`dry.go`)
   - **runner/** - Orchestrates task execution across multiple hosts with concurrency control
   - **secrets/** - Implements multiple secrets providers (Spot DB, AWS Secrets Manager, HashiCorp Vault, Ansible Vault)

### Key Interfaces and Abstractions

1. **executor.Interface** - Core abstraction for command execution:
   ```go
   type Interface interface {
       Run(ctx context.Context, c string, opts *RunOpts) (out []string, err error)
       Upload(ctx context.Context, local, remote string, opts *UpDownOpts) (err error)
       Download(ctx context.Context, remote, local string, opts *UpDownOpts) (err error)
       Sync(ctx context.Context, localDir, remoteDir string, opts *SyncOpts) ([]string, error)
       Delete(ctx context.Context, remoteFile string, opts *DeleteOpts) (err error)
       Close() error
   }
   ```

2. **runner.Connector** - Abstraction for SSH connections
3. **runner.Playbook** - Interface for playbook operations
4. **config.SecretsProvider** - Interface for secrets management

### Execution Flow

1. **CLI Entry Point** (`cmd/spot/main.go`)
   - Parses command-line options and loads playbook configuration
   - Creates a `runner.Process` with configured concurrency, connector, and options
   - Calls `runTaskForTarget()` for each task/target combination

2. **Task Orchestration** (`pkg/runner/runner.go`)
   - Uses `syncs.NewErrSizedGroup` for concurrent execution with configurable parallelism
   - Each host runs in a separate goroutine, limited by `Concurrency` setting
   - Executes commands sequentially within each host's goroutine
   - Manages task-level variables and registered variables across commands
   - Handles on-error and on-exit command execution

3. **Command Execution** (`pkg/runner/commands.go`)
   - Supports multiple command types: script, copy, sync, delete, wait, echo
   - Template variable substitution (SPOT_REMOTE_HOST, etc.)
   - Sudo handling via temporary directories
   - Register variable extraction from script output
   - On-exit command deferral

4. **Connection Management**
   - No persistent connection pool - each task execution creates new SSH connections
   - Connections are created per-host, per-task execution
   - SSH agent support with optional forwarding
   - Clean separation: each executor instance owns its connection

### Configuration Capabilities

1. **Playbook Format**
   - Supports both YAML and TOML formats
   - Two playbook types: full (complex) and simplified
   - Key components: user, ssh_key, inventory, targets, tasks

2. **Inventory Management**
   - YAML/TOML format with groups and hosts
   - Can be file-based or URL-based (HTTP)
   - Supports host groups, names, and tags for targeting
   - Special "all" group automatically created containing all hosts

3. **Variable System**
   - Template variables: `$SPOT_REMOTE_HOST`, `${SPOT_REMOTE_HOST}`, `{SPOT_REMOTE_HOST}`
   - Environment variables passed through `env` field
   - Register variables to capture command output
   - Dynamic targets using `$variable` syntax

4. **Secrets Management**
   - Multiple providers with consistent interface
   - Secrets encrypted using NaCl Secretbox with Argon2 key derivation
   - Secrets referenced in playbooks and loaded at runtime

### Testing Patterns

1. **Container-Based Integration Tests**
   - Uses `testcontainers-go` for real SSH testing
   - SSH container setup using `lscr.io/linuxserver/openssh-server`
   - Helper function `startTestContainer` returns host:port and teardown func

2. **Mock Generation**
   - Uses moq with `//go:generate` directives
   - Mocks stored in `mocks` subdirectory
   - Example: `//go:generate moq -out mocks/connector.go -pkg mocks -skip-ensure -fmt goimports . Connector`

3. **Test Structure**
   - Table-driven tests with subtests using testify
   - Compact test formatting (single line struct fields)
   - `testdata` directories for test fixtures
   - Real SSH connection tests against containers

## Primary Guidelines

- Provide brutally honest and realistic assessments of requests, feasibility, and potential issues. No sugar-coating. No vague possibilities where concrete answers are needed.
- Always operate under the assumption that the user might be incorrect, misunderstanding concepts, or providing incomplete/flawed information. Critically evaluate statements and ask clarifying questions when needed.
- Don't be flattering or overly positive. Be honest and direct.
- We work as equal partners and treat each other with respect as two senior developers with equal expertise and experience.
- Prefer simple and focused solutions that are easy to understand, maintain and test.

## Build, Lint and Test Commands

```bash
# Build all binaries
go build -ldflags "-X main.revision=$(git describe --tags --abbrev=0 --exact-match 2>/dev/null || git rev-parse --abbrev-ref HEAD)-$(git rev-parse --short=7 HEAD)-$(git log -1 --format=%ct HEAD | xargs -I{} date -u -r {} +%Y%m%dT%H%M%S) -s -w" -o .bin/spot ./cmd/spot
go build -ldflags "-X main.revision=$(git describe --tags --abbrev=0 --exact-match 2>/dev/null || git rev-parse --abbrev-ref HEAD)-$(git rev-parse --short=7 HEAD)-$(git log -1 --format=%ct HEAD | xargs -I{} date -u -r {} +%Y%m%dT%H%M%S) -s -w" -o .bin/spot-secrets ./cmd/secrets

# Run all tests with race detection and coverage
go test -race -coverprofile=coverage.out ./...

# Run a specific test
go test ./pkg/executor -run TestExecuter_Run

# Run tests with coverage
go test -cover ./...

# Check race conditions
go test -race ./...

# Lint code (always run from the top level)
golangci-lint run

# Format code (excluding vendor)
gofmt -s -w $(find . -type f -name "*.go" -not -path "./vendor/*")

# Run goimports (excluding vendor)
goimports -w $(find . -type f -name "*.go" -not -path "./vendor/*")

# Run code generation
go generate ./...

# Coverage report (excluding mocks)
go test -race -coverprofile=coverage.out ./... && grep -v "_mock.go" coverage.out | grep -v mocks > coverage_no_mocks.out && go tool cover -func=coverage_no_mocks.out && rm coverage.out coverage_no_mocks.out

# Normalize code comments
unfuck-ai-comments run --fmt --skip=mocks ./...

# Run completion sequence (formatting, code generation, linting, testing)
gofmt -s -w $(find . -type f -name "*.go" -not -path "./vendor/*") && go generate ./... && golangci-lint run && go test -race ./...
```

**IMPORTANT:** NEVER commit without running tests, formatter, comments normalizer and linters for the entire codebase!

## Important Workflow Notes

- Always run tests, linter and normalize comments BEFORE committing anything
- Run formatting, code generation, linting and testing on completion
- Never commit without running completion sequence
- Run tests and linter after making significant changes to verify functionality
- IMPORTANT: Never put into commit message any mention of Claude or Claude Code
- Do not include "Test plan" sections in PR descriptions
- Do not add comments that describe changes, progress, or historical modifications
- Comments should only describe the current state and purpose of the code, not its history or evolution
- Use `go:generate` for generating mocks, never modify generated files manually
- Mocks are generated with `moq` and stored in the `mocks` package
- After important functionality added, update README.md accordingly
- When merging master changes to an active branch, make sure both branches are pulled and up to date first
- Don't leave commented out code in place
- If working with github repos use `gh`
- Avoid multi-level nesting
- Avoid multi-level ifs, never use else if
- Never use goto
- Avoid else branches if possible
- Write tests in compact form by fitting struct fields to a single line (up to 130 characters)
- Before any significant refactoring, ensure all tests pass and consider creating a new branch
- The standard location for documentation and spec is `docs/`
- When refactoring, editing, or fixing failed tests:
  - Do not redesign fundamental parts of the code architecture
  - If unable to fix an issue with the current approach, report the problem and ask for guidance
  - Focus on minimal changes to address the specific issue at hand
  - Preserve the existing patterns and conventions of the codebase

## Spot-Specific Libraries

- SSH: `golang.org/x/crypto/ssh` and `github.com/pkg/sftp`
- AWS SDK: `github.com/aws/aws-sdk-go-v2/*` (for AWS Secrets Manager)
- Vault: `github.com/hashicorp/vault/api` (for HashiCorp Vault integration)
- Container testing: `github.com/testcontainers/testcontainers-go`
- Encryption: `golang.org/x/crypto/scrypt` and `crypto/aes`

## Code Style Guidelines

### Import Organization
- Organize imports in the following order:
  1. Standard library packages first (e.g., "fmt", "context")
  2. A blank line separator
  3. Third-party packages
  4. A blank line separator
  5. Project imports (e.g., "github.com/umputun/spot/pkg/*")
- Example:
  ```go
  import (
      "context"
      "fmt"
      "net/http"

      "github.com/go-pkgz/lgr"
      "github.com/jessevdk/go-flags"

      "github.com/umputun/spot/pkg/config"
  )
  ```

### Error Handling
- Return errors to the caller rather than using panics
- Use descriptive error messages that help with debugging
- Use error wrapping: `fmt.Errorf("failed to process request: %w", err)`
- Check errors immediately after function calls
- Return early when possible to avoid deep nesting

### Variable Naming
- Use descriptive camelCase names for variables and functions
- Good: `notFoundHandler`, `requestContext`, `userID`
- Bad: `not_found_handler`, `x`, `temp1`
- Be consistent with abbreviations (e.g., `httpClient` not `HTTPClient`)
- Local scope variables can be short (e.g., "lmt" instead of "orderLimit")

### Function Parameters
- Group related parameters together logically
- Use descriptive parameter names that indicate their purpose
- Consider using parameter structs for functions with many (4+) parameters
- If function returns 3 or more results, consider wrapping in Result/Response struct
- If function accepts 3 or more input parameters, consider wrapping in Request/Input struct (but never add context to struct)

### Documentation
- All exported functions, types, and methods must have clear godoc comments
- Begin comments with the name of the element being documented
- Include usage examples for complex functions
- Document any non-obvious behavior or edge cases
- All comments should be lowercase, except for godoc public functions and methods
- IMPORTANT: all comments except godoc comments must be lowercase, test messages must be lowercase, log messages must be lowercase

### Code Structure
- Keep code modular with focused responsibilities
- Limit file sizes to 300-500 lines when possible
- Group related functionality in the same package
- Use interfaces to define behavior and enable mocking for tests
- Keep code minimal and avoid unnecessary complexity
- Don't keep old functions for imaginary compatibility
- Interfaces should be defined on the consumer side (idiomatic Go)
- Aim to pass interfaces but return concrete types when possible
- Consider nested functions when they simplify complex functions

### Code Layout
- Keep cyclomatic complexity under 30
- Function size preferences:
  - Aim for functions around 50-60 lines when possible
  - Don't break down functions too small as it can reduce readability
  - Maintain focus on a single responsibility per function
- Keep lines under 130 characters when possible
- Avoid if-else chains and nested conditionals:
  - Never use long if-else-if chains; use switch statements instead
  - Prefer early returns to reduce nesting depth
  - Extract complex conditions into separate boolean functions or variables
  - Use context structs or functional options instead of multiple boolean flags

### Testing
- Write thorough tests with descriptive names (e.g., `TestRouter_HandlesMiddlewareCorrectly`)
- Prefer subtests or table-based tests, using Testify
- Use table-driven tests for testing multiple cases with the same logic
- Test both success and error scenarios
- Mock external dependencies to ensure unit tests are isolated and fast
- Aim for at least 80% code coverage
- Keep tests compact but readable
- If test has too many subtests, consider splitting it to multiple tests
- Never disable tests without a good reason and approval
- Important: Never update code with special conditions to just pass tests
- Don't create new test files if one already exists matching the source file name
- Add new tests to existing test files following the same naming and structuring conventions
- Don't add comments before subtests, t.Run("description") already communicates what test case is doing
- Never use godoc-style comments for test functions
- For mocking external dependencies:
  - Create a local interface in the package that needs the mock
  - Generate mocks using `moq` with: `//go:generate moq -out mocks/accessor.go -pkg mocks -skip-ensure -fmt goimports . Accessor`
  - The mock should be located in a `mocks` package under the package containing the interface
  - Always use moq-generated mocks, not testify mock

## Git Workflow

### After merging a PR
```bash
# Switch back to the master branch
git checkout master

# Pull latest changes including the merged PR
git pull

# Delete the temporary branch (might need -D for force delete if squash merged)
git branch -D feature-branch-name
```

## Commonly Used Libraries
- Logging: `github.com/go-pkgz/lgr`
- CLI flags: `github.com/jessevdk/go-flags`
- HTTP/REST: `github.com/go-pkgz/rest` with `github.com/go-pkgz/routegroup`
- Database: `github.com/jmoiron/sqlx` with `modernc.org/sqlite`
- Testing: `github.com/stretchr/testify`
- Mock generation: `github.com/matryer/moq`
- Concurrency: `github.com/go-pkgz/pool` and `github.com/go-pkgz/syncs`
- Flow control and assertions: `github.com/go-pkgz/ctrl`
- HTTP client: `github.com/go-pkgz/requester`
- OpenAI: `github.com/sashabaranov/go-openai`
- Advanced enum generation: `github.com/go-pkgz/enum`
- String manipulation: `github.com/go-pkgz/stringutils`
- File operations: `github.com/go-pkgz/fileutils`
- Caching: `github.com/go-pkgz/lcw/v2`
- Email sending: `github.com/go-pkgz/email`
- Notification: `github.com/go-pkgz/notify`
- Frontend: HTMX v2. Try to avoid using JS.
- For containerized tests use `github.com/go-pkgz/testutils`
- To access libraries, figure how to use and check their documentation, use `go doc` command and `gh` tool

## Web Server Setup
- Create server with routegroup: `router := routegroup.New(http.NewServeMux())`
- Apply middleware: `router.Use(rest.Recoverer(), rest.Throttle(), rest.BasicAuth())`
- Define routes with groups: `router.Mount("/api").Route(func(r *routegroup.Bundle) {...})`
- Start server: `srv := &http.Server{Addr: addr, Handler: router}; srv.ListenAndServe()`
- Check documentation with `go doc` and handle middlewares as needed

## Formatting Guidelines
- NEVER do gofmt for vendor directory

## Logging Guidelines
- Never use fmt.Printf for logging, only log.Printf or lgr logger

## Code Analysis Tools
- mpt code analysis: run `mpt --openai.enabled --openai.model=gpt-4.1 -f "**/*.go" -f "*.md" -p "analyze code, check for design and security issues. make sure code is readable, maintainable and idiomatic. provide just a list of improvements"` and synthesize combined input
- mpt diff analysis: run `mpt --git.diff --openai.enabled --google.enabled --anthropic.enabled --timeout=60s -p "Perform a comprehensive code review of these changes. Analyze the design patterns and architecture. Identify any security vulnerabilities or risks. Evaluate code readability, maintainability, and idiomatic usage. Suggest specific improvements where needed."` and synthesize combined input