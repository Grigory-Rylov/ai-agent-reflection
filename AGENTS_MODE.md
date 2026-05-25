# Agent Mode — SubAgent Pipeline

## Overview

Agent mode (`/agent` command) runs a **flexible subagent-based pipeline**. The orchestrator creates a **Coordinator** agent with a `subagent` tool. Any agent can delegate work to sub-agents (worker or qa) via the `subagent` tool, forming a tree of agent calls.

## Architecture

```
Coordinator (depth=0, has subagent tool)
  ├─ subagent("worker", "implement X")   → returns result
  ├─ subagent("qa", "review this code")
  │    ├─ subagent("worker", "fix bug")   → fixed code
  │    └─ review_approve + return final
  └─ consolidates → final response
```

### Agent Types

| Agent | Has `subagent`? | Has `review_approve`? | Can delegate to |
|---|---|---|---|
| **coordinator** | ✅ (depth=0) | ❌ | worker, qa |
| **worker** | ❌ (leaf) | ❌ | — |
| **qa** | ✅ (depth+1) | ✅ | worker |

- Max depth: 2 (coordinator → qa → worker)
- Worker is a **leaf** — cannot delegate further

## Entry Point

`/agent [task description]` — handled by `handler.go`:

```
handler.go (handleAgentCommand)
  └─ orchestrator.ExecuteTask(ctx, task, peerID)
       └─ creates Coordinator agent with subagent tool
            └─ Coordinator.ProcessMessage(task)
```

## Pipeline Flow

```
┌─────────────────────────────────────────────┐
│  Orchestrator.ExecuteTask()                 │
│  ─ Coordinator created with subagent(depth=0)│
│  ─ Coordinator.ProcessMessage(task)         │
└────────────────┬────────────────────────────┘
                 │
    ┌────────────▼────────────┐
    │  Coordinator            │
    │  Delegates via subagent │
    └────────────┬────────────┘
                 │
    ┌────────────┼────────────┐
    │            │            │
    ▼            ▼            ▼
  worker       worker        qa
  (leaf)       (leaf)     │  delegates
                           │  to worker
                           ▼
                         worker (leaf)
```

## SubAgentTool

The `SubAgentTool` (in `pkg/agentloop/subagent_tool.go`) implements the `tools.Tool` interface.

### Execute logic

1. Validate inputs (`name`: "worker"|"qa", `task`: string)
2. Check `CurrentDepth < MaxDepth`
3. Load system prompt from `system_prompt/{name}.txt`
4. Create agent with fresh session (no auto-save, no file persistence)
5. Register main tools (file_read, file_write, etc.)
6. If name != "worker": register `subagent` tool with `CurrentDepth + 1`
7. If name == "qa": register `review_approve` tool
8. Set thinking callback with `[{name}]` prefix
9. Call `ProcessMessage(ctx, task, peerID)`
10. Return result text as `ToolResult{Data: {response: text}}`

### Depth tracking

- Each `SubAgentTool` instance holds its own `CurrentDepth`
- When a non-worker sub-agent is created, it receives `CurrentDepth + 1`
- MaxDepth = 2 prevents infinite recursion

### QA Fix Loop

QA manages its own iteration:

1. Reviews code
2. If issues: calls `subagent("worker", "fix: ...")`
3. Worker fixes, returns code
4. QA reviews again
5. After max 3 iterations or clean approval: calls `review_approve`
6. Returns final approved code in response text

## System Prompts

Stored in `system_prompt/` directory:

| File | Purpose |
|---|---|
| `system_prompt/coordinator.txt` | Coordinator — decomposes tasks, delegates via subagent |
| `system_prompt/worker.txt` | Worker (leaf) — implements tasks, no delegation |
| `system_prompt/qa.txt` | QA — reviews code, calls worker for fixes, uses review_approve |

## Tool Registration per Agent

| Tool | Coordinator | Worker | QA |
|---|---|---|---|
| file_read, file_write, shell_execute, ... | ✅ | ✅ | ✅ |
| subagent (depth=N) | ✅ (0) | ❌ | ✅ (N+1) |
| review_approve | ❌ | ❌ | ✅ |

## Debug Logging

When run with `-d` flag:

```
[AGENT] Mode activated. Task: <truncated task>
[AGENT] Agent mode completed. Duration: 12.345s
```

Thinking callbacks include agent prefix:
```
[coordinator] I'll delegate this task
[worker] Implementing...
[qa] Reviewing code...
```

## Integration Testing

Scenarios in `testdata/scenarios/` use a mock HTTP server that serves SSE responses sequentially. Each step is a mock LLM response.

Two scenarios:
- **simple_approve** — coordinator receives direct text response (no delegation)
- **worker_task** — coordinator delegates to worker, returns worker's result

Adding a scenario:
1. Create directory `testdata/scenarios/{name}/`
2. Add `prompt.txt` (user task) and `assert.txt` (assertions with `contains:` / `not_contains:`)
3. Add numbered step files (`000_*.txt`, `001_*.txt`, etc.) for each LLM response
4. Add `runScenario(t, "{name}")` call in `run_scenario_test.go`

## Key Files

| File | Purpose |
|------|---------|
| `pkg/agentloop/orchestrator.go` | Orchestrator — creates coordinator, registers subagent tool |
| `pkg/agentloop/subagent_tool.go` | SubAgentTool — creates sub-agents, depth control |
| `pkg/tools/review_tools.go` | ReviewApproveTool (ReviewReviseTool removed) |
| `system_prompt/coordinator.txt` | Coordinator system prompt |
| `system_prompt/worker.txt` | Worker system prompt |
| `system_prompt/qa.txt` | QA system prompt |
| `cmd/vk-gateway/main.go` | Orchestrator creation and wiring |
| `SUBAGENTS_FEATURE.md` | Detailed design document |
