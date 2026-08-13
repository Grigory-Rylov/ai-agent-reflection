package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/debug"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/instructions"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/prompt"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

// Aliases for tools from tools package
type FileReadTool = tools.FileReadTool
type FileWriteTool = tools.FileWriteTool
type TimeGetTool = tools.TimeGetTool
type DirListTool = tools.DirListTool
type ShellExecuteTool = tools.ShellExecuteTool
type WebFetchTool = tools.WebFetchTool
type WebSearchTool = tools.WebSearchTool
type GlobTool = tools.GlobTool
type GrepTool = tools.GrepTool
type CalcTool = tools.CalcTool
type EditTool = tools.EditTool
type ApplyPatchTool = tools.ApplyPatchTool
type QuestionTool = tools.QuestionTool

// ============================================================
// AI Agent Implementation — реализация агента с подключением к llama-server
// ============================================================

// ThinkingCallback callback для отправки thinking сообщений
type ThinkingCallback func(peerID int64, content string) error

// agentImpl реализует интерфейс AI агента с подключением к llama-server
type agentImpl struct {
	config            Config
	sessions          map[int64]*session.Session
	toolsRegistry     *tools.Registry
	mu                sync.RWMutex
	client            *http.Client
	compactor         *compress.Compactor
	systemPrompt      string                   // системный промпт из файла или дефолтный
	thinkingCallback  ThinkingCallback         // callback для отправки thinking сообщений
	toolSchemas       []map[string]interface{} // схемы инструментов, переданные извне
	toolExecutor      ToolExecutor             // кастомный executor (для тестов через StubToolExecutor)
	debugLog          debug.Logger             // логгер для отладочных сообщений
	permissionChecker PermissionChecker        // проверка разрешений для инструментов
}

// PermissionChecker проверяет разрешения на выполнение инструментов
type PermissionChecker interface {
	Check(toolName string) string                    // "allow", "deny", "ask"
	Evaluate(permission, pattern string) string      // "allow", "deny", "ask" по правилам
	Approve(permission, pattern string)              // добавить правило allow
}
// ============================================================
// Инициализация
// ============================================================

// NewAgent создаёт новый AI Agent
func NewAgent(config Config) *agentImpl {
	agent := &agentImpl{
		config:       config,
		sessions:     make(map[int64]*session.Session),
		toolsRegistry: tools.NewRegistry(),
		client: &http.Client{
			Timeout: 2 * time.Hour,
			Transport: &http.Transport{
				DisableKeepAlives: true,
			},
		},
		debugLog: debug.NewLogger(config.Debug),
	}

	// Загружаем системный промпт из файла или используем дефолтный
	agent.loadSystemPrompt()

	// Регистрируем инструменты по умолчанию если включены
	if config.EnableTools {
		agent.registerDefaultTools()
	}

	// Инициализируем компактор для opencode-style компакции
	if config.EnableCompression {
		agent.initCompactor()
	}

	return agent
}

// loadSystemPrompt загружает системный промпт из шаблонов или файла
func (a *agentImpl) loadSystemPrompt() {
	defaultPrompt := "You are a helpful assistant."

	// Пробуем использовать TemplateEngine если указана директория с шаблонами
	if a.config.PromptsDir != "" {
		a.loadFromTemplates()
		if a.systemPrompt != "" {
			return
		}
	}

	// Fallback: читаем из файла
	if a.config.SystemPromptFile != "" {
		data, err := os.ReadFile(a.config.SystemPromptFile)
		if err == nil && strings.TrimSpace(string(data)) != "" {
			a.systemPrompt = strings.TrimSpace(string(data))
			a.debugLog.Info("Loaded system prompt from '%s' (%d bytes)",
				a.config.SystemPromptFile, len(a.systemPrompt))
			return
		}
	}

	a.systemPrompt = defaultPrompt
}

