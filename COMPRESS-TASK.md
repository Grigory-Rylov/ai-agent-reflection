# Алгоритм сжатия контекста (opencode, TypeScript)

## 1. Два механизма

В opencode существуют **два независимых** механизма управления контекстом:

| Механизм | Суть | Триггер | Файл |
|----------|------|---------|------|
| **Compaction** | LLM-суммаризация старой истории в структурированный Markdown | При переполнении контекстного окна | `src/session/compaction.ts` |
| **Pruning** | Удаление output старых tool-вызовов (без LLM) | После каждого шага цикла | `compaction.ts:prune()` |

---

## 2. Overflow Detection (`overflow.ts`)

### Usable capacity

```typescript
const COMPACTION_BUFFER = 20_000

function usable(input) {
  const reserved = cfg.compaction?.reserved ??
    Math.min(COMPACTION_BUFFER, maxOutputTokens(model))
  return model.limit.input
    ? Math.max(0, model.limit.input - reserved)
    : Math.max(0, model.limit.context - maxOutputTokens(model))
}
```

Резервируется `min(20000, maxOutputTokens)` токенов, чтобы осталось место для ответа компакшена.

### IsOverflow

```typescript
function isOverflow(input) {
  if (cfg.compaction?.auto === false) return false
  if (model.limit.context === 0) return false
  const count = tokens.total
    || tokens.input + tokens.output + tokens.cache.read + tokens.cache.write
  return count >= usable(input)
}
```

Срабатывает, когда потреблённые токены >= `usable()`.

---

## 3. Main алгоритм Compaction

### 3.1 Триггеры вызова (в `session/prompt.ts`)

1. **После step-finish** — процессор ставит `needsCompaction = true` при `isOverflow()` (processor.ts:610-615)
2. **API error** — `ContextOverflowError` от провайдера; процессор выставляет `needsCompaction` (processor.ts:754-755)
3. **В основном цикле** — если `lastFinished.summary !== true` и `isOverflow()` → `compaction.create()` с `auto: true` (prompt.ts:1322-1328)
4. **Процессор вернул `"compact"`** — стрим прерван, `compaction.create()` с `overflow: true` (prompt.ts:1476-1484)
5. **Manual** — ручной вызов пользователем

### 3.2 Compaction.create()

Создаёт user-сообщение с `CompactionPart`:
```typescript
type CompactionPart = {
  type: "compaction"
  auto: boolean        // true = автоматический
  overflow?: boolean   // true = из-за overflow API
  tail_start_id?: MessageID  // ID первого сохраняемого сообщения (заполняется позже)
}
```

### 3.3 Алгоритм select() — "хвост" сохраняется

```typescript
const DEFAULT_TAIL_TURNS = 2
const MIN_PRESERVE_RECENT_TOKENS = 2_000
const MAX_PRESERVE_RECENT_TOKENS = 8_000

function select(messages, cfg, model):
  tail_turns = cfg.compaction?.tail_turns ?? DEFAULT_TAIL_TURNS  // = 2
  budget = preserveRecentBudget()  // min(8000, max(2000, usable * 0.25))

  // Все user-обороты (кроме compaction-маркеров)
  all = turns(messages)
  recent = all.slice(-tail_turns)

  // Идём от newest к oldest, пытаемся вписаться в budget
  for each turn in recent (с конца):
    if total + size(turn) <= budget:
      keep этот turn
    else:
      remaining = budget - total
      split = splitTurn(turn, remaining)  // пытаемся частично сохранить
      if split: keep = split
      break

  return { head: messages[0..keep.start], tail_start_id: keep.id }
```

**tail_turns** = 2 (по умолчанию) — два последних user-оборота (с их assistant/tool ответами) стараемся сохранить целиком.

**budget** = от 2000 до 8000 токенов (по умолчанию 25% от `usable()`).

Если последний оборот не влезает — `splitTurn()` пробует сохранить его часть, отрезая по одному сообщению с начала оборота.

### 3.4 Process Compaction (compaction.process())

```
1. Найти предыдущие завершённые компакшены (completedCompactions)
2. Собрать hidden-индексы (уже сжатые user+assistant пары)
3. Вызвать select() для не-hidden сообщений → head (сжать) + tail_start_id
4. head → toModelMessages() с stripMedia: true, toolOutputMaxChars: 2000
5. Отправить LLM промпт с SUMmary_TEMPLATE
6. Если result === "continue" и auto:
   - overflow mode: перепослать последнее user-сообщение (без медиа)
   - normal mode: synthetic "Continue" сообщение
```

