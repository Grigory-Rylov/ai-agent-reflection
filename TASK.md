# TASK.md — План доработок Go Agent (по итогам сравнения с OMP)

Ветка: `feat/OMP_improvements`

## Task 1. Спекулятивная компакция (пред-порог) — [x] DONE

**Проблема:** компакция реактивная — `compactIfNeeded` (pkg/agent/agent_impl.go:277) и `checkAndCompressOpenCode` (pkg/agentloop/agentloop.go:989) запускают LLM-суммаризацию только после overflow. С медленной локальной LLM это блокирует ход на 30–120 сек.

**Решение:**
1. Новый порог `SPECULATIVE_COMPACT_RATIO = 0.75` от usable (конфиг: `speculative_compact_ratio`, default 0.75, 0 = off).
2. При `tokens >= ratio * usable` и нет активного компакта — запустить асинхронную goroutine с `CompactWithOpenCode` (копия истории на момент запуска).
3. При реальном overflow:
   - если спекулятивный результат готов и свежий (история с момента запуска не выросла > 20% и нет новых tool-результатов) — применить его мгновенно;
   - иначе — отменить спекуляцию и сделать синхронную компакцию (текущее поведение).
4. Статистика в debug.log: `[SPEC-COMPACT] started at X tokens / ready after Ys / applied|discarded`.

**Файлы:** pkg/compress (новые: speculative.go), pkg/agent/agent_impl.go, pkg/agentloop/agentloop.go, pkg/agentloop/config.go, cmd/vk-gateway-restarter/main.go (config field).

**Тесты (TDD):**
- `TestSpeculativeCompactionTriggersAtRatio` — запуск при 75% usable.
- `TestSpeculativeCompactionAppliedOnOverflow` — готовый результат применяется без повторного LLM-вызова.
- `TestSpeculativeCompactionDiscardedOnGrowth` — результат отброшен, если история выросла.
- `TestSpeculativeCompactionDisabledWhenRatioZero`.

**Готово, когда:** тесты зелёные, `go build ./...` ок, в integration-сценарии при overflow нет повторного LLM-вызова суммаризации.

## Task 2. Фоновые shell-задачи + уведомление в VK — [x] DONE

**Проблема:** `ShellExecuteTool` (pkg/tools/impl_tools.go:274) блокирует ход до завершения (timeout до 600 сек). Долгие сборки/тесты мешают диалогу.

**Решение:**
1. Новый инструмент `shell_background`: параметры `command`, `name` (опц.), `notify` (bool, default true). Возвращает `task_id` сразу.
2. Реестр задач `pkg/tools/background.go` (BackgroundHub):
   - `Start(command) (taskID, error)` — `sh -c` в отдельной process group, stdout+stderr в файл `background/<taskID>.log` (в allowed dir или tmp).
   - `Status(taskID)` — running/exit_code/started_at/duration.
   - `Output(taskID, tailLines)` — последние N строк лога.
   - `Kill(taskID)` — SIGKILL process group.
   - Лимит одновременных задач (default 4, конфиг `max_background_tasks`).
3. Новый инструмент `shell_check`: параметры `task_id`, `tail` (default 20). Возвращает статус + хвост вывода + полный путь к логу.
4. Уведомление в VK: по завершении задачи с `notify=true` — `VKClient.SendMessage(peerID, "[BG] task <name/id> finished (exit N, Xs) — details via shell_check")`. PeerID и VKClient пробрасываются в hub из main.go.
5. Hub переживает компакцию (не привязан к сессии), живёт на весь процесс. По `/clear` — задачи не убиваются, но привязка к peer теряется (допустимо, задокументировать).

**Файлы:** pkg/tools/background.go (+test), pkg/tools/impl_tools.go (регистрация), pkg/agentloop/config.go (VKClient уже есть), main.go (создание hub, регистрация инструментов, система prompt).

**Тесты (TDD):**
- `TestBackgroundStartAndCheck` — `sleep 1` → running → finished, exit 0.
- `TestBackgroundKill` — `sleep 100` → kill → exit -1.
- `TestBackgroundLimit` — 5 задач при лимите 4 → ошибка.
- `TestBackgroundNotify` — fake VKClient получил сообщение при завершении.
- `TestBackgroundOutputTail`.

**Готово, когда:** тесты зелёные, бинарник собирается, VK-уведомление приходит (ручная проверка).

## Task 3. Critical patterns (принудительный ask) — [x] DONE