func (a *agentImpl) loadFromTemplates() {
	engine := prompt.NewEngine(a.config.PromptsDir)

	provider := prompt.DetectProvider(a.config.Model)
	toolNames := a.config.ToolsList
	if toolNames == nil {
		toolNames = extractToolNames(a.toolsRegistry.GetAll())
	}

	result, err := engine.Resolve(prompt.Config{
		Model:       a.config.Model,
		Provider:    provider,
		WorkingDir:  tools.WorkingDir,
		Tools:       toolNames,
		MaxTokens:   a.config.MaxTokens,
		Temperature: a.config.Temperature,
		Mode:        a.config.Mode,
	})
	if err != nil {
		a.debugLog.Warn("Template resolution failed: %v, falling back", err)
		return
	}

	a.systemPrompt = result
	a.debugLog.Info("Loaded system prompt from templates (%s, %d chars)",
		provider, len(result))
}

func extractToolNames(toolList []tools.Tool) []string {
	if toolList == nil {
		return nil
	}
	names := make([]string, 0, len(toolList))
	for _, t := range toolList {
		names = append(names, t.Name())
	}
	return names
}

// GetSystemPrompt возвращает системный промпт
func (a *agentImpl) GetSystemPrompt() string {
	return a.systemPrompt
}

// initCompactor инициализирует компактор для opencode-style компакции
func (a *agentImpl) initCompactor() {
	compressor := compress.NewLLMCompressor(a.config.LlamaServerURL, a.config.Model, a.config.Temperature)
	a.compactor = compress.NewCompactor(compressor)
}

// registerDefaultTools регистрирует инструменты по умолчанию
func (a *agentImpl) registerDefaultTools() {
	a.toolsRegistry.Register(&FileReadTool{})
	a.toolsRegistry.Register(&FileWriteTool{})
	a.toolsRegistry.Register(&TimeGetTool{})
	a.toolsRegistry.Register(&DirListTool{})
	a.toolsRegistry.Register(&ShellExecuteTool{})
	a.toolsRegistry.Register(&WebFetchTool{})
	a.toolsRegistry.Register(&WebSearchTool{})
	a.toolsRegistry.Register(&GlobTool{})
	a.toolsRegistry.Register(&GrepTool{})
	a.toolsRegistry.Register(&CalcTool{})
	a.toolsRegistry.Register(&EditTool{})
	a.toolsRegistry.Register(&ApplyPatchTool{})
	a.toolsRegistry.Register(&QuestionTool{})
}

// RegisterTools регистрирует инструменты из внешнего реестра
func (a *agentImpl) RegisterTools(registry *tools.Registry) {
	if registry == nil {
		return
	}
	for _, tool := range registry.GetAll() {
		if !a.toolsRegistry.IsRegistered(tool.Name()) {
			a.toolsRegistry.Register(tool)
		}
	}
	// toolSchemas должен отражать весь накопленный реестр, а не затираться
	// схемами последнего вызова. Иначе агент, которому инструменты регистрируют
	// несколькими вызовами (основные инструменты + task-инструмент, как воркер
	// в multi-agent режиме), видел в LLM только последний набор.
	a.toolSchemas = a.toolsRegistry.ToOpenAISchema()
}

// ReplaceTools replaces the entire tools registry and schemas
func (a *agentImpl) ReplaceTools(registry *tools.Registry) {
	if registry == nil {
		return
	}
	a.toolsRegistry = registry
	a.toolSchemas = registry.ToOpenAISchema()
}
// ============================================================
// Методы Agent Interface
// ============================================================

