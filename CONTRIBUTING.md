# Contributing to Geryon

Thank you for your interest in contributing to Geryon! This guide covers how to set up the development environment, run tests, and add new features.

## Development Setup

### Prerequisites

- Go 1.25 or later
- Make (for build commands)
- Git

### Clone and Build

```bash
git clone https://github.com/GeryonProxy/geryon.git
cd geryon
make build    # Builds binary to bin/geryon
make test     # Runs all tests
make lint     # Runs go vet and gofmt check
```

### Running Locally

```bash
# Generate example configuration
./bin/geryon --generate-config > geryon.yaml

# Edit with your settings
vim geryon.yaml

# Start the proxy
./bin/geryon --config geryon.yaml
```

## Testing

### Running Tests

```bash
# All tests (without integration tests)
go test -short ./...

# Specific package
go test -race ./internal/pool/

# Single test
go test -race -run TestPoolMode ./internal/pool/

# With race detector (requires CGO)
CGO_ENABLED=1 go test -race ./...

# Benchmarks
go test -bench=. -benchmem -run=^$ ./benchmarks/...
```

### Test Patterns

Geryon uses table-driven tests:

```go
func TestFeature(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
    }{
        {"valid input", "abc", "ABC"},
        {"empty input", "", ""},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // test logic
        })
    }
}
```

### Integration Tests

Integration tests that require running databases are in `integration-tests/`. They are skipped with `-short`:

```bash
# Skip integration tests
go test -short ./...

# Run integration tests (requires PostgreSQL, MySQL, MSSQL running)
go test -v ./integration-tests/ -tags=integration
```

## Adding a New Protocol

To add support for a new database protocol:

1. **Create codec package** in `internal/protocol/<name>/`:
   - `codec.go` — Message framing, parsing, serialization
   - `codec_test.go` — Unit tests
   - Implement the `common.Codec` interface

2. **Add body type** to `internal/config/config.go`:
   - Add to `PoolConfig.Body` validation
   - Add to `validatePoolConfig()`

3. **Wire up in proxy** in `internal/proxy/listener.go`:
   - Add protocol-specific authentication handler
   - Add protocol-specific reset logic in `internal/pool/reset.go`

4. **Add tests**:
   - Unit tests for codec
   - Protocol-specific integration tests

## Adding a REST API Endpoint

1. **Register route** in `internal/api/rest/server.go` in `NewServer()`:
   ```go
   mux.HandleFunc("/api/v1/new-resource", s.handleNewResource)
   ```

2. **Implement handler** following the existing pattern:
   ```go
   func (s *Server) handleNewResource(w http.ResponseWriter, r *http.Request) {
       switch r.Method {
       case http.MethodGet:
           // List/get resource
       case http.MethodPost:
           // Create resource (validate input, use MaxBytesReader)
       case http.MethodDelete:
           // Delete resource (validate pool/resource name)
       default:
           http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
       }
   }
   ```

3. **Add validation**:
   - Use `validatePoolName()` for pool names
   - Use `http.MaxBytesReader` for request body limits
   - Sanitize error messages with `sanitizeErr()`

4. **Update OpenAPI spec** in `docs/openapi.yaml`

## Adding an MCP Tool

1. **Define tool** in `internal/api/mcp/tools.go`:
   ```go
   mcpServer.AddTool(mcp.Tool{
       Name: "geryon_new_tool",
       Description: "Description of what this tool does",
       InputSchema: mcp.ToolInputSchema{
           Type: "object",
           Properties: map[string]interface{}{
               "param1": map[string]string{"type": "string"},
           },
           Required: []string{"param1"},
       },
   }, s.handleNewTool)
   ```

2. **Implement handler** that returns `mcp.CallToolResult`

## Code Style

- **No comments** unless the WHY is non-obvious (hidden constraint, workaround, subtle invariant)
- **No multi-line docstrings** — one short line max if needed
- **Named identifiers** should communicate what the code does
- **Table-driven tests** for all test functions
- **Error handling** — check errors, don't ignore them
- **No emojis** in code or documentation

## Git Workflow

```bash
# Create a feature branch
git checkout -b feature/my-feature

# Make changes, commit
git add internal/pkg/myfile.go
git commit -m "feat(pkg): add my feature

Co-Authored-By: Your Name <your@email.com>"

# Push and create PR
git push -u origin feature/my-feature
```

### Commit Message Convention

- `feat:` — New features
- `fix:` — Bug fixes
- `docs:` — Documentation changes
- `style:` — Code style changes (formatting, whitespace)
- `refactor:` — Code refactoring (no behavior change)
- `test:` — Test additions or changes
- `ci:` — CI/CD changes

Format: `type(scope): short description`

## Pull Request Guidelines

- Keep PRs focused on a single change
- Include tests for new functionality
- Update documentation if behavior changes
- Reference related issues with `#issue-number`
- Follow existing code patterns in the codebase