### 3.5 Compaction Prompt (`SUMMARY_TEMPLATE`)

Жёсткая структура Markdown, LLM должен выдать только секции **без тегов `<template>`**:

```markdown
## Goal
- [single-sentence task summary]

## Constraints & Preferences
- [user constraints, preferences, specs, or "(none)"]

## Progress
### Done
### In Progress
### Blocked

## Key Decisions
## Next Steps
## Critical Context
## Relevant Files
```

Если уже есть предыдущий summary — промпт:
```
Update the anchored summary below using the conversation history above.
Preserve still-true details, remove stale details, and merge in the new facts.
<previous-summary>
  ...
</previous-summary>
```

Если нет — `Create a new anchored summary from the conversation history above.`

### 3.6 FilterCompacted — перестановка сообщений

После компакшена сообщения переставляются для модели:

**До:** `[msg1, msg2, ..., compaction-user, summary-assistant, ...tail-msgs, ...newer-msgs]`

**После:** `[compaction-user, summary-assistant, ...tail-msgs (из старой позиции), ...newer-msgs]`

Модель видит: сначала summary, потом сохранённый хвост хронологически, потом новые сообщения.

Реализация — `filterCompacted()` в `message-v2.ts:1014-1065`.

---

## 4. Pruning (без LLM)

### Алгоритм

```typescript
const PRUNE_MINIMUM = 20_000    // мин. токенов для оправдания pruning
const PRUNE_PROTECT = 40_000    // защищаем столько токенов хвоста
const PRUNE_PROTECTED_TOOLS = ["skill"]  // эти инструменты не трогаем

function prune():
  // Идём от newest к oldest
  // Пропускаем первые 2 user-оборота (защищены)
  // Останавливаемся на assistant с summary: true (уже сжато)
  // Останавливаемся на уже compacted tool part
  for each msg (с конца):
    пропускаем первые 2 user turns
    если assistant.summary → break
    для каждого tool part:
      если already compacted → break
      estimate = estimateTokens(output)
      total += estimate
      если total <= PRUNE_PROTECT → continue (защищаем хвост)
      pruned += estimate
      добавляем в toPrune

  // Если набрали > PRUNE_MINIMUM — выполняем
  if pruned > PRUNE_MINIMUM:
    for each part in toPrune:
      part.state.time.compacted = Date.now()
      // В model messages output заменяется на "[Old tool result content cleared]"
```

**Суть:** защищаем последние 40K токенов tool output. Всё старше этого — помечаем `compacted`. Минимальный порог — 20K токенов, иначе pruning не имеет смысла.

---

## 5. Tool Output Truncation (отдельно)

При конвертации сообщений для компакшена (`toModelMessagesEffect`):

```typescript
function truncateToolOutput(text, maxChars = 2000):
  if text.length > maxChars:
    return text[0:maxChars] + "\n[Tool output truncated for compaction: omitted N chars]"
```

При обычной работе (tool.ts):
- `max_lines = 2000`
- `max_bytes = 51200` (50KB)
- Полный output сохраняется в `~/.opencode/data/tool-output/`
- В контекст идёт preview

---

## 6. Config knobs (в opencode.jsonc)

```jsonc
{
  "compaction": {
    "auto": true,                          // авто-компакшен
    "prune": true,                         // авто-pruning
    "tail_turns": 2,                       // сколько последних user-оборотов сохранять
    "preserve_recent_tokens": 8000,        // бюджет для хвоста (по умолчанию 25% от usable)
    "reserved": 20000                      // резерв токенов для ответа компакшена
  },
  "tool_output": {
    "max_lines": 2000,
    "max_bytes": 51200
  }
}
```

Env-флаги:
- `OPENCODE_DISABLE_AUTOCOMPACT=true` — отключить авто-компакшен
- `OPENCODE_DISABLE_PRUNE=true` — отключить pruning

**Compaction agent** — отдельный агент `"compaction"`, можно настроить модель в `agent.compaction`.

---

## 7. Полный lifecycle

```
Normal operation → сообщения копятся в SQLite
    │
    ├── После каждого step-finish: check isOverflow()
    │     └── overflow → needsCompaction = true
    │
    ├── API error (context overflow) → needsCompaction = true
    │
    ├── Основной цикл prompt.ts:
    │     ├── Есть compaction task? → compaction.process()
    │     ├── Есть overflow? → compaction.create() + continue
    │     └── Нормальный запрос к LLM
    │
    ├── Compaction.process():
    │     ├── Найти предыдущие summaries
    │     ├── select() → head (сжать) + tail (оставить)
    │     ├── head → LLM с SUMMARY_TEMPLATE
    │     ├── Результат: assistant-сообщение с summary: true
    │     └── auto-continue → synthetic "Continue" / replay user
    │
    ├── После завершения шага: compaction.prune()
    │     └── Mark tool outputs как compacted
    │
    └── filterCompacted() — перестановка при формировании запроса к модели
          [compaction-user, summary, ...tail, ...new]
```