// ProcessMessage обрабатывает сообщение пользователя и возвращает ответ
func (a *agentImpl) ProcessMessage(ctx context.Context, message string, peerID int64) (string, error) {
	a.debugLog.Debug("ProcessMessage called: peerID=%d, message=%q, tools=%d", peerID, message, len(a.toolsRegistry.GetAll()))

	// Если контекст отменён (/clear, таймаут, etc) — не модифицируем сессию.
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	// Получаем или создаём сессию
	s := a.getSession(peerID)

	// Проверяем, не зациклилась ли AI
	if s.IsLoopDetected() {
		alert := s.GetLoopAlertMessage()
		if alert != "" {
			message = "[LOOP DETECTED] " + alert + "\n\n" + message
		}
	}

	// Добавляем сообщение в сессию, если его там ещё нет.
	// В обычном потоке (через agentloop) сообщение уже добавлено в сессию
	// и сохранено в файл. При прямом вызове (через Orchestrator) добавляем здесь.
	history := s.GetHistory()
	if len(history) == 0 || history[len(history)-1].Role != session.UserRole || history[len(history)-1].Content != message {
		s.AddUserMessage(message)
		history = s.GetHistory()
	}

	// Проверяем и при необходимости сжимаем контекст (opencode-style)
	if a.compactor != nil {
		a.compactIfNeeded(ctx, s, true)
		history = s.GetHistory()
	}

	// Формируем сообщения для API (включая pinned промпты в начале)
	apiMessages := a.convertHistoryToAPIMessages(s.GetContextMessages())

	// Добавляем AGENTS.md/CLAUDE.md из рабочей директории (как в opencode)
	// отдельным system-сообщением после основного системного промпта
	workingDir := s.GetWorkingDir()
	if workingDir == "" {
		workingDir = tools.WorkingDir
	}
	apiMessages = a.injectInstructions(apiMessages, workingDir)

	// Перед LLM-запросом проверяем, не отменён ли контекст
	// (защита от гонки с /clear после модификации сессии).
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Проверяем, нужно ли использовать инструменты
	if a.config.EnableTools {
		// Используем function calling с инструментами
		result, err := a.processWithTools(ctx, apiMessages, s)
		if err != nil {
			return "", fmt.Errorf("process with tools: %w", err)
		}
		return result.Response, nil
	}

	// Обычный streaming запрос без инструментов
	return a.processStreaming(ctx, apiMessages, s)
}

// compactIfNeeded выполняет opencode-style компакцию при переполнении контекста.
// addAutoContinue — если true, добавляет CompactionAutoContinueText после успешной
// компактизации (для ProcessMessage path); если false — не добавляет (tool loop).
// Возвращает true если компактизация успешно выполнена (summary не пустой).
func (a *agentImpl) compactIfNeeded(ctx context.Context, s *session.Session, addAutoContinue bool) bool {
	history := s.GetHistory()

	visible := a.convertSessionHistory(history)
	tokensBefore := compress.EstimateMessagesTokensSimple(visible)
	if !compress.IsOverflowWithLimits(tokensBefore, a.config.MaxTokens, a.config.ModelLimitInput, a.config.CompactionReserved) {
		return false
	}

	tailTurns := a.config.TailTurns
	if tailTurns <= 0 {
		tailTurns = 2
	}

	result, err := a.compactor.CompactWithOpenCode(ctx, a.convertSessionHistoryRaw(history), a.config.MaxTokens, tailTurns, a.config.PreserveRecentTokens)
	if err != nil {
		a.debugLog.Warn("LLM compaction failed: %v, falling back to aggressive head pruning", err)

		fbResult := a.compactionFallback(history, tailTurns, a.config.MaxTokens)
		if fbResult != nil {
			a.markCompactedHead(s, fbResult.TailStartID)
			s.MarkCompaction(fbResult.TailStartID, compactionFallbackSummary)
		}
		return false
	}

	if result.Summary == "" {
		return false
	}

	s.MarkCompaction(result.TailStartID, result.Summary)

	if addAutoContinue && shouldAddAutoContinue(s) {
		s.AddUserMessage(tokenizers.CompactionAutoContinueText)
	}

	return true
}

// markCompactedHead помечает головные сообщения сессии как compacted.
func (a *agentImpl) markCompactedHead(s *session.Session, tailStartID int) {
	for i := 0; i < tailStartID && i < len(s.GetHistory()); i++ {
		msg := s.GetHistory()[i]
		if msg.Role != session.SystemRole {
			s.MarkMessageCompacted(i, compress.PRUNED_OUTPUT_PLACEHOLDER)
		}
	}
	a.debugLog.Info("Compaction fallback: marked %d head messages as compacted", tailStartID)
}

