package vk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/opencode/llama-client/pkg/agentloop"
	"github.com/opencode/llama-client/pkg/logger"
	"github.com/opencode/llama-client/pkg/modelsconfig"
	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/pkg/util"
	"github.com/opencode/llama-client/session"
)

func expandTilde(path string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			if path == "~" {
				return home
			}
			return home + path[1:]
		}
	}
	return path
}

type AgentOrchestrator interface {
	ExecuteTask(ctx context.Context, task string, peerID int64) (string, error)
	RunAgent(ctx context.Context, agentName, task string, peerID int64) (string, error)
	ListAgentNames() []string
	GetCurrentAgent() string
	GetActiveAgentSessions(peerID int64) (string, error)
	ClearActiveSessions(peerID int64)
}

type BotHandler struct {
	vkClient       *BotClient
	aiAgent        agentloop.AgentLoop
	orchestrator   AgentOrchestrator
	log            *logger.Logger
	sessions       map[int64]*session.Session
	sessionMu      sync.RWMutex
	mainPeerID     int64
	thinkingPeerID int64
	modelHolder    *modelsconfig.Holder
	cancelFuncs    map[int64]context.CancelFunc
	cancelMu       sync.RWMutex
	attachmentsDir string
	// peerProcessors — per-peer mutex для сериализации обработки сообщений.
	// Когда агент занят, новые сообщения от того же peerID встают в очередь и ждут
	// завершения текущей задачи без отмены контекста.
	peerProcessors     map[int64]*sync.Mutex
	peerProcessorsMu   sync.RWMutex
	// semaphore limits concurrent message processing goroutines.
	semaphore chan struct{}
}

const maxConcurrentHandlers = 10

func NewBotHandler(vkClient *BotClient, aiAgent agentloop.AgentLoop, log *logger.Logger) *BotHandler {
	return &BotHandler{
		vkClient:       vkClient,
		aiAgent:        aiAgent,
		log:            log,
		sessions:       make(map[int64]*session.Session),
		cancelFuncs:    make(map[int64]context.CancelFunc),
		peerProcessors: make(map[int64]*sync.Mutex),
		attachmentsDir: "./attachments",
		semaphore:      make(chan struct{}, maxConcurrentHandlers),
	}
}

func NewBotHandlerWithPeerID(vkClient *BotClient, aiAgent agentloop.AgentLoop, log *logger.Logger, mainPeerID, thinkingPeerID int64, orchestrator AgentOrchestrator, modelHolder *modelsconfig.Holder) *BotHandler {
	return &BotHandler{
		vkClient:       vkClient,
		aiAgent:        aiAgent,
		orchestrator:   orchestrator,
		log:            log,
		sessions:       make(map[int64]*session.Session),
		mainPeerID:     mainPeerID,
		thinkingPeerID: thinkingPeerID,
		modelHolder:    modelHolder,
		cancelFuncs:    make(map[int64]context.CancelFunc),
		peerProcessors: make(map[int64]*sync.Mutex),
		attachmentsDir: "./attachments",
		semaphore:      make(chan struct{}, maxConcurrentHandlers),
	}
}

func (h *BotHandler) agentNames() []string {
	if h.orchestrator != nil {
		names := h.orchestrator.ListAgentNames()
		if len(names) > 0 {
			return names
		}
	}
	return []string{"worker", "qa", "explore", "general", "agent", "coordinator"}
}

func ParseAgentHashMention(text string, knownNames []string) (agentName string, task string) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "#") {
		return "", text
	}
	spaceIdx := strings.Index(text, " ")
	if spaceIdx < 0 {
		return text[1:], ""
	}
	maybeName := text[1:spaceIdx]
	for _, name := range knownNames {
		if strings.EqualFold(maybeName, name) {
			return name, strings.TrimSpace(text[spaceIdx+1:])
		}
	}
	return "", text
}