**Проблема:** permission-система (pkg/permission) позволяет allow-нуть любой паттерн, включая `rm -rf /`. Нужен статический список команд, которые ВСЕГДА требуют подтверждения.

**Решение:**
1. `pkg/permission/critical.go`:
   - `CriticalPatterns []CriticalPattern` — список `{name, regex, reason}`: `rm -rf` с `~`/`/`/`*`, `mkfs`, `dd if=`, `:(){ :|:& };:` (fork bomb), `> /dev/sd*`, `shred`, `wipefs`, `chmod -R 777 /`, `git push --force` в origin, `systemctl stop/restart` (для агента — по AGENTS.md только по запросу), `kill` по PID-диапазону, `mv` на `/`-корень.
   - `CheckCritical(command string) (matched bool, reasons []string)` — по каждому сегменту из `SplitCommands` (уже есть в shell.go).
2. В `checkShellPermission` (pkg/agent/tool_executor.go:180) — ПЕРЕД evaluate по правилам: если `CheckCritical` matched → принудительный ask с текстом `[CRITICAL] <reasons>`. Ответ «Always allow» на critical pattern НЕ сохраняется в learning (только one-time allow).
3. Конфиг `critical_patterns_enabled` (default true) — отключить нельзя для bash, только для тестов.

**Файлы:** pkg/permission/critical.go (+test), pkg/agent/tool_executor.go.

**Тесты (TDD):**
- Таблица: `rm -rf /`, `rm -rf ~`, `rm -rf $HOME`, `mkfs.ext4 /dev/sda1`, `dd if=/dev/zero of=/dev/sda`, `chmod -R 777 /`, `git push --force origin main` → matched.
- Таблица: `rm -rf ./build`, `rm -rf /tmp/test`, `git push origin main`, `rm file.txt` → не matched.
- `TestCriticalForcesAskDespiteAllowRule` — даже при правиле `bash: {"rm *": "allow"}` идёт ask.
- `TestCriticalNotLearned` — после one-time allow правило не сохраняется.

**Готово, когда:** тесты зелёные, сборка ок.

## Task 4. Schema-результаты суб-агентов — [x] DONE

**Проблема:** `SubAgentTool.Execute` (pkg/agentloop/subagent_tool.go:129) возвращает свободный текст. Родитель (lead) не может надёжно понять: успех/провал, что сделано, какие файлы изменены.

**Решение:**
1. В system prompt суб-агентов (agents/*.md — добавить секцию «Формат результата») требовать финальный ответ в формате:
   ```
   RESULT:
   status: success|failure|partial
   summary: <1-3 предложения>
   files: <список путей или none>
   next: <что делать дальше, или done>
   ```
2. `pkg/agentloop/subagent_result.go`:
   - `ParseSubAgentResult(text string) SubAgentResult` — парсит блок `RESULT:` (lenient: если блока нет — status=partial, summary=последний абзац).
   - `SubAgentResult{Status, Summary, Files []string, Next string, Raw string}`.
3. `SubAgentTool.Execute` возвращает `Data: {status, summary, files, next, response(кратко)}` вместо одного `response`. Если `status=failure` → `Success: false` + error с summary (родитель увидит ошибку и сможет перенаправить).
4. В описании инструмента (Description) — инструкция: «результат структурирован: status/summary/files/next».

**Файлы:** pkg/agentloop/subagent_result.go (+test), pkg/agentloop/subagent_tool.go, agents/*.md (секция формата).

**Тесты (TDD):**
- `TestParseSubAgentResultFull` — полный блок.
- `TestParseSubAgentResultMissing` — без блока RESULT → partial + fallback summary.
- `TestParseSubAgentResultMalformed` — битый блок (нет status) → partial.
- `TestSubAgentToolReturnsStructured` — fake agent возвращает текст → ToolResult.Data содержит status/summary.
- `TestSubAgentToolFailurePropagates` — status=failure → Success=false.

**Готово, когда:** тесты зелёные, сборка ок, lead получает структурированный ответ.

## Порядок работы

1. Task 3 (critical patterns) — самый маленький, независимый.
2. Task 4 (schema-результаты) — маленький, независимый.
3. Task 2 (background shell) — средний, независимый.
4. Task 1 (speculative compaction) — самый сложный, последним.

Каждый task: TDD (red→green→refactor), `go build ./...`, `go test ./...` перед переходом к следующему. Коммиты — только после подтверждения пользователя.