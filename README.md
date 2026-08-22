# AI Agent Gateway (VK Bot)

Go-based AI agent gateway connecting VK Bot API with local LLM (`llama-server`). A self-hosted coding assistant accessible through VK Messenger, with file/shell/web tools and multi-agent pipelines.

## Quick Start

```bash
git clone <repo> && cd ai-agent-reflection

# Build both binaries (agent + agent-restarter)
./build.sh

# Configure config.json and models.json, then run:
./agent                  # direct
./agent-restarter        # with remote update/restart support
```

## Configuration

Two JSON files in the project root. Fallback path for `config.json`: `~/.config/ai-agent/config.json`.

**config.json** — VK token, peers, agents, permissions:

```json
{
  "token_vk": "vk1.a.your_bot_token",
  "peer_id": 2000000001,
  "thinking_peer_id": 2000000002,
  "temperature": 0.7,
  "stream_idle_timeout_sec": 300,
  "db_path": "./agent.db",
  "mcp_config_path": "./mcp_config.json",
  "allowed_dirs": ["/your/working/dir"],
  "agents": {
    "lead":   { "mode": "primary", "prompt": "agents/lead.md" },
    "worker": { "mode": "subagent", "leaf": false, "prompt": "agents/worker.md" },
    "reviewer": { "mode": "subagent", "leaf": true, "review": true, "prompt": "agents/reviewer.md" },
    "qa":     { "mode": "subagent", "leaf": true, "prompt": "agents/qa.md" }
  }
}
```

**models.json** — LLM models and their `llama-server` endpoints:

```json
{
  "default": "gemma-4",
  "models": {
    "gemma-4": {
      "name": "gemma-4-12b-it-Q4_K_XL.gguf",
      "host": "127.0.0.1:8081",
      "context": 32768,
      "vision": true,
      "slot-save": true
    }
  }
}
```

Per model entry:

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `name` | yes | — | Model name on llama-server (e.g. `.gguf` filename) |
| `host` | yes | — | Server address, e.g. `127.0.0.1:8081` |
| `context` | no | from server | Context limit in tokens; 0 = fetch actual limit from llama-server |
| `vision` | no | `false` | Model supports images (multimodal) |
| `slot-save` | no | `false` | Persist KV-cache per session for faster follow-up turns. Requires `--slot-save-path <dir>` on the server. |

### Stream idle watchdog

If the LLM stream sends no SSE data for a while (stalled engine, dead connection), the agent aborts the stream and retries the request automatically.

Configured in **config.json** via `stream_idle_timeout_sec`:

| Value | Meaning |
|-------|---------|
| `0` / absent | default — 300 seconds (5 minutes; long prefills on big contexts are normal silence) |
| `> 0` | custom timeout in seconds |
| `< 0` | watchdog disabled — wait forever (only the global 2h HTTP timeout applies) |

An aborted-by-watchdog stream is treated as a retryable error and goes through the standard retry loop (`retry_delay`, default 5s).

## Features

- **14+ tools**: file read/write/edit/patch, shell execute, glob, code grep, web search/fetch, math calc, image OCR/vision, question dialogs, time.
- **Multi-agent pipeline**: lead → worker → reviewer → qa with autonomous delegation and approval cycles.
- **MCP integration**: external tool servers via JSON config (stdio/SSE transport, auto-discovery of tools).
- **Slot KV-cache persistence** — fast context reuse between conversation turns via llama-server `/slots` API.
- **Permission system**: per-agent allow/deny/ask rules for tools and shell commands, with runtime "Always allow" learning.
- **Session memory** with automatic token-limit aware pruning
- **Stream idle watchdog** — detects stalled LLM streams (no SSE data) and retries automatically; timeout configurable via `stream_idle_timeout_sec`

## Bot Commands

| Command | Description |
|---------|-------------|
| `/clear` | Clear conversation history (working dir preserved) |
| `/newsession [path]` | Reset session, optionally change working dir |
| `/status` | Show session info and working dir |
| `/help` | Show command list |
| `/restart` | Restart agent without rebuild (via restarter) |
| `/update` | `git pull`, rebuild, restart (via restarter) |
| `/test-llama` | Test llama-server connection |
| `/pin <prompt>` | Pin a system prompt prefix for the session |
| `/sessions` | List active sessions |

Commands starting with `/` are handled by the bot and never sent to the model.

## Multi-Agent Pipeline

Prefix a message with `#agent_name` to route it directly:

```
#worker rewrite the parser module
#reviewer check the changes
#qa write tests for pkg/tools/
```

The lead agent orchestrates the full pipeline automatically: delegate to worker → reviewer approves/rejects → qa validates. Each agent has its own system prompt and tool permissions defined in `config.json`.

## Restarter Commands

Remote management via VK when running under `agent-restarter`:

| Command | Description |
|---------|-------------|
| `/update` | Git pull, rebuild (`./build.sh`), restart |
| `/b <branch>` | Checkout branch, pull, rebuild, restart |
| `/restart` | Restart agent without rebuilding |

## Project Structure

```
cmd/vk-gateway-restarter/   # Restarter binary
pkg/agent/                   # LLM streaming + function calling
pkg/agentloop/               # Conversation orchestration
pkg/tools/                   # Tool implementations
pkg/vk/                      # VK Bot API client
session/                     # Session memory (SQLite)
system_prompt.txt            # Main system prompt
agents/*.md                  # Per-agent system prompts
build.sh                     # Build script (use this, not go build directly)
debug.log                    # Runtime log file
```