func (h *BotHandler) ProcessMessage(message string, peerID int64) string {
	h.ensureSession(peerID)

	command := extractCommand(message)

	// Команды обрабатываются немедленно — без блокировки peer mutex'ом.
	if strings.HasPrefix(command, "/") {
		result := h.handleCommand(command, peerID)
		if result != "" {
			return result
		}
		if restarterCommands[extractBaseCommand(command)] {
			return ""
		}
		return fmt.Sprintf("Неизвестная команда: %s. Напишите /help для списка команд.", command)
	}

	mu := h.getPeerMutex(peerID)
	mu.Lock()
	defer mu.Unlock()

	h.clearCancelFunc(peerID)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.cancelMu.Lock()
	h.cancelFuncs[peerID] = cancel
	h.cancelMu.Unlock()
	defer h.clearCancelFunc(peerID)

	// Не-команды могут быть ответами на pending вопросы (права доступа, уточнения).
	if tools.HasPendingQuestion(peerID) {
		logger.DebugToFile("[ProcessMessage] HasPendingQuestion=true for peer %d, command=%s", peerID, util.Truncate(command, 100))
		if tools.ResolvePendingQuestion(peerID, command) {
			logger.DebugToFile("[ProcessMessage] Resolved pending question for peer %d with: %s", peerID, util.Truncate(command, 50))
			return ""
		} else {
			logger.DebugToFile("[ProcessMessage] ResolvePendingQuestion returned false for peer %d, command=%s", peerID, util.Truncate(command, 50))
		}
	}

	if agentName, task := ParseAgentHashMention(command, h.agentNames()); agentName != "" {
		if h.log != nil {
			h.log.InfoLogf("Agent #%s invoked by peer %d with task: %s", agentName, peerID, util.Truncate(task, 100))
		}

		if task == "" {
			return fmt.Sprintf("Укажите задачу для #%s. Например: #%s создай простой HTTP сервер", agentName, agentName)
		}

		if h.orchestrator != nil {
			response, err := h.orchestrator.RunAgent(ctx, agentName, task, peerID)
			if err != nil {
				if errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled") {
					return ""
				}
				if h.log != nil {
					h.log.ErrorLogf("Orchestrator error for #%s: %v", agentName, err)
				}
				return fmt.Sprintf("❌ Ошибка при выполнении задачи через #%s: %v", agentName, err)
			}
			return response
		}

		message = fmt.Sprintf("[Задача для #%s]\n\n%s", agentName, task)
	} else {
		s := h.aiAgent.GetSession(peerID)
		if s != nil && s.IsLoopDetected() {
			alert := s.GetLoopAlertMessage()
			if alert != "" {
				message = "[LOOP DETECTED] " + alert + "\n\n" + message
			}
		}

		response, err := h.aiAgent.ProcessMessage(ctx, message, peerID)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				if h.log != nil {
					h.log.InfoLogf("AI Agent request canceled for peer %d", peerID)
				}
				return ""
			}
			if h.log != nil {
				h.log.ErrorLogf("AI Agent error: %v", err)
			}
			return fmt.Sprintf("❌ Ошибка: %v", err)
		}

		return response
	}

	s := h.aiAgent.GetSession(peerID)
	if s != nil && s.IsLoopDetected() {
		alert := s.GetLoopAlertMessage()
		if alert != "" {
			message = "[LOOP DETECTED] " + alert + "\n\n" + message
		}
	}

	response, err := h.aiAgent.ProcessMessage(ctx, message, peerID)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			if h.log != nil {
				h.log.InfoLogf("AI Agent request canceled for peer %d", peerID)
			}
			return ""
		}
		if h.log != nil {
			h.log.ErrorLogf("AI Agent error: %v", err)
		}
		return fmt.Sprintf("❌ Ошибка: %v", err)
	}

	return response
}