// compactionFallbackSummary — placeholder summary когда LLM-суммаризация не удалась.
const compactionFallbackSummary = "## Goal\n- [context compacted — summary unavailable]\n\n## Constraints & Preferences\n- (none)\n\n## Progress\n### Done\n- (compact failed)\n\n### In Progress\n- (truncated)\n\n### Blocked\n- context overflow during summarization\n\n## Key Decisions\n- (lost during compaction fallback)\n\n## Next Steps\n- continue current task\n\n## Critical Context\n- [compaction summary could not be generated]\n\n## Relevant Files\n- (none)"

// convertSessionHistory конвертирует историю сессии в tokenizers.Message
// Tool call аргументы добавляются к контенту для корректной оценки токенов
// (в opencode оценивается JSON.stringify всего request, включая tool calls)
// После конвертации применяет FilterCompacted для корректного порядка
// сообщений после компактизации: [compaction-user, summary, tail, after-summary]
func (a *agentImpl) convertSessionHistory(history []session.Message) []tokenizers.Message {
	return compress.FilterCompacted(a.convertSessionHistoryRaw(history))
}

// convertSessionHistoryRaw конвертирует историю 1:1 (без FilterCompacted),
// сохраняя выравнивание индексов с session.messages для TailStartID.
func (a *agentImpl) convertSessionHistoryRaw(history []session.Message) []tokenizers.Message {
	messages := make([]tokenizers.Message, len(history))
	for i, msg := range history {
		content := msg.Content
		// Добавляем содержимое tool calls к оценке токенов
		for _, tc := range msg.ToolCalls {
			content += tc.Function.Arguments
		}
		messages[i] = tokenizers.Message{
			Role:        string(msg.Role),
			Content:     content,
			Summary:     msg.Summary,
			Compacted:   msg.Compacted,
			TailStartID: msg.TailStartID,
		}
	}
	return messages
}

// ResetSession сбрасывает сессию пользователя
func (a *agentImpl) ResetSession(peerID int64) {
	s := a.getSession(peerID)
	s.Reset()
}

// compactionFallback возвращает SelectResult для агрессивного проранинга head,
// когда LLM-суммаризация не уместилась в контекст. Использует тот же select(),
// чтобы сохранить tail и максимально сократить head.
func (a *agentImpl) compactionFallback(history []session.Message, tailTurns int, maxTokens int) *compress.SelectResult {
	if len(history) == 0 {
		return nil
	}

	raw := a.convertSessionHistoryRaw(history)
	budget := compress.PreserveRecentBudget(maxTokens, a.config.PreserveRecentTokens)
	selected := compress.SelectMessages(raw, tailTurns, budget)

	if selected.TailStartID <= 0 || len(selected.Head) == 0 {
		return nil
	}

	return &selected
}

// GetSession возвращает сессию пользователя
func (a *agentImpl) GetSession(peerID int64) *session.Session {
	return a.getSession(peerID)
}

// SetThinkingCallback устанавливает callback для отправки thinking сообщений
func (a *agentImpl) SetThinkingCallback(cb ThinkingCallback) {
	a.thinkingCallback = cb
}

// SetTools регистрирует инструменты, переданные из agentloop
func (a *agentImpl) SetTools(toolSchemas []map[string]interface{}) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toolSchemas = toolSchemas
}

// SetToolExecutor устанавливает кастомный executor для инструментов
func (a *agentImpl) SetToolExecutor(executor ToolExecutor) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toolExecutor = executor
}

// SetPermissionChecker устанавливает проверку разрешений
func (a *agentImpl) SetPermissionChecker(checker PermissionChecker) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.permissionChecker = checker
}

// ============================================================
// Управление сессиями
// ============================================================

// getSession возвращает или создаёт сессию для пользователя
func (a *agentImpl) getSession(peerID int64) *session.Session {
	a.mu.RLock()
	s, exists := a.sessions[peerID]
	a.mu.RUnlock()

	if !exists {
		a.mu.Lock()
		// Double-check после получения write-lock
		s, exists = a.sessions[peerID]
		if !exists {
			config := a.config.SessionConfig
			config.PeerID = peerID
			config.SystemPrompt = a.systemPrompt
			s = session.NewSession(config)
			a.sessions[peerID] = s

			// Восстанавливаем workingDir из стора в глобальную переменную
			if wd := s.GetWorkingDir(); wd != "" {
				tools.SetWorkingDir(wd)
			}
		}
		a.mu.Unlock()
	}

	return s
}

