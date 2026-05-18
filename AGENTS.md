# AGENTS.md — Instructions for AI coding agents

## Code Style

### Function size limit
- Maximum: **50 lines** per function
- Exceeding 50 lines → **split into smaller functions**
- Each function should have **one clear responsibility**

### No Duplication (DRY)
- Extract common logic into reusable functions
- Create helper packages for shared utilities
- Use interfaces for similar behaviors

### Single Responsibility
- One function = One job
- If a function does two things → split it

## Testing (TDD)

### Process
1. Write test FIRST (describes expected behavior)
2. Run test — it MUST fail (red phase)
3. Write minimal code to pass test (green phase)
4. Run test — it must pass
5. Refactor without breaking tests

### Test organization
- Tests co-located with implementation: `pkg/module/module_test.go`
- Test function: `TestFunctionName(t *testing.T)`
- Table-driven tests for multiple cases
- Subtests: `t.Run("scenario", func(t *testing.T) {...})`

## Project conventions

### Naming
- Go: camelCase for vars, PascalCase for exports
- Tests: descriptive names, single assertion per scenario

### Error handling
- Return errors, don't panic
- Wrap errors with context: `fmt.Errorf("context: %w", err)`
- Handle errors at the appropriate level

### Imports
- Standard library first, then third-party, then internal
- Grouped with blank lines between groups

### Logging
- `[TOOL] Call:` / `[TOOL] Result:` for tool calls (brief, no full content)
- `fmt.Printf` for console, thinking callback for VK thinking_peer_id

## Tools system

### Adding a new tool
1. Create tool struct implementing `tools.Tool` interface
2. Implement: `Name()`, `Description()`, `Schema()`, `Execute(ctx, inputs)`
3. Register in `cmd/vk-gateway/main.go`
4. Add tests in `pkg/tools/impl_tools_test.go`
5. Add to `system_prompt.txt`

### Tool interface
```go
type Tool interface {
    Name() string
    Description() string
    Schema() map[string]interface{}
    Execute(ctx context.Context, inputs map[string]string) (ToolResult, error)
}
```
