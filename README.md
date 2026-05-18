# AI Agent with VK Bot API Gateway

Go-based AI agent gateway connecting VK Bot API with local LLM models (llama-server). Built with **TDD methodology** and strict code quality standards.

---

## Architecture

```
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────┐
│   VK Bot API     │────▶│  Agent Gateway   │────▶│  llama-server   │
│  (User Interface)│     │  (Go Application)│     │  (Local LLM)    │
└─────────────────┘     └──────────────────┘     └─────────────────┘
                               │                         │
                     ┌─────────┼─────────┐              │
                     ▼         ▼         ▼              ▼
              ┌──────────┐ ┌────────┐ ┌──────────┐  ┌────────────────┐
              │  Session │ │  VK    │ │  Tools   │  │     GGUF       │
              │   Memory │ │ Client │ │ Registry │  │     Model      │
              └──────────┘ └────────┘ └──────────┘  └────────────────┘
                     │                    │
                     ▼                    ▼
              ┌─────────────────────────────────────┐
              │           AgentLoop                  │
              │  (ProcessPrompt → sendToLLM → tools) │
              └─────────────────────────────────────┘
```

## Project Structure

```
├── cmd/vk-gateway/main.go     # Entry point
├── pkg/
│   ├── agent/                 # AI Agent: streaming, function calling, tool execution
│   ├── agentloop/             # Conversation orchestration, session management
│   ├── compress/              # Context compression (disabled by default)
│   ├── logger/                # Logging utilities
│   ├── tokenizers/            # Token counting via llama-server API
│   ├── tools/                 # Tool interface, registry, implementations
│   ├── vk/                    # VK Bot API client + message handler
│   └── access/                # Access control (file operations)
├── session/                   # Session memory with persistence
├── parser/                    # SSE stream parser
├── config.json                # Configuration
├── system_prompt.txt          # System prompt for the AI model
└── test_data/                 # Test SSE samples
```

## Configuration

```json
{
    "llama_server_url": "192.168.1.212:8081",
    "token_vk": "vk1.a.your_vk_bot_token",
    "peer_id": 2000000001,
    "thinking_peer_id": 2000000002,
    "model": "",
    "max_tokens": 4096,
    "temperature": 0.7
}
```

| Field | Description |
|-------|-------------|
| `llama_server_url` | llama-server address (host:port) |
| `token_vk` | VK Bot API token |
| `peer_id` | Main chat for responses |
| `thinking_peer_id` | Chat for thinking/tool logs |
| `model` | Model name (empty = server default) |
| `max_tokens` | Max output tokens |
| `temperature` | LLM temperature (0.0-1.0) |

## Bot Commands

| Command | Description |
|---------|-------------|
| `/reset` or `/clear` | Clear conversation history |
| `/newsession [path]` | Reset session and change working directory |
| `/status` | Show session status and working directory |
| `/help` | Show command list |

Commands starting with `/` are handled by the bot and never sent to the AI model.

## Available Tools

| Tool | Description | Parameters |
|------|-------------|------------|
| `file_read` | Read file contents | `path` (required) |
| `file_write` | Write content to file | `path`, `content` (required) |
| `file_list` | List directory contents | `path` (optional) |
| `shell_execute` | Execute shell command | `command` (required), `timeout` (optional) |
| `web_fetch` | Fetch URL content | `url` (required), `method` (optional) |
| `web_search` | Search the web | `query` (required) |
| `glob` | Find files by pattern | `pattern` (required), `path` (optional) |
| `search_code` | Search text in files | `pattern` (required), `path`/`include` (optional) |
| `time_get` | Get current date/time | None |
| `calc` | Evaluate math expression | `expression` (required) |
| `edit` | Search and replace in file | `path`, `old_string`, `new_string` (required) |

All tools log their calls and results to console and `thinking_peer_id` as `[TOOL] Call:` / `[TOOL] Result:`.

## Working Directory

Each session has its own working directory, used for relative file paths and shown in the system prompt. Change it with `/newsession [path]`.

## Quick Start

```bash
# Run tests
go test ./...

# Build
go build -o vk-gateway ./cmd/vk-gateway

# Configure
cp config.example.json config.json
# Edit config.json with your VK token and llama-server URL

# Run
./vk-gateway

# Debug mode
./vk-gateway -d
```

## Development

- **TDD:** Write tests first, see them fail, implement, refactor
- **Function limit:** Max 50 lines per function
- **Integration tests:** Run against real llama-server: `go test ./pkg/agent/... -run TestLLMToolCall`
