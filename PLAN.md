# Go agent -> oh-my-pi parity. Roadmap

Scope: `/home/grishberg/projects/go/ai-agent-reflection` (Go VK agent) vs `/home/grishberg/projects/ts/oh-my-pi` (TS reference).
Constraints honored: rebuild via `./build.sh` only, NEVER restart the agent/services; no code comments; funcs <=50 lines; TDD; wrap errors `fmt.Errorf("..: %w", err)`.

## Centerpiece requested: subagent "store + manager"
In oh-my-pi the subagent system is: a declarative **catalog** (bundled + file-discovered agent definitions with YAML frontmatter; `task/agents.ts`, `task/discovery.ts`, `omp agents unpack`) and a runtime **manager** (`task/index.ts` TaskTool with single + batch `{context,tasks[]}` form; `async/job-manager.ts` background jobs with IDs and auto-delivery; `registry/*` running/idle/parked/aborted; `tools/hub` send/wait/list/cancel; `tools/yield` result+schema; budgets + `task/parallel.ts` semaphore; optional `isolation-runner.ts` CoW git worktrees with merge-back).

### Current Go state (grounded)
- Catalog EXISTS: `pkg/agentpolicy` `AgentManager` + `AgentInfo`; bundled defaults (build/plan/general/qa/explore/summary) in `initDefaults`; extra agents from `config.json.agents` (prompt = path `agents/<name>.md`). Loaded in `main.go:293 initAgentManager`.
- Spawn tool EXISTS: `pkg/agentloop/subagent_tool.go` `task` (747 lines): name/task aliasing, recursion guard `MaxDepth`, per-agent permissions, review/read-only modes, background delivery, thinking callback, session store + checkpoints.
- `pkg/agentloop/active_registry.go` = only a busy-name flag (MarkAgentActive), NOT a per-instance registry with id/status/cancel/list.
- `main.go:314` registers one `SubAgentTool{MaxDepth:4}`.

### Gaps vs oh-my-pi (subagents)
| Capability | oh-my-pi | Go today |
|---|---|---|
| Agent DEFINITION as file (md+frontmatter) + discovery precedence (project/user/bundled) | yes | NO (defs only in config.json; md is prompt body only) |
| Rich per-agent fields (tools, model role, effort, output schema, blocking, budgets) | yes | partial (Model, Permission, flags) |
| Batch `{context,tasks[]}` parallel fan-out | yes (semaphore 32) | NO (one subagent per call) |
| Background jobs + IDs + auto-delivery | yes | partial (BG delivery, no job ids/wait) |
| Per-instance registry running/idle/aborted + list/cancel/wait (hub) | yes | NO |
| `yield` result tool + JSON-schema validation | yes | NO (text via ParseSubAgentResult) |
| `<task-result>` envelope + agent:// pointer | yes | NO |
| Budgets: soft request budget, wall-clock, output caps | yes | only MaxDepth |
| CoW worktree isolation + merge-back | yes | NO |

## Broader core-loop parity (Go vs oh-my-pi)
oh-my-pi core is `packages/agent/src/agent-loop.ts` (~3400 LOC) + `speculative-execution.ts` + `pause.ts` + `compaction/*`.

| Area | oh-my-pi | Go today | Priority |
|---|---|---|---|
| Compaction | typed outcome ok/cancelled/failed; input trimmed to budget; retry; native/remote; shake/prune layers | non-destructive + transient retry (this session); chunked map-reduce; mechanical prune backstop | P2 (close native/shake gap) |
| Context limits | per-model catalog + provider caps, server clamp | models.json per-model `context` else server probe (this session); config.json global cap removed | DONE |
| LLM timeouts | provider-level; tolerant of queue | 2h HTTP caps, idle-watchdog off by default (this session) | DONE |
| Tool concurrency | per-tool shared/exclusive + per-call resolver; parallel shared tools | tool loop exists; parallel-exec semantics not per-tool-policy | P2 |
| Speculative tool exec | full coordinator (admit/claim/commit/discard) | none | P3 |
| Steering/interrupt | peer-IRC aside injection at step boundary | steering tests exist; aside injection TBD | P2 |
| Global pause gate | pause.ts freezes main+subagents+advisor | none | P3 |
| Model/role resolution | @role aliases + overrides + prewalk + advisor | models.json + per-agent Model field | P2 |
| Session persistence/checkpoints | JSONL transcripts, resume, handoff | store + checkpoints exist | P2 |
| Skills/rules | skill:// protocol, autoload per agent | none | P3 |

Legend: P0 = headline subagent work; P1 = high-value cheap; P2 = parity; P3 = large/deferred.

## Design: Subagent Store + Manager (Go)

