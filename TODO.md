# TODO — Сверка компактизации агента с opencode

Ветка: `opencode-comparsion`
Цель: привести механизм компактизации текущего агента (`~/projects/go/agent/pkg/compress`) к поведению opencode (`~/projects/ts/opencode/packages/opencode/src/session/compaction.ts` и `core/src/session/compaction.ts`), за единственным исключением: **после компактизации всегда должны добавляться в начало контекста промпты, добавленные командой `/pin`**.

---

## Обязательные доработки (чтобы поведение совпадало с opencode)

### 1. Компактизация должна удалять только head, а не всю историю
- **opencode**: старые сообщения (head) **не удаляются** из БД. При запросе `filterCompacted()` (message-v2.ts:532) переупорядочивает сообщения: `[compaction-user, summary, ...retained tail..., continue-user]`. Хвост (tail) помечается через `tail_start_id` и дословно сохраняется, головная часть скрывается.
- **агент**: `applyOpenCodeCompactResult()` (agentloop.go:952) и `compactIfNeeded()` (agent_impl.go:297) делают `sess.Reset()` — полностью стирают историю, затем вручную добавляют `<<CONVERSATION COMPACTED>>` + summary + tail.
- **DONE**: переход на модель opencode. `applyOpenCodeCompactResult` → `sess.MarkCompaction(tailStartID, summary)`; `compactIfNeeded` → `sess.MarkCompaction(...)`. `session.MarkCompaction` помечает head как `compacted`, сохраняет `tail_start_id`, добавляет маркер+summary, НЕ сбрасывает историю. `GetContextMessages`/`FilterCompacted` переупорядочивают в `[system, pinned..., compaction-user, summary, ...tail...]`. `restoreSessionMessages` (оркестратор) восстанавливает метаданные через `RestoreMessages`. `session_store.go`/`store.MessageData` персистят `Summary`/`Compacted`/`TailStartID` в БД.
- **DONE**: маркер компактизации рендерится модели как в opencode (`message-v2.ts:239`): `tokenizers.CompactionUserMessage = "What did we do so far? Respond in the same language as the conversation."` вместо технического `<<CONVERSATION COMPACTED>>`. `userTurns`/`withoutCompactionPairs` по-прежнему пропускают маркер по этой константе. Внимание: старые сессии в БД содержат текст `<<CONVERSATION COMPACTED>>` — для них детекция маркера по константе не сработает (среда разработки, сессии эфемерные).

### 2. Сохранение `tail_start_id` и корректное переупорядочивание при многократной компактизации
- **opencode**: compaction-part хранит `tail_start_id`; `filterCompacted()` использует его для восстановления порядка `[compaction, summary, tail...]`. При повторной компактизации `completedCompactions()` находит предыдущий summary и скрывает его (hidden set).
- **агент**: `FilterCompacted()` (filter.go:33) находит маркер по `Summary=true` и строит результат вручную; `tail_start_id` не хранится в сессии — только вычисляется на лету. При повторной компактизации предыдущий summary-оборот остаётся в истории и снова попадает в выбор.
- **DONE**: `Message.TailStartID`/`Message.Compacted` хранятся в сессии, файловом сторе и SQLite (`store.MessageData` + колонки `summary`/`compacted`/`tail_start_id` в `messages` + миграция `ensureColumnTyped` для существующих БД). `FilterCompacted`/`GetContextMessages` используют сохранённый `TailStartID`. При повторной компактизации `CompactWithOpenCode` работает по сырой истории (индексы совпадают с `session.messages`), `withoutCompactionPairs` убирает старые пары «маркер+summary» из head, `findPreviousSummary` возвращает последний summary. Защищено тестами `TestRepeatedCompaction_*` и `TestMessageCRUD_CompactionMetadata`.