// getPeerMutex возвращает mutex для сериализации обработки сообщений одного peerID.
// Если для peerID ещё нет мьютекса — создаёт новый. Обеспечивает безопасную работу
// нескольких goroutine через sync.Map паттерн с RWMutex.
func (h *BotHandler) getPeerMutex(peerID int64) *sync.Mutex {
	h.peerProcessorsMu.RLock()
	mu, ok := h.peerProcessors[peerID]
	h.peerProcessorsMu.RUnlock()
	if ok {
		return mu
	}

	h.peerProcessorsMu.Lock()
	defer h.peerProcessorsMu.Unlock()
	if mu, ok = h.peerProcessors[peerID]; ok {
		return mu
	}
	mu = &sync.Mutex{}
	h.peerProcessors[peerID] = mu
	return mu
}

func extractCommand(message string) string {
	message = strings.TrimSpace(message)

	if len(message) > 0 && message[0] == '[' {
		closeIdx := strings.Index(message, "]")
		if closeIdx > 0 && closeIdx < len(message)-1 {
			rest := strings.TrimSpace(message[closeIdx+1:])
			return rest
		}
	}

	return message
}

func (h *BotHandler) ProcessMessageWithTimeout(message string, peerID int64, timeout time.Duration) string {
	h.ensureSession(peerID)
	mu := h.getPeerMutex(peerID)

	command := extractCommand(message)

	if strings.HasPrefix(command, "/") {
		result := h.handleCommand(command, peerID)
		if result != "" {
			return result
		}
		if restarterCommands[extractBaseCommand(command)] {
			return ""
		}
		return fmt.Sprintf("Неизвестная команда: %s. Напишите /help для списка команд.", command)
	}

	mu.Lock()
	defer mu.Unlock()

	h.clearCancelFunc(peerID)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	h.cancelMu.Lock()
	h.cancelFuncs[peerID] = cancel
	h.cancelMu.Unlock()
	defer h.clearCancelFunc(peerID)

	if tools.HasPendingQuestion(peerID) {
		logger.DebugToFile("[ProcessMessageWithTimeout] HasPendingQuestion=true for peer %d, command=%s", peerID, util.Truncate(command, 100))
		if tools.ResolvePendingQuestion(peerID, command) {
			return ""
		}
	}

	s := h.aiAgent.GetSession(peerID)
	if s != nil && s.IsLoopDetected() {
		alert := s.GetLoopAlertMessage()
		if alert != "" {
			message = "[LOOP DETECTED] " + alert + "\n\n" + message
		}
	}

	response, err := h.aiAgent.ProcessMessage(ctx, message, peerID)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			if h.log != nil {
				h.log.InfoLogf("AI Agent request canceled for peer %d", peerID)
			}
			return ""
		}
		if h.log != nil {
			h.log.ErrorLogf("AI Agent error: %v", err)
		}
		return fmt.Sprintf("❌ Ошибка: %v", err)
	}

	return response
}

var restarterCommands = map[string]bool{
	"/update":  true,
	"/b":       true,
	"/restart": true,
}

