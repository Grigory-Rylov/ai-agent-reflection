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
| `vk-gateway` | Main AI agent with tools and LLM integration |
| `vk-gateway-restarter` | Watchdog for remote updates via VK commands |

## Quick Start

```bash
# Build both binaries
go build -o vk-gateway ./cmd/vk-gateway
go build -o vk-gateway-restarter ./cmd/vk-gateway-restarter

# Configure config.json with VK token
# Run agent directly:
./vk-gateway

# Or run via restarter (recommended for remote updates):
./vk-gateway-restarter
```

## Configuration

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

## Bot Commands

| Command | Description |
|---------|-------------|
| `/reset` | Clear conversation history |
| `/newsession [path]` | Reset session and change working dir |
| `/status` | Show session info and working dir |
| `/help` | Show command list |

Commands starting with `/` are handled by the bot and never sent to the model.

## Project Structure

```
cmd/vk-gateway/              # Main AI agent entry point
cmd/vk-gateway-restarter/    # Watchdog/restarter for remote updates
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
