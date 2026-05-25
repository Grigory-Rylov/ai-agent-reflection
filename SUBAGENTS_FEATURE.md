# SubAgents Feature — Flexible Multi-Agent Pipeline

## 1. Problem

Current `orchestrator.go` hardcodes a 3-phase loop:
```
Coordinator → Developer → Reviewer (×3 max iterations)
```
This is rigid — every task goes through all three phases even if not needed.
Adding or reordering agents requires modifying Go code.

## 2. Solution: `subagent` Tool

### 2.1 Concept

Any agent can call `subagent(name, task)` to delegate work to a sub-agent.
The call is **synchronous** — the caller waits for the sub-agent's response.

```
Coordinator (depth=0)
  ├─ subagent("worker", "implement main.go")   → result
  ├─ subagent("qa", "review this code")
  │    ├─ subagent("worker", "fix the bug")     → fixed code
  │    └─ review_approve + return final code
  └─ consolidates → final response
```

### 2.2 Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  Orchestrator.ExecuteTask()                                │
│  Creates coordinator agent with subagent tool (depth=0)    │
│  Calls coordinator.ProcessMessage(task)                    │
└──────────────────────────┬──────────────────────────────────┘
                           │
              ┌────────────▼────────────┐
              │  coordinator agent      │
              │  prompt: coordinator.txt │
              │  tools: [main tools +   │
              │         subagent(depth)] │
              └────────────┬────────────┘
                           │
              subagent("qa", "review...")
                           │
              ┌────────────▼────────────┐
              │  qa agent               │
              │  prompt: qa.txt         │
              │  tools: [main tools +   │
              │         subagent(depth+1)│
              │         review_approve]  │
              └────────────┬────────────┘
                           │
              subagent("worker", "fix...")
                           │
              ┌────────────▼────────────┐
              │  worker agent (LEAF)    │
              │  prompt: worker.txt     │
              │  tools: [main tools]    │
              │  (NO subagent tool!)    │
              └────────────┬────────────┘
                           │
              returns result text
```

### 2.3 Depth Control

| Agent | Has `subagent` tool? | Has `review_approve`? | Max depth |
|---|---|---|---|
| coordinator | ✅ (depth=0) | ❌ | 0 (created by Orchestrator) |
| worker | ❌ (leaf) | ❌ | N/A |
| qa | ✅ (depth = parent+1) | ✅ | 2 (qa → worker) |

MaxDepth = 2 means:
- coordinator(0) → qa(1) → worker(2) ← 2 levels of nesting
- deeper chains blocked

### 2.4 QA Iteration Loop

QA manages its own fix cycle:

```
QA receives code → reviews → if issues found:
  1. subagent("worker", "fix: <description>\n\nCode:\n```\n...```")
  2. Worker fixes, returns code
  3. QA reviews again
  4. Repeat up to 3 iterations
  5. review_approve(reason="summary")
  6. Return final approved code in response text
```

No `review_revise` tool needed — QA uses `subagent(worker)` for fixes.
`review_approve` is the signal that everything is done.

### 2.5 QA returns result to Coordinator

Because `subagent` is **synchronous**:

```
coordinator:
  result = call subagent(qa, "review:\n" + code)
  // result == QA's final response text (containing approved code)
  return result to user
```

QA just writes the approved code in its response. The `subagent` tool returns
this text to the coordinator. No special parsing needed.

## 3. SubAgentTool Implementation

### 3.1 Struct

```go
type SubAgentTool struct {
    AgentConfig     agent.Config
    MainTools       *tools.Registry
    SystemPromptDir string
    CurrentDepth    int
    MaxDepth        int
    PeerID          int64
    ThinkingPeerID  int64
    VKClient        VKClient
    Logger          Logger
    Debug           bool
}
```

### 3.2 Execute Logic

```
Execute(ctx, inputs):
  1. Extract "name" and "task" from inputs
  2. Validate: name must be "worker" or "qa"
  3. Check CurrentDepth < MaxDepth (else return error)
  4. Load system prompt from SystemPromptDir + name + ".txt"
  5. Create agent.NewAgent(cfg) with the prompt
  6. Register main tools (file_read, file_write, ...)
  7. If name != "worker":
     - Register subagent tool with CurrentDepth+1
  8. If name == "qa":
     - Register ReviewApproveTool
  9. Set thinking callback (prefix messages with [name])
  10. Call agent.ProcessMessage(ctx, task, PeerID)
  11. Return result as ToolResult{Success: true, Data: response}