func (h *BotHandler) handleCommand(input string, peerID int64) string {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return ""
	}
	cmd := parts[0]

	switch cmd {
	case "/clear":
		return h.handleClear(peerID)

	case "/sessions":
		return h.handleSessions(peerID)

	case "/newsession", "/n":
		return h.handleNewSession(input, peerID)

	case "/help":
		knownNames := h.agentNames()
		helpStr := "Доступные команды:\n" +
			"/clear - Очистить историю диалога (рабочая директория сохраняется)\n" +
			"/newsession [path] (/n) - Сбросить сессию и сменить рабочую директорию\n" +
			"/help - Показать эту справку\n" +
			"/status - Показать статус агента (сообщения, символы, токены)\n" +
			"/test-llama - Тест соединения с llama-server\n" +
			"/m, /models - Список доступных моделей\n" +
			"/r [alias] - Переключить текущую модель\n" +
			"/pin <промпт> - Закрепить промпт (переживает компактизацию) и выполнить его\n" +
			"/restart - Перезапустить агента без пересборки\n" +
			"/update - git pull, пересобрать и перезапустить агента\n" +
			"/agent [задача] - Запустить AI Agent для исследования проекта\n\n" +
			"Перенаправление задачи агенту через #:\n"
		for _, name := range knownNames {
			helpStr += fmt.Sprintf("#%s, ", name)
		}
		helpStr = strings.TrimSuffix(helpStr, ", ")
		helpStr += " — доступные роли"
		return helpStr

	case "/test-llama":
		return h.handleTestLlama()

	case "/status":
		h.aiAgent.EnsureSession(peerID)
		s := h.aiAgent.GetSession(peerID)
		status := "AI Agent активен и готов к работе."
		if s != nil {
			status += "\nPeer ID: " + fmt.Sprintf("%d", peerID) +
				"\nСообщений: " + fmt.Sprintf("%d", s.HistoryLength()) +
				"\nРабочая директория: " + s.GetWorkingDir()
		}
		chars, tokens, err := h.aiAgent.GetContextStats(peerID)
		if err == nil {
			status += "\nСимволов в контексте: " + fmt.Sprintf("%d", chars) +
				"\nТокенов в контексте: " + fmt.Sprintf("%d", tokens)
		}
		if h.modelHolder != nil {
			alias, modelName, host := h.modelHolder.GetCurrent()
			status += "\nМодель: " + alias + " (" + modelName + ")"
			status += "\nСервер: " + host
		}
		if h.orchestrator != nil {
			agentName := h.orchestrator.GetCurrentAgent()
			if agentName != "" {
				status += "\nРежим: агентов — активен: " + agentName
			} else {
				status += "\nРежим: агентов (ожидание)"
			}
		} else {
			status += "\nРежим: обычный"
		}
		return status

	case "/agent":
		return h.handleAgentCommand(input, peerID)

	case "/pin":
		return h.handlePinCommand(input, peerID)

	case "/m", "/models":
		return h.handleModelsList()

	case "/r":
		return h.handleModelSwitch(input)

	default:
		return ""
	}
}

func (h *BotHandler) handleModelsList() string {
	if h.modelHolder == nil {
		return "Модели не настроены (models.json не загружен)"
	}

	models := h.modelHolder.List()
	currentAlias := h.modelHolder.GetDefaultAlias()

	var b strings.Builder
	b.WriteString("Доступные модели:\n")
	for alias, entry := range models {
		mark := " "
		if alias == currentAlias {
			mark = "✓"
		}
		b.WriteString(fmt.Sprintf("  %s %s → %s (%s)\n", mark, alias, entry.Name, entry.Host))
	}
	b.WriteString(fmt.Sprintf("\nТекущая: %s\n", currentAlias))
	b.WriteString("Используйте /r [alias] для переключения.")

	return b.String()
}

func (h *BotHandler) handleModelSwitch(input string) string {
	if h.modelHolder == nil {
		return "Модели не настроены (models.json не загружен)"
	}

	parts := strings.SplitN(input, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return "Укажите алиас модели. Пример: /r gemma-4\n" +
			"Список моделей: /m"
	}

	alias := strings.TrimSpace(parts[1])
	if err := h.modelHolder.Switch(alias); err != nil {
		return fmt.Sprintf("Ошибка: %v", err)
	}

	alias2, modelName, host := h.modelHolder.GetCurrent()
	return fmt.Sprintf("✓ Модель переключена на: %s\n  %s (%s)", alias2, modelName, host)
}

