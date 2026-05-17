# AI Agent with VK Bot API Gateway

Go-based AI agent gateway connecting VK Bot API with local LLM models (llama-server). Built with **TDD methodology** and strict code quality standards.

---

## 📋 Table of Contents

- [Technical Specification](#technical-specification)
- [Architecture](#architecture)
- [Code Quality Rules](#code-quality-rules)
- [Methodology](#methodology)
- [Features](#features)
  - [Token Counting & Context Management](#1-token-counting--context-management)
  - [Context Compression](#2-context-compression)
  - [Session Memory](#3-session-memory)
  - [Tools System](#4-tools-system)
  - [Access Control](#5-access-control)
  - [SSE Streaming Parser](#6-sse-streaming-parser)
  - [Agent Loop](#7-agent-loop-planned)
- [Getting Started](#getting-started)
- [Development](#development)

---

## 📖 Technical Specification

### Project Overview

**Type:** AI Agent Gateway  
**Language:** Go 1.22+  
**Architecture:** Event-driven, modular  
**Testing:** TDD (Test-Driven Development)  
**Target Platform:** Linux ARM64 (Raspberry Pi / Orange Pi)  

### Core Requirements

1. **VK Bot API Integration** — Receive and respond to messages via VK Bot API
2. **LLM Integration** — Connect to llama-server via SSE streaming
3. **Session Management** — Persistent conversation history
4. **Tool System** — Extensible function calling mechanism
5. **Access Control** — File operation security with user consent

---

## 🏗 Architecture

```
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────┐
│   VK Bot API     │────▶│  Agent Gateway   │────▶│  llama-server   │
│  (User Interface)│     │  (Go Application)│     │  (Local LLM)    │
└─────────────────┘     └──────────────────┘     └─────────────────┘
                               │                         │
                    ┌──────────┼──────────┐              │
                    ▼          ▼          ▼              ▼
             ┌────────────┐ ┌────────┐ ┌──────────┐  ┌────────────────┐
             │  Session   │ │Context │ │  Tools   │  │     GGUF       │
             │   Memory   │ │Manager │ │ Registry │  │     Model      │
             └────────────┘ └────────┘ └──────────┘  └────────────────┘
                    │          │          │
                    ▼          ▼          ▼
             ┌────────────────────────────────────────────────┐
             │                Access Control                  │
             └────────────────────────────────────────────────┘
```

**Module Flow:**
```
User Prompt → AgentLoop → ContextManager → Session
                    ↓              ↓
                Tokenizer      Compressor
                    ↓              ↓
                llama-server   (auto-compress)
```

---

## ⚠️ Code Quality Rules

### CRITICAL: No Large, Hard-to-Read Functions

**Rule 1: Function Size Limit**
- Maximum function length: **50 lines**
- If function exceeds limit → **split into smaller functions**
- Each function should have **one clear responsibility**

**Rule 2: Avoid Duplication (DRY)**
- Extract common logic into reusable functions
- Create helper packages for shared utilities
- Use interfaces for similar behaviors

**Rule 3: Single Responsibility**
- One function = One job
- If function does two things → **split it**

### Example: Bad vs Good

**❌ BAD — Large function:**
```go
func handleMessage(msg string) {
    // 200 lines of code doing everything
    // - parsing
    // - validation
    // - database access
    // - formatting
    // - error handling
    // - logging
}
```

**✅ GOOD — Split functions:**
```go
func handleMessage(msg string) {
    validated := validateMessage(msg)
    if !validated { return }
    
    context := loadSessionContext(msg.UserID)
    response := generateResponse(context, msg.Content)
    
    saveSessionContext(context)
    sendResponse(msg.UserID, response)
}
```

---

## 🧪 Methodology: Test-Driven Development (TDD)

### The Three Phases

```
┌─────────────┐    ┌──────────────┐    ┌─────────────┐
│ Write Test  │───▶│  See Fail    │───▶│ Write Code  │
│ (Red)       │    │  (Green)     │    │ (Green)     │
└─────────────┘    └──────────────┘    └─────────────┘
                                                    │
                                                    ▼
                                            ┌─────────────┐
                                            │  Refactor   │
                                            └─────────────┘
```

### TDD Rules

1. **Write failing test FIRST** — describe expected behavior
2. **Run test — it must fail** — confirm test is valid
3. **Write minimal code** — make test pass (no more)
4. **Run test — it must pass** — verify implementation
5. **Refactor** — clean up without breaking tests
6. **Repeat** — incrementally build functionality

### Test Organization

```
pkg/
├── module/
│   ├── module.go        # Implementation
│   └── module_test.go   # Tests (co-located!)
```

**Naming convention:**
- Test function: `TestFunctionName(t *testing.T)`
- Subtest: `t.Run("scenario description", func(t *testing.T) {...})`
- Table tests for multiple cases

---

## ✨ Features

### 1. Token Counting & Context Management

Accurate token counting using llama-server API for proper context budget management.

**Module:** `pkg/tokenizers/`

**Capabilities:**
- Count tokens via llama-server API (`usage.prompt_tokens`)
- Estimate context size before sending requests
- Track token budget across conversation turns
- Automatic context compression when approaching limits

**Usage:**
```go
// Create tokenizer connected to llama-server
tokenizer := tokenizers.NewLlamaServerTokenizer("192.168.1.212:8081", "qwen3.6", 8192)

// Count tokens in text
count, _ := tokenizer.CountTokens("Your text here")

// Create context counter
counter := tokenizers.NewContextCounter(tokenizer, 8192)
counter.SetSystemMessage("You are a helpful assistant")

// Get full context statistics
stats, _ := counter.CountFullContext(messages)
fmt.Println(tokenizers.FormatStats(stats))
// "Контекст: 1200/8192 токенов (система: 50)"
```

**Architecture:**
```
pkg/tokenizers/
├── tokenizer.go        — Tokenizer interface + ContextSize + Message
├── llama_server.go     — LlamaServerTokenizer (main path via API)
├── context_counter.go  — ContextCounter for context tracking
├── tokenizer_test.go   — Interface tests (TDD)
├── server_test.go      — LlamaServerTokenizer tests
└── context_counter_test.go — ContextCounter tests
```

---

### 2. Context Compression

Automatic context compression via LLM summarization when conversation approaches token limits.

**Module:** `pkg/compress/`

**Strategies:**
| Strategy | Description | Use Case |
|----------|-------------|----------|
| `Summarize` | Summarize conversation via LLM | Long conversations |
| `Truncate` | Keep only recent messages | Quick recovery |
| `Hybrid` | Summarize + truncate oldest | Balanced approach |

**Usage:**
```go
// Create compressor connected to llama-server
compressor := compress.NewLLMCompressor("192.168.1.212:8081", "qwen3.6", 0.7)

// Create context manager
manager := compress.NewAgentContextManager(compress.AgentConfig{
    ServerURL:   "192.168.1.212:8081",
    Model:       "qwen3.6",
    MaxTokens:   8192,
    Temperature: 0.7,
    Strategy:    compress.SummarizeStrategy,
})

// Check if compression needed (automatic)
err := manager.CheckAndCompress(ctx, peerID, messages, 8192)

// Get compression report
if err == nil {
    fmt.Println("Context compressed successfully")
}
```

**Architecture:**
```
pkg/compress/
├── compressor.go       — Compressor interface + strategies + triggers
├── llm_compressor.go   — LLMCompressor (compression via model)
├── context_manager.go  — ContextManager (automatic compression)
└── compress_test.go    — Tests (12 tests)
```

**How it works:**
```
1. User sends message
       ↓
2. Session adds message to history
       ↓
3. ContextManager counts tokens (via LlamaServerTokenizer)
       ↓
4. If tokens > threshold OR tokens/maxTokens > percentage
       ↓
5. Send context to LLM with compression request
       ↓
6. LLM summarizes/condenses the conversation
       ↓
7. Replace old context with compressed version
       ↓
8. Continue normal conversation flow
```

---

### 3. Session Memory

Persistent conversation history stored in a file.

**Location:** `~/.opencode-vk-gateway/session.json`

**Data Model:**
```json
{
  "user_id": "vk_user_123",
  "created_at": "2025-05-17T10:00:00Z",
  "updated_at": "2025-05-17T10:05:00Z",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "Hello"},
    {"role": "assistant", "content": "Hi! How can I help?"},
    {"role": "user", "content": "What time is it?"}
  ],
  "metadata": {
    "total_turns": 3,
    "session_token_budget": 8192
  }
}
```

**Commands:**
| Command | Action |
|---------|--------|
| `/reset` | Clear session history |
| `/history` | Show conversation summary |
| `/memory` | Show memory usage stats |

---

### 4. Tools System

Tools allow the AI agent to perform actions and retrieve information via function calling.

#### Available Tools

| Tool | Description | Parameters | Returns |
|------|-------------|------------|---------|
| `file_read` | Read file contents | `path` (string) | File content or error |
| `file_write` | Write content to file | `path`, `content` | Success message |
| `file_list` | List directory contents | `path` (optional, `.`) | Directory listing |
| `shell_execute` | Execute shell command | `command` (string) | Output + exit code |
| `web_fetch` | Fetch URL content | `url`, `method` (optional) | HTTP response |
| `time_get` | Get current time | None | ISO 8601 timestamp |
| `calc` | Evaluate math expression | `expression` (string) | Result |
| `search_code` | Search codebase | `query`, `path` (optional) | Matching lines |

#### Tool Interface

```go
// Tool defines the interface all tools must implement
type Tool interface {
    // Name returns the tool identifier
    Name() string
    
    // Description provides tool documentation for LLM
    Description() string
    
    // Schema returns JSON schema for parameters
    Schema() map[string]interface{}
    
    // Execute runs the tool with given inputs
    Execute(ctx context.Context, inputs map[string]string) (ToolResult, error)
}

// ToolResult represents tool execution output
type ToolResult struct {
    Success bool
    Data    interface{}
    Error   string
}
```

#### Tool Registration

```go
// Register all available tools
tools := NewRegistry()
tools.Register(&FileReadTool{})
tools.Register(&FileWriteTool{})
// ...
```

---

### 5. Access Control System

Security layer preventing unauthorized file operations.

#### Access Control Rules

**Rule 1: Allowed Directories**
- Current working directory (project root)
- User's home directory (`$HOME`)
- Explicitly configured directories in `config.json`

**Rule 2: Path Canonicalization**
- All paths must be resolved to absolute form
- Symlinks must be followed and resolved
- `..` traversal is blocked if it exits allowed directories

**Rule 3: Write Protection**
- Before writing: verify target path is within allowed directories
- If path is outside → **request user permission via VK bot**
- If permission denied → operation fails with explanation

#### Access Check Flow

```
User Action → AI Agent → Access Control Check
                                │
                        Is path allowed?
                                │
                        ┌───────┴───────┐
                        │               │
                      YES              NO
                        │               │
                        ▼               ▼
                  Allow Operation   Request Permission
                                    │
                                User Response
                                ┌───────┴───────┐
                                │               │
                             Allowed         Denied
                                │               │
                                ▼               ▼
                          Allow Operation   Return Error
```

#### Permission Request Format

```
⚠️ Access Request

Attempted action: Write file
Target path: /home/user/external/data.txt
Reason: Path is outside allowed directories

Type "allow" to permit or "deny" to block.
Timeout: 60 seconds
```

---

### 6. SSE Streaming Parser

Parse real-time events from llama-server with proper error handling.

**Features:**
- Line-by-line SSE parsing
- Automatic retry on connection loss
- Timeout handling
- Token statistics tracking

---

### 7. Agent Loop (Planned)

Main conversation orchestration module handling:
- User prompt reception and processing
- AI response streaming and collection
- Tool call execution and result forwarding
- Loop detection (AI response repetition)
- Thinking message delivery to `thinking_peer_id`
- Step-by-step logging

**Planned module:** `pkg/agentloop/`

See [TASK.md](TASK.md) for detailed specifications.

---

## 🚀 Getting Started

### Prerequisites

- Go 1.22+
- llama-server running locally
- VK Bot token
- Linux ARM64 system (recommended)

### Quick Start

```bash
# Clone repository
git clone https://github.com/your-org/opencode-vk-gateway.git
cd opencode-vk-gateway

# Run tests first (TDD!)
go test ./...

# Build
go build -o vk-gateway ./cmd/vk-gateway

# Configure
cp config.example.json config.json
# Edit config.json with your VK token and llama-server URL

# Run
./vk-gateway
```

### Configuration

```json
{
  "vk_token": "your_vk_bot_token",
  "llama_server_url": "127.0.0.1:8081",
  "model": "Qwen3.6-35B-A3B",
  "session_file": "~/.opencode-vk-gateway/session.json",
  "max_tokens": 8192,
  "allowed_directories": [
    ".",
    "$HOME/projects"
  ],
  "tools": {
    "enabled": ["file_read", "file_write", "shell_execute", "web_fetch"],
    "security_level": "strict"
  },
  "context": {
    "compression": {
      "enabled": true,
      "strategy": "summarize",
      "threshold": 6000,
      "percentage": 0.75
    },
    "thinking_peer_id": 0
  }
}
```

---

## 🧑‍💻 Development

### Running Tests

```bash
# All tests
go test ./...

# With coverage
go test ./... -cover

# Specific package
go test ./pkg/context/...

# Single test
go test ./pkg/parser/... -run TestParseRealSSEStream

# Watch mode (requires gotestfmt)
gotestsum --watch
```

### Adding New Tools (TDD Workflow)

**Step 1: Write test first** (`pkg/tools/my_tool_test.go`):
```go
func TestMyTool(t *testing.T) {
    t.Run("executes successfully", func(t *testing.T) {
        tool := NewMyTool()
        result, err := tool.Execute(context.Background(), map[string]string{"input": "test"})
        
        assert.NoError(t, err)
        assert.True(t, result.Success)
    })
}
```

**Step 2: Run test — it must FAIL:**
```bash
go test ./pkg/tools/... -run TestMyTool
# FAIL: tool not implemented yet
```

**Step 3: Write minimal implementation** (`pkg/tools/my_tool.go`):
```go
type MyTool struct{}

func NewMyTool() Tool {
    return &MyTool{}
}

func (t *MyTool) Name() string { return "my_tool" }

func (t *MyTool) Description() string { return "My tool description" }

func (t *MyTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
    // Minimal implementation
    return ToolResult{Success: true, Data: "ok"}, nil
}
```

**Step 4: Run test — it must PASS:**
```bash
go test ./pkg/tools/... -run TestMyTool
# PASS
```

**Step 5: Refactor** — clean up without breaking tests

### TDD Checklist

- [ ] Test written BEFORE implementation
- [ ] Test fails initially (red phase)
- [ ] Minimal code added to pass test (green phase)
- [ ] All tests pass
- [ ] Code reviewed for duplication
- [ ] Functions split if too long (>50 lines)
- [ ] Refactored for clarity

---

## 📁 Project Structure

```
opencode-vk-gateway/
├── cmd/
│   └── vk-gateway/
│       └── main.go              # Entry point
├── pkg/
│   ├── agent/
│   │   ├── agent.go             # Core agent logic
│   │   └── agent_test.go        # Agent tests (TDD!)
│   ├── agentloop/               # [PLANNED] Main conversation loop
│   │   ├── agentloop.go         # AgentLoop implementation
│   │   └── agentloop_test.go    # AgentLoop tests
│   ├── context/
│   │   ├── session.go           # Session memory manager
│   │   └── session_test.go      # Session tests
│   ├── parser/
│   │   ├── sse_parser.go        # SSE stream parser
│   │   └── sse_parser_test.go   # Parser tests
│   ├── tools/
│   │   ├── registry.go          # Tool registry
│   │   ├── base_tool.go         # Tool interface
│   │   ├── file_tools.go        # File operations
│   │   ├── shell_tools.go       # Shell execution
│   │   └── *_test.go            # Tool tests
│   ├── access/
│   │   ├── access_control.go    # Access control rules
│   │   └── access_control_test.go
│   ├── vk/
│   │   ├── bot.go               # VK Bot API client
│   │   └── bot_test.go          # Bot tests
│   ├── tokenizers/              # Token counting & context tracking
│   │   ├── tokenizer.go         # Tokenizer interface
│   │   ├── llama_server.go      # LlamaServerTokenizer
│   │   ├── context_counter.go   # ContextCounter
│   │   ├── tokenizer_test.go    # Interface tests
│   │   ├── server_test.go       # LlamaServer tests
│   │   └── context_counter_test.go
│   └── compress/                # Context compression
│       ├── compressor.go        # Compressor interface + strategies
│       ├── llm_compressor.go    # LLMCompressor
│       ├── context_manager.go   # ContextManager
│       └── compress_test.go     # Tests
├── test_data/
│   └── llama_sse_stream.txt     # Real SSE stream samples
├── config.json
├── go.mod
└── README.md
```

---

## 🔒 Security Considerations

1. **Path Traversal:** All file paths are canonicalized before access
2. **Symlink Attack:** Symlinks outside allowed directories are blocked
3. **Command Injection:** Shell commands are sandboxed and parameterized
4. **Network Access:** Only whitelisted URLs can be fetched
5. **Rate Limiting:** Request throttling prevents abuse

---

## 📊 Test Coverage Goals

| Package | Target Coverage | Status |
|---------|-----------------|--------|
| `pkg/parser` | 95%+ | ✅ |
| `pkg/context` | 95%+ | ✅ |
| `pkg/access` | 100% | ✅ |
| `pkg/tools` | 90%+ | ✅ |
| `pkg/agent` | 85%+ | ✅ |
| `pkg/tokenizers` | 90%+ | ✅ NEW |
| `pkg/compress` | 90%+ | ✅ NEW |
| `pkg/agentloop` | 85%+ | 📋 Planned |
| **Overall** | **90%+** | |

---

## 🤝 Contributing

### Guidelines

1. **Follow TDD methodology** — tests before implementation
2. **Keep functions small** — max 50 lines, split if needed
3. **Avoid duplication** — extract common logic
4. **Write clear tests** — descriptive names, single assertions per scenario
5. **Maintain coverage** — don't drop below targets
6. **Document new features** — update README.md

### Commit Message Format

```
<type>(<scope>): <subject>

<body>

<footer>
```

**Types:** `feat`, `fix`, `docs`, `test`, `refactor`, `chore`

**Examples:**
```
feat(tools): add file_read tool with access control
fix(parser): handle empty SSE lines correctly
test(context): add reset scenario tests
```

---

## License

MIT