### 3. Selection head/tail должен точно повторять opencode `select()`
- **opencode** (compaction.ts:198 `select`): идёт от самых новых оборотов к старым, копит в бюджет `preserveRecentBudget` (default `min(8000, max(2000, usable*0.25))`), использует `tail_turns` (default 2). Если оборот не влезает — `splitTurn()` делит его, сохраняя хвост оборотa. Если `keep.start === 0` — ничего не компактируем.
- **агент**: `SelectMessages()` (opencode_compaction.go:145) повторяет логику, но:
  - возвращает `TailStartID` как **индекс**, а не ID сообщения;
  - при `keepStart == 0` возвращает `Head: nil, TailStartID: 0` — opencode в этом случае оставляет всё как есть (`head: messages, tail_start_id: undefined`), а не помечает всё в хвост.
- **DONE**: семантика `keepStart == 0` выровнена: `SelectMessages` возвращает `Head: messages, TailStartID: -1` (компактится всё, хвост не сохраняется) — как `head: messages, tail_start_id: undefined` в opencode. Индекс в сырой истории — стабильный ID (сообщения только добавляются), поэтому используется как аналог `tail_start_id`. Обновлены тесты `TestSelectMessages`, `TestScenario_MaxTailTurns`, `TestScenario_EmptyMessages`.
- **DONE**: `Head = messages[:TailStartID]` (граница = первый user ≥ keepStart) — при `splitTurn` (keepStart на не-user сообщении) остаток оборота попадает в head, а не выпадает из head и tail (потеря данных). Защищено `TestSelectMessages/split_turn_keeps_no_gap_between_head_and_tail`.

### 4. SplitTurn / оценка размера оборота
- **opencode**: `splitTurn()` итеративно от `start = turn.start+1` ищет сообщение, от которого хвост влезает в бюджет; использует реальную оценку токенов `Token.estimate(JSON.stringify(msgs))` (символы/4).
- **агент**: `splitTurn()` (opencode_compaction.go:127) аналогичен, но оценка `estimateMessagesTokens` = `EstimateMessagesTokensSimple` — эвристика с codeFactor 1.3 и структурным бонусом, а не `len/4`.
- **DONE**: унифицировано (см. п. 9) — `EstimateTokensSimple` = `len/4` (как opencode `Token.estimate`), `splitTurn`/`select` используют ту же оценку, что overflow и prune.

### 5. Промпт суммаризации должен быть как `buildPrompt()` opencode
- **opencode** (core/src/session/compaction.ts:167): `[previousSummary ? "Update the anchored summary..." : "Create new...", SUMMARY_TEMPLATE, ...context]`, где `context = [previousSummary.recent, selected.head]`. При этом **head и previous-summary передаются отдельно**, а не склеиваются с историей.
- **агент**: `BuildSummaryPrompt()` (opencode_compaction.go:209) вставляет **всю head-историю** в виде `[role]: content` перед инструкцией. Это не соответствует opencode: head должен идти как отдельные сообщения, а не как текст промпта.
- **DONE**: `BuildSummaryPrompt(previousSummary, context []string)` = `[инструкция, SUMMARY_TEMPLATE, ...context]` (как opencode buildPrompt). Head передаётся отдельными сообщениями: `summarizeChunk` шлёт `[system, ...chunk, user(prompt)]`. `LLMCompressor.buildCompressionUserPrompt` флэттенит их в user-промпт — head больше не дублируется в тексте инструкции.

### 6. Передача `previousSummary.recent` (последних сообщений) в контекст суммаризации
- **opencode**: в `compactAfterOverflow` (core compaction.ts:185) в контекст попадает `previousSummary.recent` — последние сообщения до компактизации, чтобы сумма их не потеряла. Также при повторной компактизации `previousSummary` обновляется и передаётся в новый вызов.
- **агент**: `findPreviousSummary()` (opencode_compaction.go:400) возвращает только текст последнего summary, без `recent`. При повторной компактизации recent не передаётся.
- **DONE**: `findPreviousCompaction` возвращает summary + recent (`messages[tailStartID:markerIdx]` — хвост, сохранённый предыдущей компактизацией). `summarizeHead`/`summarizeChunk` включают recent в контекст первого вызова суммаризации. Защищено `TestCompactWithOpenCode_IncludesPreviousRecent`. Замечание: recent всегда входит в новый head (хвост хранится дословно), поэтому это дублирование — как в opencode core (context = [prev.recent, head]).