func (h *BotHandler) handlePinCommand(input string, peerID int64) string {
	parts := strings.SplitN(input, " ", 2)
	content := ""
	if len(parts) > 1 {
		content = strings.TrimSpace(parts[1])
	}

	s := h.aiAgent.EnsureSession(peerID)
	if s == nil {
		return "Ошибка: не удалось получить сессию."
	}

	switch {
	case content == "clear":
		s.ClearPinned()
		if h.log != nil {
			h.log.InfoLogf("User %d cleared pinned prompts", peerID)
		}
		return "Pinned промпты удалены."

	case content == "":
		pinned := s.GetPinned()
		if len(pinned) == 0 {
			return "Pinned промптов нет. Используйте /pin <промпт> чтобы закрепить промпт, который переживёт компактизацию."
		}
		var b strings.Builder
		b.WriteString("Закреплённые промпты (/pin):\n")
		for i, p := range pinned {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, p))
		}
		b.WriteString("\n/pin <промпт> — добавить, /pin clear — удалить все.")
		return b.String()

	default:
		s.AddPinned(content)
		if h.log != nil {
			h.log.InfoLogf("User %d pinned prompt: %s", peerID, util.Truncate(content, 100))
		}

		pinCtx, pinCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer pinCancel()
		response, err := h.aiAgent.ProcessMessage(pinCtx, content, peerID)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fmt.Sprintf("✓ Промпт закреплён: %s\n\nОперация отменена или истёк таймаут.", util.Truncate(content, 100))
		}
		if err != nil {
			return fmt.Sprintf("✓ Промпт закреплён: %s\n\n❌ Ошибка при выполнении: %v", util.Truncate(content, 100), err)
		}
		return fmt.Sprintf("✓ Промпт закреплён: %s\n\n%s", util.Truncate(content, 100), response)
	}
}

func (h *BotHandler) handleAgentCommand(input string, peerID int64) string {
	parts := strings.SplitN(input, " ", 2)
	instruction := "изучи текущий проект и создай документацию с рекомендациями по доработке"
	if len(parts) > 1 {
		instruction = strings.TrimSpace(parts[1])
	}

	if h.orchestrator != nil {
		if h.log != nil {
			h.log.InfoLogf("Starting /agent mode for peer %d: %s", peerID, util.Truncate(instruction, 100))
		}
		ctx, agCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer agCancel()
		response, err := h.orchestrator.ExecuteTask(ctx, instruction, peerID)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return "Операция отменена или истёк таймаут."
			}
			if h.log != nil {
				h.log.ErrorLogf("Orchestrator error in /agent: %v", err)
			}
			return "Произошла ошибка при выполнении команды /agent. Попробуйте позже."
		}
		s := h.aiAgent.EnsureSession(peerID)
		s.AddUserMessage(input)
		s.AddAssistantMessage(response)
		return response
	}

	ctx, agCancel2 := context.WithTimeout(context.Background(), 5*time.Minute)
	defer agCancel2()
	response, err := h.aiAgent.ProcessMessage(ctx, instruction, peerID)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "Операция отменена или истёк таймаут."
	}
	if err != nil {
		if h.log != nil {
			h.log.ErrorLogf("AI Agent error in /agent: %v", err)
		}
		return "Произошла ошибка при выполнении команды /agent. Попробуйте позже."
	}

	return response
}

