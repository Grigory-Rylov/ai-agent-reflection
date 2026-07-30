# AI Agent Gateway (VK Bot)

Go-based AI agent gateway connecting VK Bot API with local LLM models (llama-server). The agent supports function calling with 11 tools: file operations, shell execution, web search/fetch, code search, math evaluation, and more.

## Architecture

```
VK Bot API → Agent Gateway → llama-server (local LLM)
                │
         ┌──────┼──────┐
         │      │      │
     Session  Tools   VK Client
```

## Binaries

| Binary | Description |
|--------|-------------|
| `agent` | Main AI agent with tools and LLM integration |
| `agent-restarter` | Watchdog for remote updates via VK commands |

## Quick Start

```bash
# Build both binaries
go build -o agent .
go build -o agent-restarter ./cmd/vk-gateway-restarter

# Configure config.json with VK token
# Run agent directly:
./agent

# Or run via restarter (recommended for remote updates):
./agent-restarter
```

Config is loaded from `./config.json` (priority), with fallback to `~/.config/ai-agent/config.json`.

```json
{
    "llama_server_url": "192.168.1.212:8081",
    "token_vk": "vk1.a.your_vk_bot_token",
    "peer_id": 2000000001,
    "thinking_peer_id": 2000000002,
    "max_tokens": 4096,
    "temperature": 0.7
}
```

## Multi-Agent Mode

The agent system supports multiple AI agent roles with individual prompts and tool permissions.

### Configuration

Register agents in the `"agents"` block of `config.json`:

```json
{
    "agents": {
        "lead": {
            "description": "Lead agent, coordinates the pipeline",
            "mode": "primary",
            "prompt": "agents/lead.md"
        },
        "developer": {
            "description": "Developer, writes and implements code",
            "mode": "subagent",
            "prompt": "agents/developer.md"
        },
        "reviewer": {
            "description": "Code reviewer",
            "mode": "subagent",
            "leaf": true,
            "review": true,
            "prompt": "agents/reviewer.md",
            "permission": {
                "file_write": "deny",
                "edit": "deny",
                "apply_patch": "deny"
            }
        },
        "qa": {
            "description": "QA engineer, writes and runs tests",
            "mode": "subagent",
            "leaf": true,
            "prompt": "agents/qa.md"
        }
    }
}
```

### Agent config fields

| Field | Type | Description |
|-------|------|-------------|
| `description` | string | Human-readable description (shown in available agents list) |
| `mode` | string | `"primary"` (user-facing) or `"subagent"` (invoked via `#name`) |
| `prompt` | string | Path to `.md` file with the agent's system prompt |
| `leaf` | bool | If `true`, agent cannot delegate to sub-agents |
| `review` | bool | If `true`, agent gets read-only tools + approve/reject |
| `hidden` | bool | If `true`, hidden from available agents list |
| `permission` | object | Tool permissions: `"allow"`, `"deny"`, or `"ask"` per tool |

### Permission rules

Each permission entry maps a tool name to an action:

| Tool name | Description |
|-----------|-------------|
| `file_read` | Read files |
| `file_write` | Create/overwrite files |
| `edit` | Search-and-replace edit |
| `apply_patch` | Apply patches |
| `shell_execute` | Run shell commands |
| `glob` | Find files by pattern |
| `search_code` | Grep file contents |
| `web_fetch` | Fetch URLs |
| `web_search` | Search web |
| `*` | Wildcard — applies to all tools |

Example — deny all editing, allow reading and searching:

```json
"permission": {
    "file_write": "deny",
    "edit": "deny",
    "apply_patch": "deny"
}
```

Unspecified tools default to `"allow"`.

### Usage

Send a message with `#agent_name` prefix to route to a specific agent:

```
#developer напиши тесты для модуля
#reviewer проверь код на安全问题
#qa запусти тесты и отчитайся
```

The lead agent (`#lead`) orchestrates the full pipeline: delegate to developer → reviewer → qa.

### Prompt files

Agent prompt files (`.md`) contain **only the system prompt** — no frontmatter. All configuration (description, mode, permissions) lives in `config.json`. Example `agents/developer.md`:

```markdown
You are a Developer. Implement the task using available tools.

## Instructions
1. Implement the task completely.
2. Verify your code: run the build command.
3. Return the complete result.
```

## Bot Commands

| Command | Description |
|---------|-------------|
| `/reset` | Clear conversation history |
| `/newsession [path]` | Reset session and change working dir |
| `/status` | Show session info and working dir |
| `/help` | Show command list |

Commands starting with `/` are handled by the bot and never sent to the model.

## Testing

### Scenario-based integration tests

Orchestrator tests use predefined LLM responses (no llama-server required). Each scenario is a directory under `pkg/agentloop/testdata/scenarios/`:

```
testdata/scenarios/<name>/
├── prompt.txt              # User's task prompt
├── 000_coordinator.txt     # Coordinator response
├── 001_developer.txt       # Developer response
├── 002_reviewer.txt        # Reviewer response (XML approve/revise)
├── 003_reviewer_result.txt # Reviewer follow-up after tool call
└── assert.txt              # Assertions: "contains: ..." / "not_contains: ..."
```

Run all scenarios:
```bash
go test -v -run "TestScenario" ./pkg/agentloop/
```

Run a specific scenario:
```bash
go test -v -run "TestScenario_RevisionCycle" ./pkg/agentloop/
```

To add a new scenario, create a folder with `prompt.txt`, numbered step files, and optional `assert.txt`, then add one line to `run_scenario_test.go`:
```go
func TestScenario_MyCase(t *testing.T) { runScenario(t, "my_case") }
```

## Project Structure

```
.                         # Main AI agent (main.go)
cmd/vk-gateway-restarter/  # Restarter for remote updates
pkg/agent/                   # AI Agent: streaming, function calling
pkg/agentloop/               # Conversation orchestration
pkg/tools/                   # 11 tool implementations
pkg/vk/                      # VK Bot API client + handler
session/                     # Session memory with persistence
system_prompt.txt            # System prompt for the AI model
config.json                  # Configuration
```

## Restarter Commands

These commands are handled by `vk-gateway-restarter` via VK:

| Command | Description |
|---------|-------------|
| `/update` | Git pull, rebuild, restart agent |
| `/b <branch>` | Force checkout branch, pull, rebuild, restart |
| `/restart` | Restart agent without rebuild |
| `/status` | Show agent status and current branch |
| `/help` | Show command list |