```

### 3.3 Registration on Orchestrator Creation

`Orchestrator.registerSubAgentOn(a, depth)`:
- Creates SubAgentTool with given depth
- Registers it via `a.RegisterTools(subReg)`

## 4. System Prompts

### 4.1 `system_prompt/coordinator.txt`

```
You are a coordinator. Decompose tasks and delegate execution.

Available tool:
- subagent(name, task) — delegate to "worker" or "qa"

Workflow:
1. Call subagent("worker", ...) to implement each part
2. After implementation, call subagent("qa", ...) with the complete code
3. QA handles all fixes internally via subagent(worker)
4. Present QA's final result to the user

Do NOT implement anything yourself. Always delegate.
```

### 4.2 `system_prompt/worker.txt`

```
You are a worker. Implement the task given to you.

- Return complete code/result in your response
- You are a leaf agent — you CANNOT delegate
- Include everything needed
```

### 4.3 `system_prompt/qa.txt`

```
You are a QA engineer. Review code and ensure quality.

Available tools:
- subagent(name, task) — delegate "worker" for fixes
- review_approve(reason) — signal final approval

Workflow:
1. Review the code thoroughly
2. If issues found, call subagent("worker", "fix: <desc>\n\nFull code:\n```\n...```")
3. Repeat until all issues resolved (max 3 fix cycles)
4. Call review_approve(reason="<summary>")
5. Include the complete approved code in your final response
```

## 5. Changes to Existing Files

### 5.1 `orchestrator.go` — Simplified

Remove:
- `createAgent` (moved to SubAgentTool)
- `addReviewTools` (moved to SubAgentTool)
- `parseReviewResult` (no longer needed)
- Hardcoded 3-phase loop body
- `extractSection`

New `ExecuteTask`:
```go
func (o *Orchestrator) ExecuteTask(ctx, task, peerID) (string, error) {
    coordinator := agent.NewAgent(o.makeAgentConfig("coordinator"))
    o.addMainTools(coordinator)
    o.registerSubAgentOn(coordinator, 0)
    coordinator.SetThinkingCallback(...)
    return coordinator.ProcessMessage(ctx, task, peerID)
}
```

Keep:
- `OrchestratorConfig`, `Orchestrator` struct
- `GetCurrentAgent`, `setActiveAgent`
- `debugLog`, `makeThinkingCallback`

### 5.2 `review_tools.go` — Remove ReviewReviseTool

Only `ReviewApproveTool` remains.

### 5.3 `main.go` — Remove ReviewReviseTool from scenario test registrations

### 5.4 `handler.go` — Agent mode naming

`GetCurrentAgent()` now returns `"coordinator"`, `"worker"`, `"qa"` (set by `setActiveAgent` in thinking callback).

## 6. Integration Tests

### 6.1 New Scenario: `qa_worker_cycle`

Tests the full subagent delegation chain:

```
FILES:
  prompt.txt         — User task
  assert.txt         — Assertions

  coordinator agent calls:
    000_plan.txt     — Coordinator response (plans delegation)
    001_calls_worker.txt — Coordinator calls subagent("worker", "write code")
    002_worker_code.txt  — Worker responds with code
    003_calls_qa.txt     — Coordinator calls subagent("qa", "review")

  qa agent calls:
    004_qa_find_issue.txt   — QA finds issue, calls subagent("worker", "fix")
    005_worker_fix.txt      — Worker fixes, returns
    006_qa_approve.txt      — QA calls review_approve
    007_qa_final.txt        — QA returns final approved code
```

### 6.2 Mock Server Design

The mock server (`Scenario.MockServer()`) serves all LLM calls sequentially.
For subagent tests, steps cover all agents' responses in order they're called.

Each step is an SSE response with `content` field.

When the mock runs out of steps, it returns "Done." — and the test checks
that this doesn't happen (i.e. no unexpected LLM calls).

### 6.3 Test Assertions

```go
func TestScenario_QAWorkerCycle(t *testing.T) {
    scenario, _ := LoadScenarioDir("testdata/scenarios/qa_worker_cycle")
    server := scenario.MockServer()
    defer server.Close()

    reg := tools.NewRegistry()
    reg.Register(&tools.FileReadTool{})

    orchestrator := NewOrchestrator(OrchestratorConfig{...})
    result, _ := orchestrator.ExecuteTask(ctx, scenario.Prompt, 99999)
    scenario.AssertResult(t, result)
}
```
