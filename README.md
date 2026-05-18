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

## Quick Start

```bash
go build -o vk-gateway ./cmd/vk-gateway
# configure config.json with VK token and llama-server URL
./vk-gateway
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
cmd/vk-gateway/        # Entry point
pkg/agent/             # AI Agent: streaming, function calling
pkg/agentloop/         # Conversation orchestration
pkg/tools/             # 11 tool implementations
pkg/vk/                # VK Bot API client + handler
session/               # Session memory with persistence
system_prompt.txt      # System prompt for the AI model
config.json            # Configuration
```