// ============================================================
// Streaming без инструментов
// ============================================================

// processStreaming обрабатывает streaming запрос без инструментов
func (a *agentImpl) processStreaming(ctx context.Context, messages []Message, session *session.Session) (string, error) {
	streamConfig := StreamingConfig{
		Model:       a.config.Model,
		MaxTokens:   a.config.MaxTokens,
		Temperature: a.config.Temperature,
		Stream:      true,
	}

	// Собираем ответ с reasoning (с бесконечным ретраем серверных ошибок)
	responseText, reasoningText, _, _, promptTokens, completionTokens, err := a.streamAndCollect(ctx, streamConfig, messages)
	if err != nil {
		return "", err
	}

	// Проверяем на XML tool calls в reasoning
	if reasoningText != "" {
		parsed := ParseXMLToolCalls(reasoningText)
		if len(parsed.ToolCalls) > 0 {
			// Есть XML tool calls - нужно переключиться на processWithTools
			result, err := a.processWithTools(ctx, messages, session)
			if err != nil {
				return "", err
			}
			return result.Response, nil
		}
	}

	// Отправляем очищенный reasoning в thinkingPeerID (без XML тегов)
	if reasoningText != "" && a.thinkingCallback != nil {
		cleanedReasoning := reasoningText
		parsed := ParseXMLToolCalls(reasoningText)
		if len(parsed.ToolCalls) > 0 {
			cleanedReasoning = parsed.Content
		}
		if cleanedReasoning != "" {
			if err := a.thinkingCallback(session.GetPeerID(), cleanedReasoning); err != nil {
				a.debugLog.Warn("Failed to send thinking message: %v", err)
			}
		}
	}

	// Отправляем количество токенов после ответа LLM
	a.sendThinkingTokens(session.GetPeerID(), promptTokens, completionTokens)

	// Если reasoning есть но response пустой — reasoning уже отправлен в thinking_peer_id
	// Не возвращаем его как обычный ответ
	if responseText == "" && reasoningText != "" {
		return "", nil
	}

	responseText = a.stripThinkingTags(responseText, session.GetPeerID())

	session.AddAssistantMessage(responseText)
	return responseText, nil
}

// injectInstructions добавляет содержимое AGENTS.md/CLAUDE.md (если найдено
// в рабочей директории или глобальной конфиг-директории) отдельным
// system-сообщением сразу после основного системного промпта.
func (a *agentImpl) injectInstructions(messages []Message, workingDir string) []Message {
	content := instructions.Build(workingDir)
	if content == "" {
		return messages
	}

	instrMsg := Message{Role: "system", Content: content}
	out := make([]Message, 0, len(messages)+1)
	inserted := false
	for _, m := range messages {
		out = append(out, m)
		if !inserted && m.Role == "system" {
			out = append(out, instrMsg)
			inserted = true
		}
	}
	if !inserted {
		out = append([]Message{instrMsg}, out...)
	}
	return out
}

// ============================================================
// Утилиты для конвертации
// ============================================================

// convertHistoryToAPIMessages конвертирует историю сессии в формат API
func (a *agentImpl) convertHistoryToAPIMessages(history []session.Message) []Message {
	apiMessages := make([]Message, len(history))
	for i, msg := range history {
		content := msg.Content
		if msg.Role == session.ToolRole {
			content = compress.TruncateToolOutput(content)
		}
		apiMsg := Message{
			Role:       string(msg.Role),
			Content:    content,
			ToolCallID: msg.ToolCallID,
			Name:       msg.Name,
		}
		// Конвертируем tool_calls если есть
		if len(msg.ToolCalls) > 0 {
			apiMsg.ToolCalls = make([]ToolCall, len(msg.ToolCalls))
			for j, tc := range msg.ToolCalls {
				apiMsg.ToolCalls[j] = ToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: ToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: json.RawMessage(tc.Function.Arguments),
					},
				}
			}
		}
		apiMessages[i] = apiMsg
	}
	return apiMessages
}