### 1) Store = file-based agent definitions (`pkg/agentpolicy/discovery.go`)
- Agent definition file = markdown: YAML frontmatter (metadata) + body (system prompt), like oh-my-pi.
- Extend `AgentCfg`/`AgentInfo` with: `Tools []string`, `ThinkingLevel string`, `Blocking bool`, `OutputSchema string` (raw JSON schema or `file:` path), `RequestBudget int`, `MaxRuntimeSec int`. Keep existing fields.
- Parser `parseAgentFile(path)` -> `AgentCfg` + prompt body. Minimal YAML frontmatter (scalars + comma/`-` lists); no new heavy deps.
- `DiscoverAgents(dirs []string) map[string]AgentCfg` with precedence: bundled `initDefaults` < user `~/.omp/agent/agents/*.md` < project `./agents/*.md` < `config.json.agents` (explicit wins).
- Wire `initAgentManager` (main.go) to run discovery before `LoadFromConfig`.

### 2) Manager = orchestration (`pkg/agentloop`)
- **Batch task tool**: `task` accepts single form (today) OR `{context, tasks:[{name?,agent?,task,effort?}]}`; fan out in parallel under a semaphore (`maxConcurrency`, default 8); aggregate per-item results in stable order; keep recursion guard + placeholder-task guard.
- **Registry** `subagent_registry.go`: per-instance `SubagentRef{ID,Name,Status,StartedAt,Cancel}`, states `running|done|error|aborted`; `Register/List/Cancel(id)`. Replaces reliance on busy-name-only `active_registry` for spawn tracking (keep activity map for loop-detection).
- **Async + IDs**: `task` (batch) returns `{ids:[...]}` immediately; results auto-deliver to the peer via existing delivery callback; a `subagents` tool `{action:list|wait|cancel, id?}` lets the model inspect/collect/abort (the VK analog of oh-my-pi `hub`).
- **yield + schema**: child gets a `yield` tool submitting structured output; validated against the agent `OutputSchema` (retry budget, permissive/strict). Result wrapped in a `<task-result>` envelope (status + preview + pointer).
- **Budgets**: per-agent soft request budget + wall-clock (`MaxRuntimeSec`) + output caps; force-stop path.

## Phased roadmap
- **A (P0) Store**: file discovery + precedence + richer AgentInfo + unit tests. <-- implement first
- **B (P0) Manager**: batch fan-out + registry + async IDs + `subagents` list/wait/cancel + tests.
- **C (P1) yield + schema + envelope; budgets.**
- **D (P2) per-agent model/role resolution (roles, overrides); prewalk/advisor.**
- **E (P2) tool concurrency policy; steering aside-injection.**
- **F (P3) CoW worktree isolation + merge-back; skills; speculative exec; pause gate.**

Verification per step: `go build ./...`, `go vet` on touched pkgs, `go test` (table-driven), `./build.sh`. Never restart agent.

## Status
- **Phase A (Store) DONE + built** (`./build.sh`, no restart). `pkg/agentpolicy/discovery.go`: frontmatter parser + `DiscoverAgentFiles` precedence. `AgentInfo`/`AgentCfg` +6 fields (tools, thinkingLevel, blocking, outputSchema, requestBudget, maxRuntimeSec). `initAgentManager` precedence: bundled < `config.json.agents` < agent files (files authoritative). `agents/*.md` rewritten as oh-my-pi definitions (YAML frontmatter + prompt body, bodies preserved byte-for-byte): `lead`(coordinator, spawns worker/qa/reviewer/explore/general), `worker`(spawns explore/reviewer), `qa`(review, leaf), `reviewer`(review, leaf, read-only tools). `config.json` `agents` block removed. Tests green: split/parse/precedence + live-store smoke. Behavior changes: reviewer now read-only (write/bash denied); lead/worker/qa now carry explicit role flags.
- **Parsed-but-not-yet-applied**: `model`, `thinkingLevel` (subagent inherits parent model today; confirmed Model/Temperature/TopP fields are never applied at spawn). Wire in Phase D.

## Core-loop parity, refined (core scout)
oh-my-pi extras absent in Go: mid-turn steering/follow-up/aside queues; per-tool concurrency (shared/exclusive) + interruptibility + approval tiers; speculative tool-exec coordinator; process-wide pause gate; append-only stable-prefix context for provider cache hits; native per-family tokenizers; 5-method compaction ladder + speculative pre-compaction + native provider routes; retry/fallback model chains; skills/hooks/extensions/MCP planes; ~40 builtin tools with load modes.
Go already has: streaming+retry+overflow compaction (non-destructive), steering drain, tool-call dedup, serial tool exec with permission/path checks + output spill, two-level LLM compaction, model-alias holder with live context probe, SQLite session/checkpoint/chain resume across restart, `#agent` lane queue.