func extractBaseCommand(input string) string {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func (h *BotHandler) handleSessions(peerID int64) string {
	if h.orchestrator == nil {
		return "Orchestrator not configured."
	}
	sessions, err := h.orchestrator.GetActiveAgentSessions(peerID)
	if err != nil {
		return fmt.Sprintf("Error getting sessions: %v", err)
	}
	return sessions
}

func (h *BotHandler) handleClear(peerID int64) string {
	workingDir := ""
	if s := h.aiAgent.GetSession(peerID); s != nil {
		workingDir = s.GetWorkingDir()
	}
	return h.handleNewSession("/n "+workingDir, peerID)
}

func (h *BotHandler) handleNewSession(input string, peerID int64) string {
	newPath := ""
	parts := strings.SplitN(input, " ", 2)
	if len(parts) > 1 {
		newPath = strings.TrimSpace(parts[1])
	}

	if newPath == "" {
		var err error
		newPath, err = os.Getwd()
		if err != nil {
			return "Ошибка: не удалось определить текущую директорию."
		}
	}

	newPath = expandTilde(newPath)

	info, err := os.Stat(newPath)
	if err != nil || !info.IsDir() {
		return fmt.Sprintf("Ошибка: директория '%s' не существует.", newPath)
	}

	absPath, err := filepath.Abs(newPath)
	if err != nil {
		return fmt.Sprintf("Ошибка: не удалось получить абсолютный путь: %v", err)
	}

	// Очередь гарантирует что этот код выполняется только когда предыдущий запрос завершён.
	// cancelActiveRequest не нужен — контекст уже свободен (или отменён сам по себе).
	tools.UnregisterPendingQuestion(peerID)
	h.aiAgent.ResetSession(peerID)
	tools.ClearGrants(peerID)
	if h.orchestrator != nil {
		h.orchestrator.ClearActiveSessions(peerID)
	}

	if s := h.aiAgent.GetSession(peerID); s != nil {
		s.SetWorkingDir(absPath)
	}

	tools.SetWorkingDir(absPath)

	if ctrl := tools.GetAccessController(); ctrl != nil {
		ctrl.AddAllowedDir(absPath)
		if h.log != nil {
			h.log.InfoLogf("Granted access to new working dir: %s", absPath)
		}
	}

	h.clearHandlerSession(peerID)

	if h.log != nil {
		h.log.InfoLogf("Session reset for peer %d, working dir: %s", peerID, absPath)
	}

	return fmt.Sprintf("Сессия сброшена.\nРабочая директория: %s", absPath)
}

func (h *BotHandler) handleTestLlama() string {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()

	model, responseTime, tokensPerSec, err := h.aiAgent.TestLlamaServer(ctx)
	if err != nil {
		return fmt.Sprintf("Llama-server: ОШИБКА\n%v", err)
	}

	result := fmt.Sprintf("Llama-server: OK\n"+
		"Модель: %s\n"+
		"Время ответа: %v\n",
		model, responseTime.Round(time.Millisecond))

	if tokensPerSec > 0 {
		result += fmt.Sprintf("Скорость: %.1f токенов/сек", tokensPerSec)
	}

	return result
}

func (h *BotHandler) clearHandlerSession(peerID int64) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	delete(h.sessions, peerID)
}

func (h *BotHandler) cancelActiveRequest(peerID int64) {
	h.cancelMu.Lock()
	defer h.cancelMu.Unlock()
	if cancel, ok := h.cancelFuncs[peerID]; ok {
		cancel()
		delete(h.cancelFuncs, peerID)
		logger.DebugToFile("[cancelActiveRequest] Cancelled active request for peer %d", peerID)
	}
}

func (h *BotHandler) setCancelFunc(peerID int64, cancel context.CancelFunc) {
	h.cancelMu.Lock()
	defer h.cancelMu.Unlock()
	if prev, ok := h.cancelFuncs[peerID]; ok {
		prev()
	}
	h.cancelFuncs[peerID] = cancel
}

func (h *BotHandler) clearCancelFunc(peerID int64) {
	h.cancelMu.Lock()
	defer h.cancelMu.Unlock()
	delete(h.cancelFuncs, peerID)
}

func (h *BotHandler) ensureSession(peerID int64) {
	if h.aiAgent == nil {
		if h.log != nil {
			h.log.WarnLogf("AgentLoop is nil, cannot ensure session for peer %d", peerID)
		}
	}
}

func (h *BotHandler) Start(ctx context.Context) error {
	if h.log != nil {
		h.log.InfoLog("Starting VK Long Poll bot...")
	}

	for {
		select {
		case <-ctx.Done():
			if h.log != nil {
				h.log.InfoLog("Bot handler stopped")
			}
			return nil
		default:
			server, key, ts, err := h.vkClient.GetLongPollServer()
			if err != nil {
				if h.log != nil {
					h.log.WarnLogf("Failed to get long poll server: %v", err)
				}
				time.Sleep(3 * time.Second)
				continue
			}

			if h.log != nil {
				h.log.InfoLog("Connected to VK Long Poll server")
			}

			if err := h.runLongPoll(ctx, server, key, ts); err != nil {
				if h.log != nil {
					h.log.WarnLogf("Long poll disconnected: %v", err)
				}
				time.Sleep(3 * time.Second)
			}
		}
	}
}