### 7. Автоматическое продолжение после компактизации
- **opencode**: после компактизации при `input.auto && result === "continue"` создаётся synthetic user-сообщение "Continue if you have next steps, or stop..." (compaction.ts:444-526) и цикл продолжается автоматически.
- **агент**: после `checkAndCompressOpenCode()` обработка текущего сообщения продолжается без авто-сообщения "continue". Есть ли запрос на продолжение — не проверяется.
- **TODO**: добавить авто-продолжение (synthetic user-сообщение) после авто-компактизации.

### 8. Обработка overflow при компактизации
- **opencode**: если компактизация не вписалась в контекст — `result === "compact"` повторно вызывает компактизацию; если не получается — помечает ошибку `ContextOverflowError`, завершает (compaction.ts:426-435). Также есть `replay` предыдущего user-сообщения при overflow (compaction.ts:320-336).
- **агент**: `CompactWithOpenCode()` при ошибке суммаризации просто логирует warning и возвращает (agentloop.go:897-902) — без fallback и без повтора.
- **TODO**: добавить обработку повторного overflow после компактизации и fallback.

### 9. Единая оценка токенов
- **opencode**: везде `Token.estimate = len(chars)/4`, включая `select()` и проверку `isOverflow`.
- **агент**: смесь: `EstimateMessagesTokensSimple` (с codeFactor/структурным бонусом) для выбора и эвристика для overflow; есть `RealEstimator` (реальный токенизатор) и `IsOverflowWithProviderTokens`, но в agentloop/agent используется `EstimateMessagesTokensSimple`.
- **DONE**: единая стратегия opencode-style `len/4`: `EstimateTokensSimple = ceil(len/4)` (убран codeFactor 1.3 и структурный бонус). `select`/`splitTurn`/`takeOldestFit`/overflow-проверка и `prune` используют одну и ту же оценку. `RealEstimator` остаётся для точных замеров (provider tokens), но production-путь единый.

### 10. Prune (вычистка больших tool-выводов) — как в opencode
- **opencode**: `prune()` (compaction.ts:253) ходит назад, защищает последние 2 оборота, сохраняет `PRUNE_PROTECT=40000` токенов, стирает вывод старших tool-частей (отмечает `time.compacted`), защищённые tools = `["skill"]`, порог срабатывания `PRUNE_MINIMUM=20000`.
- **агент**: `PruneMessages()` (pruning.go:22) аналогичен, но:
  - нет защищённых инструментов (`PRUNE_PROTECTED_TOOLS=["skill"]`);
  - вместо обновления состояния части (`time.compacted`) подменяет контент на placeholder `[Old tool result content cleared]` и пересобирает сессию через `sess.Reset()`.
- **DONE**: добавлен `PRUNE_PROTECTED_TOOLS = ["skill"]` — защищённые инструменты пропускаются ДО проверки compacted (как opencode). `PruneMessages(messages, protectedTools...)`, вызов из runPruning. Защищено `TestScenario_PruningProtectedTools`.
- **DONE (частично)**: `runPruning` больше не пересобирает сессию через `sess.Reset()` — `PruneMessages` работает по сырой истории (индексы совпадают с `session.messages`), а обрезка применяется на месте через `sess.MarkMessageCompacted(i, PRUNED_OUTPUT_PLACEHOLDER)`. История, маркеры компактизации и `tail_start_id` остаются нетронутыми. **Важно**: apply-цикл применяет placeholder только к новым обрезкам (`pruned[i].Compacted && !raw[i].Compacted`) — иначе уже compacted-сообщения (head, предыдущий summary) затирались бы плейсхолдером. Защищено `TestRunPruning_PreservesCompactedHead`.