---

## 8. Реализация в ai-agent-reflection (Go)

### Файлы реализации

| Файл | Что содержит | Статус |
|------|-------------|--------|
| `pkg/compress/overflow.go` | `Usable()`, `IsOverflow()`, `COMPACTION_BUFFER`, `OUTPUT_TOKEN_MAX` | ✅ |
| `pkg/compress/pruning.go` | `PruneMessages()`, `PruneConfig`, `PRUNE_MINIMUM`, `PRUNE_PROTECT` | ✅ |
| `pkg/compress/filter.go` | `FilterCompacted()`, `FindCompactionMarkers()` | ✅ |
| `pkg/compress/compaction.go` | `SelectMessages()`, `BuildSummaryPrompt()`, `CompactWithOpenCode()`, `SUMMARY_TEMPLATE`, `Turn`, `OpenCodeCompactResult` | ✅ |
| `pkg/compress/opencode_compact_test.go` | Тесты для overflow, pruning, select, filter, LLM compaction (с mock) | ✅ |
| `pkg/agentloop/config.go` | `EnableOpenCodeCompaction`, `TailTurns`, `PreserveRecentTokens`, `CompactionReserved`, `EnablePruning`, `AutoContinueAfterCompact` | ✅ |
| `pkg/agentloop/agentloop.go` | `checkAndCompressOpenCode()`, `applyOpenCodeCompactResult()`, `runPruning()` | ✅ |
| `pkg/tokenizers/tokenizer.go` | `Message.ToolCallID`, `Name`, `Compacted`, `Summary` | ✅ |

### Ключевые отличия от opencode

- **Estimation**: используется `EstimateTokensSimple()` (1 токен ≈ 4 символа) вместо API-токенайзера
- **LLM вызов**: через `LLMCompressorInterface.Compress()` (как и в legacy коде)
- **Синхронность**: compaction и pruning выполняются синхронно (в opencode — fire-and-forget)
- **Select адаптирован**: `SelectMessages()` возвращает `SelectResult{Head, TailStartID}` для Go-стиля

### Тесты

| Тест | Описание |
|------|----------|
| `TestUsable` | Проверка расчёта доступного контекста (4 кейса) |
| `TestIsOverflow` | Проверка детекции переполнения (3 кейса) |
| `TestPruneMessages` | Проверка pruning: защита первых 2 turns, пороги PRUNE_PROTECT/PRUNE_MINIMUM, placeholder |
| `TestSelectMessages` | Проверка tail preservation: 0 turns, budget limits, split turn |
| `TestBuildSummaryPrompt` | Проверка промпта: new summary vs update, включение истории |
| `TestCompactWithOpenCode` | Интеграция с mock LLM: ошибка без LLM, результат с summary+tail, уменьшение токенов |
| `TestFindCompactionMarkers` | Поиск summary-маркеров в истории |
| `TestFilterCompacted` | Перестановка сообщений: compaction-user + summary + tail |
| `TestOpenCodeFullFlow` | End-to-end: messages → overflow check → prune → compact → результат |

## 9. Ключевые файлы (opencode original)

| Файл | Что содержит |
|------|-------------|
| `src/session/compaction.ts` | Compaction service: `select()`, `prune()`, `process()`, `create()`, `SUMMARY_TEMPLATE`, `buildPrompt()` |
| `src/session/overflow.ts` | `isOverflow()`, `usable()` |
| `src/session/prompt.ts` | Main loop: триггеры compaction (строки ~1310–1328, ~1476–1484), вызов prune (строка 1495) |
| `src/session/processor.ts` | Step-finish: `needsCompaction` (строка 610–615) |
| `src/session/message-v2.ts` | `filterCompacted()`, `toModelMessagesEffect()`, `truncateToolOutput()`, `latest()`, `CompactionPart`, `ToolPart.state.time.compacted` |
| `src/util/token.ts` | `Token.estimate()` |
| `src/provider/transform.ts` | `OUTPUT_TOKEN_MAX = 32000`, `maxOutputTokens()` |
| `src/config/config.ts` | `compaction` config schema (строки 268–287) |
| `src/tool/truncate.ts` | Tool output truncation при выполнении |
