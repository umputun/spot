# Spot Development Guide

## Build, Lint and Test Commands
```bash
# Build all binaries
go build -ldflags "-X main.revision=$(GIT_REV) -s -w" -o .bin/spot ./cmd/spot
go build -ldflags "-X main.revision=$(GIT_REV) -s -w" -o .bin/spot-secrets ./cmd/secrets

# Run all tests with race detection and coverage
go test -race -coverprofile=coverage.out ./...

# Run a specific test
go test ./pkg/executor -run TestExecuter_Run

# Run tests with coverage
go test -cover ./...

# Lint code
golangci-lint run

# Format code
gofmt -s -w .

# Run code generation
go generate ./...

# Coverage report
go test -race -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

# Normalize code comments
command -v unfuck-ai-comments >/dev/null || go install github.com/umputun/unfuck-ai-comments@latest; unfuck-ai-comments run --fmt --skip=mocks ./...
```

**Note:** Always run tests and lint before submitting changes.

## Important Workflow Notes
- Always run tests, linter and normalize comments BEFORE committing anything
- Run formatting, code generation, linting and testing on completion
- Never commit without running completion sequence
- Run tests and linter after making significant changes to verify functionality
- Don't add "Generated with Claude Code" or "Co-Authored-By: Claude" to commit messages or PRs
- Do not include "Test plan" sections in PR descriptions
- Do not add comments that describe changes, progress, or historical modifications
- Avoid comments like "new function," "added test," "now we changed this," or "previously used X, now using Y"
- Comments should only describe the current state and purpose of the code, not its history or evolution
- Use `go:generate` for generating mocks, never modify generated files manually
- Mocks are generated with `moq` and stored in the `mocks` package
- After important functionality added, update README.md accordingly
- When merging master changes to an active branch, make sure both branches are pulled and up to date first

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

### Working with PRs
```bash
# View PR details
gh pr view <PR_NUMBER>

# Get PR review comments
gh api repos/umputun/spot/pulls/<PR_NUMBER>/comments --paginate | jq -r '.[] | {id: .id, path: .path, line: .line, body: .body, in_reply_to_id: .in_reply_to_id}'

# Check reviews
gh api repos/umputun/spot/pulls/<PR_NUMBER>/reviews --paginate | jq -r '.[] | select(.user.login == "umputun") | { id: .id, body: .body}'

# Checkout a PR branch to test locally
gh pr checkout <PR_NUMBER>
```

## Commonly Used Libraries
- Logging: `github.com/go-pkgz/lgr`
- CLI flags: `github.com/jessevdk/go-flags`
- HTTP/REST: `github.com/go-pkgz/rest` with `github.com/go-pkgz/routegroup`
- Database: `github.com/jmoiron/sqlx` with `modernc.org/sqlite`
- Testing: `github.com/stretchr/testify`
- Mock generation: `github.com/matryer/moq`
- OpenAI: `github.com/sashabaranov/go-openai`
- Frontend: HTMX v2. Try to avoid using JS.
- For containerized tests use `github.com/go-pkgz/testutils`
- To access libraries, figure how to use ang check their documentation, use `go doc` command and `gh` tool

## Code Style Guidelines

### Imports
- Standard library imports first
- Third-party imports second (alphabetically ordered)
- Project-specific imports last

### Error Handling
- Use `fmt.Errorf("context: %w", err)` to wrap errors with context
- Check errors immediately after function calls
- Return detailed error information through wrapping
- Validate function parameters at the start before processing
- Return early when possible to avoid deep nesting

### Naming Conventions
- **CamelCase** for exported items
- **mixedCase** for unexported items
- Short names for local variables
- Descriptive names for functions and methods
- Use snake_case for filenames, camelCase for variables, PascalCase for exported names

### Comments
- All comments inside functions should be lowercase
- Document all exported items with proper casing
- Use inline comments for complex logic
- Start comments with the name of the thing being described

### Code Layout
- Keep cyclomatic complexity under 30
- Function size preferences:
  - Aim for functions around 50-60 lines when possible
  - Don't break down functions too small as it can reduce readability
  - Maintain focus on a single responsibility per function
- Keep lines under 130 characters when possible

### Testing
- Use table-driven tests with `t.Run()`
- Use `require` for fatal assertions, `assert` for non-fatal ones
- Use mock interfaces for dependency injection
- Test names follow pattern: `Test<Type>_<method>`
- Prefer subtests or table-based tests, using Testify
- Don't create too large tests if they complicated, but split it to multiple tests
- Keep tests compact but readable
- If test has too many subtests, consider splitting it to just multiple tests
- Never disable tests without a good reason and approval
- Never update code with special conditions to just pass tests