## Next: Phase B (Manager runtime)
Batch `task` fan-out (`{context, tasks[]}` under a semaphore) + per-instance registry (running/done/error/aborted) + async job IDs + auto-delivery + a `subagents` tool (list/wait/cancel). Touches `subagent_tool.go` + new `subagent_registry.go`. Checkpoint: this changes the live `task` tool, so confirm before implementing.

## Status update (Manager, part 1)
- **Subagent registry + `subagents` tool DONE + built** (`./build.sh`). `pkg/agentloop/subagent_registry.go`: process-global thread-safe registry of running spawns (id, agent, peer, depth, status running/aborted, started, cancel). `Execute` now wraps its context in a cancelable one, registers the spawn, and deregisters on return. New `subagents` tool (`pkg/agentloop/subagents_tool.go`): `action=list` (running spawns w/ elapsed), `action=cancel` (aborts a running subagent via its context). Registered in `main.go`; documented in `system_prompt.txt`. Tests green: register/list/finish + cancel-fires-context. Additive — single-call `task` behavior unchanged.
- **Deferred (needs user async decision + stateful refactor)**: parallel batch fan-out. `SubAgentTool` keeps per-spawn mutable state on the receiver (`AgentSessionID`, KV slots, Chain, active_registry) so running N subagents from one instance would data-race; requires extracting per-spawn state first. Cancel is currently useful for concurrent/background (`#agent` lane) spawns, not the blocking single call.
- Still to wire in a later phase: apply per-agent `model`/`thinkingLevel` at spawn (fields parsed, currently ignored).

## Status update (Phase A — parallel batch DONE)
- `task` now supports batch form `{context, tasks:[{subagent_type, prompt}]}` -> runs items in parallel under a semaphore (`maxConcurrency`, default 4) and returns aggregated results in stable order. Single-call form unchanged (refactored into `runSpawn`).
- Concurrency safety: per-spawn clone via `cloneForSibling` (deep-copied `Chain`, unique `spawnNonce` for session IDs, `NoSlotSave=true` for siblings to avoid KV-slot thrash at `--parallel 1`). Verified shared primitives: Store uses `SetMaxOpenConns(1)`+WAL (serializes, race-free); SlotManager fully mutex-guarded.
- New file `pkg/agentloop/subagent_batch.go`. Struct gained `MaxParallel`, `NoSlotSave`, `spawnNonce`. `createAgent` skips slot assignment when `NoSlotSave`.
- Tests (no LLM needed): `fanOut` cap+stable-order, `aggregateBatchResults` success/error mapping, `cloneForSibling` isolation, `firstNonEmpty`, `promptWithContext`. Green. Build+vet clean, `./build.sh` rebuilt.
- Note: at the current `--parallel 1` server this gives tool/IO overlap + non-blocking fan-out, NOT extra LLM throughput (engine serializes). Real LLM parallelism needs `--parallel K` (KV x K, likely OOM at 256k) or routing siblings to the second server (8084).

## Phase B (next, not yet started)
- Async job IDs: `task` returns spawn IDs immediately, results auto-deliver to the peer, `subagents wait <id>` collects. Requires migrating lead/worker/qa prompts that assume synchronous return so existing review/QA chains don't lose results.

## Status update (Phase B — async job IDs, DONE + built)
- Async job layer DONE: `task` (single and batch) can run in the BACKGROUND, returning `{status:started, ids:[...]}`; each result auto-delivers to the peer (`[subagent <name> job <id>] done|FAILED:` via the delivery callback) when its goroutine finishes. New global job store `pkg/agentloop/subagent_jobs.go` (`subJob`/`subJobStore`: create/wait/cancel/snapshot, per-job done channel). `subagents` tool gained `action=jobs` (id/agent/status/summary) and `action=wait` (block on one id or all; `timeout_seconds`).
- DEFAULT = SYNCHRONOUS (safe); async is OPT-IN: config `async_subagents:true` (global) or per-call `async:true`; `blocking:true` forces sync per call. Reason: this agent's loop does NOT re-enter the parent turn on background-job completion (oh-my-pi does via hub auto-delivery into the parent loop), so async-by-default would strand a coordinator that needs the result to chain spawn->result->spawn. The 3 core scenario tests + coordinator->worker->qa pipeline depend on synchronous return. Batch keeps its SYNCHRONOUS parallel aggregate (fan-out + ordered results) by default; async batch = fire-and-forget ids.
- Guards run BEFORE dispatch (sync AND async): placeholder-task guard hoisted into `Execute` (single) + new `firstBadTask` validation (empty/placeholder per item) in `runBatch` and `startAsyncBatch`, so garbage never spawns a job.
- Prompt migration: `system_prompt.txt` (Background subagents section: async opt-in + wait/jobs/cancel), `agents/lead.md` + `agents/worker.md` (spawn then `subagents action=wait <id>` when the answer is needed before continuing).
- Tests green (`pkg/agentloop` full suite incl. scenario/slot/placeholder + new `subagent_jobs_test.go`): wait-completes, wait-timeout, cancel-cancels-context, formatJobResult. Build+vet clean; `./build.sh` rebuilt. NOT live until the agent is restarted by the user.
- Remaining for TRUE oh-my-pi async-by-default (deferred, large): loop job-completion re-entry — inject a finished background job's result into the originating parent turn so an async coordinator continues after the spawn (this session delivers to the peer chat, not parent-turn re-entry).