func (h *BotHandler) runLongPoll(ctx context.Context, server, key string, ts int64) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			messages, newTs, err := h.vkClient.CheckUpdates(ctx, server, key, ts)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				errStr := err.Error()
				if strings.Contains(errStr, "long poll failed") {
					return err
				}
				time.Sleep(1 * time.Second)
				continue
			}

			ts = newTs
			fullMsgMap := h.fetchFullMessages(messages)

			for _, msg := range messages {
				if h.thinkingPeerID > 0 && msg.PeerID == h.thinkingPeerID {
					continue
				}
				replyPeerID := msg.PeerID
				if h.mainPeerID > 0 {
					replyPeerID = h.mainPeerID
				}
				h.launchMessageHandler(msg, replyPeerID, fullMsgMap)
			}
		}
	}
}

func (h *BotHandler) fetchFullMessages(messages []VKMessage) map[int64]VKMessage {
	if len(messages) == 0 {
		return nil
	}
	ids := make([]int64, len(messages))
	for i, m := range messages {
		ids[i] = m.ID
	}
	full, err := h.vkClient.GetMessagesByID(ids)
	if err != nil {
		if h.log != nil {
			h.log.WarnLogf("Failed to fetch full messages: %v", err)
		}
		return nil
	}
	result := make(map[int64]VKMessage, len(full))
	for _, m := range full {
		result[m.ID] = m
	}
	return result
}

func (h *BotHandler) handleIncomingMessage(
	msg VKMessage,
	targetPeer int64,
	fullMsgMap map[int64]VKMessage,
) {
	tools.SetQuestionPeerID(msg.PeerID)
	fullText := h.buildFullText(&msg, fullMsgMap)
	logger.DebugToFile("[handler] goroutine: peerID=%d, targetPeer=%d, text=%s",
		msg.PeerID, targetPeer, util.Truncate(fullText, 100))

	response := h.ProcessMessage(fullText, msg.PeerID)
	if response == "" {
		return
	}
	_, err := h.vkClient.SendMessage(targetPeer, response)
	if err != nil && h.log != nil {
		h.log.ErrorLogf("Failed to send response to peer %d: %v", targetPeer, err)
	}
}

// launchMessageHandler spawns a goroutine for message processing bounded by semaphore.
// If the semaphore is full, it drops the message with a warning log.
func (h *BotHandler) launchMessageHandler(
	msg VKMessage,
	replyPeerID int64,
	fullMsgMap map[int64]VKMessage,
) {
	select {
	case h.semaphore <- struct{}{}:
		go func() {
			defer func() { <-h.semaphore }()
			h.handleIncomingMessage(msg, replyPeerID, fullMsgMap)
		}()
	default:
		if h.log != nil {
			h.log.WarnLogf("Dropping message from peer %d: max concurrent handlers (%d) reached", msg.PeerID, maxConcurrentHandlers)
		}
	}
}

func (h *BotHandler) buildFullText(msg *VKMessage, fullMsgMap map[int64]VKMessage) string {
	full, found := fullMsgMap[msg.ID]
	if !found || len(full.Attachments) == 0 {
		return msg.Text
	}
	absAttachmentsDir, _ := filepath.Abs(h.attachmentsDir)
	if ctrl := tools.GetAccessController(); ctrl != nil {
		ctrl.AddAllowedDir(absAttachmentsDir)
	}

	rawAttachments := toRawAttachments(full.Attachments)
	downloaded, err := DownloadAttachments(rawAttachments, h.attachmentsDir)
	if err != nil {
		if h.log != nil {
			h.log.WarnLogf("Failed to download attachments: %v", err)
		}
		return msg.Text
	}
	info := FormatAttachmentInfo(downloaded)
	if info == "" {
		return msg.Text
	}
	return msg.Text + "\n\n" + info
}

func toRawAttachments(attachments []VKAttachment) []map[string]interface{} {
	result := make([]map[string]interface{}, len(attachments))
	for i, a := range attachments {
		result[i] = a.ToRaw()
	}
	return result
}
