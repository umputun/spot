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

# Lint code
golangci-lint run
```

**Note:** Always run tests and lint before submitting changes.

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

## Code Style Guidelines

### Imports
- Standard library imports first
- Third-party imports second (alphabetically ordered)
- Project-specific imports last

### Error Handling
- Use `fmt.Errorf("context: %w", err)` to wrap errors with context
- Check errors immediately after function calls
- Return detailed error information through wrapping

### Naming Conventions
- **CamelCase** for exported items
- **mixedCase** for unexported items
- Short names for local variables
- Descriptive names for functions and methods

### Comments
- All comments inside functions should be lowercase
- Document all exported items with proper casing
- Use inline comments for complex logic
- Start comments with the name of the thing being described

### Testing
- Use table-driven tests with `t.Run()`
- Use `require` for fatal assertions, `assert` for non-fatal ones
- Use mock interfaces for dependency injection
- Test names follow pattern: `Test<Type>_<method>`