### 11. Tool output truncation в контексте модели
- **opencode**: `toModelMessagesEffect()` обрезает вывод tool до `TOOL_OUTPUT_MAX_CHARS=2000` при каждом формировании запроса (message-v2.ts:306, compaction.ts:363-375).
- **агент**: `TruncateToolOutput()` обрезает **при записи** в сессию (tool_result_processor.go:103,377), но не при формировании запроса. Различие: в opencode truncation происходит на этапе рендера, а не на записи.
- **TODO**: рассмотреть перенос truncation на этап построения API-сообщений (чтобы повторные запросы тоже обрезались).
- **DONE**: truncation перенесён на этап рендера — `convertHistoryToAPIMessages()` (agent_impl.go) и `buildAPIMessages()` (agentloop.go) обрезают большой tool-вывод через `TruncateToolOutput()` при формировании каждого запроса. В сессии хранятся полные выводы (инструменты сохраняют полный файл с хинтом "Full output saved to:" для перечитывания). Защищено `TestConvertHistoryToAPIMessages_TruncatesLargeToolOutput` и `TestBuildAPIMessages_TruncatesLargeToolOutput`.

### 12. Системный промпт и AGENTS.md при компактизации
- **opencode**: system prompt (env + instructions + skills) собирается динамически при каждом запросе (prompt.ts:1333) и **не является частью истории**; при компактизации он не теряется.
- **агент**: системный промпт хранится первым сообщением в истории; после `sess.Reset()` восстанавливается из `config.SystemPrompt`, а AGENTS.md добавляется отдельным system-сообщением через `injectInstructions()` после компактизации.
- **TODO**: убедиться, что после компактизации system prompt и инструкции (AGENTS.md/CLAUDE.md) восстанавливаются так же надёжно, как в opencode.
- **DONE**: system-сообщение не компактится (`MarkCompaction` пропускает `SystemRole`), `injectInstructions()` вызывается при каждом построении запроса и перечитывает AGENTS.md/CLAUDE.md из config и project директорий. Защищено `TestSystemPromptAndInstructionsSurviveCompaction`.

---

## Специфика агента (сохранить)

### 13. `/pin` — pinned-промпты должны всегда идти в начале после компактизации
- **opencode**: команды `/pin` нет; инструкции всегда в system (собираются каждый раз).
- **агент**: `GetContextMessages()` (session.go:369) вставляет pinned-промпты после system-сообщения, в самое начало контекста. При `Reset()` (компактизация) `s.pinned` сохраняется, поэтому после компактизации pinned снова добавляются в начало. **Это исключение из поведения opencode — должно сохраняться.**
- **TODO**: зафиксировать это поведение тестами: после компактизации pinned-промпты обязаны оказаться в начале результирующего контекста (до summary/tail).

---

## Приоритеты

| Приоритет | Пункт | Что даёт | Статус |
|-----------|-------|----------|--------|
| P0 | 1, 2, 3 | Core-логика выбора head/tail и переупорядочивания = базовое совпадение с opencode | DONE |
| P0 | 5, 6 | Качество summary (правильная структура промпта, сохранение recent) | DONE |
| P1 | 7, 8 | Устойчивость: авто-продолжение и fallback при overflow | TODO |
| P1 | 9, 10, 11 | Консистентность оценок и работы с tool-выводами | DONE |
| P2 | 4, 12 | Точность границ оборота и восстановление system-контекста | DONE |
| P2 | 13 | Подтверждение поведения `/pin` тестами (обязательно к сохранению) | DONE (тесты есть) |

---

## Референсы
- opencode: `packages/opencode/src/session/compaction.ts`
- opencode: `packages/core/src/session/compaction.ts` (`buildPrompt`, `compactAfterOverflow`)
- opencode: `packages/opencode/src/session/overflow.ts` (`usable`, `isOverflow`)
- opencode: `packages/opencode/src/session/message-v2.ts` (`filterCompacted`, `toModelMessagesEffect`)
- агент: `pkg/compress/opencode_compaction.go`, `pkg/compress/filter.go`, `pkg/compress/pruning.go`, `pkg/compress/overflow.go`
- агент: `pkg/agentloop/agentloop.go` (`checkAndCompressOpenCode`, `applyOpenCodeCompactResult`), `pkg/agent/agent_impl.go` (`compactIfNeeded`), `session/session.go` (`GetContextMessages`, `/pin`)