## Status update (async BY DEFAULT + safe, oh-my-pi-faithful)
- Async is now the DEFAULT for the conversational main agent (agentloop path): `task` spawns in the background (ids), and the parent turn HOLDS OPEN at the stop boundary until its subagents settle (`awaitSubagentResult` in `pkg/agent/loop.go` `runTurn`), mirroring oh-my-pi `agent-loop.ts` outer-drain (~1565-1583): before yielding, drain aside/follow-up messages and continue if non-empty. Each settled job's result is Admitted into the owning session's `PeerInput` (`makeBGDelivery` / `handleBackgroundNotification`) and folded back into the parent turn as a user message, so a coordinator chains spawn->result->spawn without losing results.
- Correlation by owner token (`ownerToken` in `subagent_jobs.go`): `BGOwner` (session id) for sub-agents, `"main:<peer>"` for the top agent, so jobs map to the exact turn that spawned them, per-peer safe across nesting. Job store gained settled counters + a settle signal (`pendingForOwner` / `waitForNextSettle`); `pkg/agent` depends on it via the `SubagentWatcher` interface (dependency inversion; `agentloop.NewSubagentWatcher` adapter injected into every agent `Config`).
- Orchestrator (`RunAgent`/`ExecuteTask` + `#agent` routed runs) runs subagents SYNCHRONOUSLY (`Blocking=true`): it is a run-to-completion API returning the final result; scenario/slot suites stay deterministic. The interactive agent is the async one.
- Safety valves: per-wait cap 30s, total hold cap 60m, ctx-cancelable (/stop). `blocking:true` forces inline synchronous per call; config `blocking_subagents` (default false = async) forces sync globally.
- Tests: `pkg/agent` `TestAwaitSubagentResult{Collects,NoPending,OtherOwner}`; `pkg/agentloop` job-store owner/settle tests; scenario + slot suites green. build/vet clean; `./build.sh` rebuilt. NOT live until agent restart (user's call).
- Remaining for full oh-my-pi: true fire-and-forget jobs that outlive the turn need idle-wake (deliver to an idle session and re-drive a turn at the handler level); today hold-open always collects, so nothing is stranded, but spawns do not detach past the turn.


## Review vs oh-my-pi (async job runtime) — divergences fixed
Compared our async subagent runtime against `oh-my-pi` `async/job-manager.ts`. Fixed real divergences in `pkg/agentloop/subagent_jobs.go` + `subagent_tool.go`:
- **Running-job cap**: oh-my-pi caps at `DEFAULT_MAX_RUNNING_JOBS=15` and rejects extra `register()` calls. We had none -> unbounded concurrent async jobs (goroutine + sub-agent-session memory, OOM risk). Now `subJobs.create` atomically returns nil at 15 running; `task` single returns `"background subagent limit reached (15)"`, batch starts what fits and reports `skipped`.
- **Retention/eviction**: oh-my-pi evicts a settled job row after `DEFAULT_RETENTION_MS=5m`. We never deleted -> the `subJobs` map grew for the process lifetime (agent runs for days). Now `complete()` schedules `evict(id)` after 5m.
- **Dead-letter instead of misroute**: oh-my-pi routes an owned delivery ONLY to its owner's sink and NEVER falls back to a shared default (would leak one agent's result into another session). We fell back to `"main"` unconditionally when the owner's sink was gone. Now the `"main"` fallback applies only to top-level jobs (`BGOwner==""`); a sub-agent-owned job with a missing sink is dead-lettered (warn) with its result kept on the row until eviction.
- Non-divergences (intentional/na): delivery-retry exists in oh-my-pi only because their sink awaits an async `enqueueWithReceipt`; our primary delivery is an in-memory synchronous `PeerInput.Admit` that cannot fail. Hold-open (collect within the turn) vs oh-my-pi yield+idle-wake is the documented architectural adaptation (we lack handler-level idle-wake); it is the safe choice and also avoids turns hanging on jobs that complete after yield.
- Tests added: `TestSubJobCreateEnforcesRunningCap`, `TestSubJobEvictRemovesFromSnapshot`. Full `agentloop`+`agent` suites green; `./build.sh` rebuilt.
