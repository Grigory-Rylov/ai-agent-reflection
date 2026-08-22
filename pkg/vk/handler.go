package vk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agentloop"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/util/stringutil"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
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

	ClearRegisteredAgents(peerID int64) []string

	IsPrimary(agentName string) bool

	GetSystemPrompt(agentName string) (string, error)

	ActiveChainPeers() []int64
	ResumeActiveChainsForPeer(ctx context.Context, peerID int64) error
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
	cancelFuncs    map[int64]*cancelEntry
	cancelMu       sync.RWMutex
	attachmentsDir string

	peerProcessors     map[int64]*sync.Mutex
	peerProcessorsMu   sync.RWMutex

	semaphore chan struct{}

	pendingKeyboards    map[int64]map[string]interface{}
	pendingKeyboardMu   sync.RWMutex

	queueMu       sync.Mutex
	waitingCounts map[int64]int
	generations   map[int64]uint64
}

const maxConcurrentHandlers = 10

func NewBotHandler(vkClient *BotClient, aiAgent agentloop.AgentLoop, log *logger.Logger) *BotHandler {
	return &BotHandler{
		vkClient:         vkClient,
		aiAgent:          aiAgent,
		log:              log,
		sessions:         make(map[int64]*session.Session),
		cancelFuncs:      make(map[int64]*cancelEntry),
		peerProcessors:   make(map[int64]*sync.Mutex),
		pendingKeyboards: make(map[int64]map[string]interface{}),
		waitingCounts:    make(map[int64]int),
		generations:      make(map[int64]uint64),
		attachmentsDir:   "./attachments",
		semaphore:        make(chan struct{}, maxConcurrentHandlers),
	}
}

func NewBotHandlerWithPeerID(vkClient *BotClient, aiAgent agentloop.AgentLoop, log *logger.Logger, mainPeerID, thinkingPeerID int64, orchestrator AgentOrchestrator, modelHolder *modelsconfig.Holder) *BotHandler {
	return &BotHandler{
		vkClient:         vkClient,
		aiAgent:          aiAgent,
		orchestrator:     orchestrator,
		log:              log,
		sessions:         make(map[int64]*session.Session),
		mainPeerID:       mainPeerID,
		thinkingPeerID:   thinkingPeerID,
		modelHolder:      modelHolder,
		cancelFuncs:      make(map[int64]*cancelEntry),
		peerProcessors:   make(map[int64]*sync.Mutex),
		pendingKeyboards: make(map[int64]map[string]interface{}),
		waitingCounts:    make(map[int64]int),
		generations:      make(map[int64]uint64),
		attachmentsDir:   "./attachments",
		semaphore:        make(chan struct{}, maxConcurrentHandlers),
	}
}

func (h *BotHandler) agentNames() []string {
	if h.orchestrator != nil {
		return h.orchestrator.ListAgentNames()
	}
	return nil
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

	if tools.HasPendingQuestion(peerID) {
		logger.DebugToFile("[ProcessMessage] HasPendingQuestion=true for peer %d, command=%s", peerID, stringutil.Truncate(command, 100, "..."))
		if tools.ResolvePendingQuestion(peerID, command) {
			logger.DebugToFile("[ProcessMessage] Resolved pending question for peer %d with: %s", peerID, stringutil.Truncate(command, 50, "..."))
			return ""
		}
		logger.DebugToFile("[ProcessMessage] ResolvePendingQuestion returned false for peer %d, command=%s", peerID, stringutil.Truncate(command, 50, "..."))
	}

	h.admitPeerInput(peerID, message)

	releaseQueueSlot, generationAtArrival := h.beginProcessingWait(peerID)
	mu := h.getPeerMutex(peerID)
	mu.Lock()
	releaseQueueSlot()
	defer mu.Unlock()

	if h.peerGeneration(peerID) != generationAtArrival {
		logger.DebugToFile("[ProcessMessage] peer %d: session was reset while message waited in queue, dropping stale message", peerID)
		return ""
	}

	if !h.claimPeerInput(peerID, message) {
		logger.DebugToFile("[ProcessMessage] peer %d: message already promoted into running turn, dropping", peerID)
		return ""
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cancelEntry := &cancelEntry{cancel: cancel}
	h.setCancelFunc(peerID, cancelEntry.cancel)
	defer h.clearCancelFunc(peerID, cancelEntry)

	if agentName, task := ParseAgentHashMention(message, h.agentNames()); agentName != "" {
		if h.log != nil {
			h.log.InfoLogf("Agent #%s invoked by peer %d with task: %s", agentName, peerID, stringutil.Truncate(task, 100, "..."))
		}

		if task == "" {
			return fmt.Sprintf("Укажите задачу для #%s. Например: #%s создай простой HTTP сервер", agentName, agentName)
		}

		if h.orchestrator != nil && h.orchestrator.IsPrimary(agentName) {

			agentPrompt, err := h.orchestrator.GetSystemPrompt(agentName)
			logger.DebugToFile("[#%s] GetSystemPrompt -> %d chars, err=%v", agentName, len(agentPrompt), err)
			if err != nil {
				if h.log != nil {
					h.log.ErrorLogf("Failed to load system prompt for #%s: %v", agentName, err)
				}
				return fmt.Sprintf("❌ Ошибка при выполнении задачи через #%s: %v", agentName, err)
			}
			response, err := h.aiAgent.ProcessPromptWithSystemPrompt(ctx, task, peerID, agentPrompt)
			if err != nil {
				if errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled") {
					return ""
				}
				if h.log != nil {
					h.log.ErrorLogf("Main agent error for #%s: %v", agentName, err)
				}
				return fmt.Sprintf("❌ Ошибка при выполнении задачи через #%s: %v", agentName, err)
			}
			return response
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
		response, err := h.aiAgent.ProcessMessage(ctx, message, peerID)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return ""
			}
			return fmt.Sprintf("❌ Ошибка: %v", err)
		}
		return response
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
}

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

func (h *BotHandler) admitPeerInput(peerID int64, message string) {
	s := h.aiAgent.EnsureSession(peerID)
	if s == nil {
		return
	}
	if in := s.GetPeerInput(); in != nil {
		in.Admit(message)
	}
}

func (h *BotHandler) claimPeerInput(peerID int64, message string) bool {
	s := h.aiAgent.GetSession(peerID)
	if s == nil {
		return true
	}
	if in := s.GetPeerInput(); in != nil {
		return in.Claim(message)
	}
	return true
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

var restarterCommands = map[string]bool{
	"/update":  true,
	"/b":       true,
	"/restart": true,
	"/stop":    true,
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
			"/log, /logs - Отправить все файлы из папки debug/\n" +
			"/m, /models - Список доступных моделей\n" +
			"/r [alias] - Переключить текущую модель\n" +
			"/pin <промпт> - Закрепить промпт (переживает компактизацию) и выполнить его\n" +
			"/restart - Перезапустить агента без пересборки\n" +
			"/update - git pull, пересобрать и перезапустить агента\n" +

			"Перенаправление задачи агенту через #:\n"
		for _, name := range knownNames {
			helpStr += fmt.Sprintf("#%s, ", name)
		}
		helpStr = strings.TrimSuffix(helpStr, ", ")
		helpStr += " — доступные роли"
		return helpStr

	case "/test-llama":
		return h.handleTestLlama()

	case "/log", "/logs":
		return h.handleLogCommand(peerID)

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

	case "/pin":
		return h.handlePinCommand(input, peerID)

	case "/m", "/models":
		return h.handleModelsList(peerID)

	case "/r":
		return h.handleModelSwitch(input)

	case "/restart":
		h.writeSignalFile(".agent-restart", "")
		return "Перезапуск агента..."

	case "/update":
		h.writeSignalFile(".agent-update", "")
		return "Обновление агента: git pull, build, restart..."

	case "/b":
		branch := strings.TrimSpace(strings.TrimPrefix(input, "/b"))
		if branch == "" {
			return "Укажите ветку: /b <branch>"
		}
		h.writeSignalFile(".agent-branch", branch)
		return fmt.Sprintf("Переключение на ветку %s...", branch)

	default:
		return ""
	}
}

func (h *BotHandler) handleModelsList(peerID int64) string {
	if h.modelHolder == nil {
		return "Модели не настроены (models.json не загружен)"
	}

	models := h.modelHolder.List()
	currentAlias := h.modelHolder.GetDefaultAlias()

	aliases := make([]string, 0, len(models))
	for alias := range models {
		aliases = append(aliases, alias)
	}
	h.setPendingKeyboard(peerID, CreateModelsKeyboard(aliases, currentAlias))

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

func (h *BotHandler) setPendingKeyboard(peerID int64, kb map[string]interface{}) {
	h.pendingKeyboardMu.Lock()
	h.pendingKeyboards[peerID] = kb
	h.pendingKeyboardMu.Unlock()
}

func (h *BotHandler) popPendingKeyboard(peerID int64) map[string]interface{} {
	h.pendingKeyboardMu.Lock()
	kb := h.pendingKeyboards[peerID]
	delete(h.pendingKeyboards, peerID)
	h.pendingKeyboardMu.Unlock()
	return kb
}

func (h *BotHandler) payloadToCommand(payloadJSON string) string {
	var payload struct {
		Command string `json:"command"`
		Alias   string `json:"alias"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return ""
	}
	switch payload.Command {
	case "model_switch":
		return fmt.Sprintf("/r %s", payload.Alias)
	default:
		return ""
	}
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
			h.log.InfoLogf("User %d pinned prompt: %s", peerID, stringutil.Truncate(content, 100, "..."))
		}

		ctx, release, started := h.beginExclusiveTurn(peerID)
		if !started {
			return fmt.Sprintf("✓ Промпт закреплён: %s\n\nВыполнение отменено: сессия была сброшена.", stringutil.Truncate(content, 100, "..."))
		}
		defer release()

		response, err := h.aiAgent.ProcessMessage(ctx, content, peerID)
		if errors.Is(err, context.Canceled) {
			return fmt.Sprintf("✓ Промпт закреплён: %s\n\nОперация отменена.", stringutil.Truncate(content, 100, "..."))
		}
		if err != nil {
			return fmt.Sprintf("✓ Промпт закреплён: %s\n\n❌ Ошибка при выполнении: %v", stringutil.Truncate(content, 100, "..."), err)
		}
		return fmt.Sprintf("✓ Промпт закреплён: %s\n\n%s", stringutil.Truncate(content, 100, "..."), response)
	}
}

func (h *BotHandler) beginExclusiveTurn(peerID int64) (context.Context, func(), bool) {
	releaseQueueSlot, generationAtArrival := h.beginProcessingWait(peerID)
	mu := h.getPeerMutex(peerID)
	mu.Lock()
	releaseQueueSlot()
	if h.peerGeneration(peerID) != generationAtArrival {
		mu.Unlock()
		return nil, func() {}, false
	}

	ctx, cancel := context.WithCancel(context.Background())
	entry := &cancelEntry{cancel: cancel}
	h.setCancelFunc(peerID, cancel)
	release := func() {
		h.clearCancelFunc(peerID, entry)
		cancel()
		mu.Unlock()
	}
	return ctx, release, true
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
	return h.handleNewSession("/n "+h.resolveClearWorkingDir(peerID), peerID)
}

func (h *BotHandler) resolveClearWorkingDir(peerID int64) string {
	if s := h.aiAgent.GetSession(peerID); s != nil {
		if wd := s.GetWorkingDir(); wd != "" && h.isAccessibleDir(wd) {
			return wd
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func (h *BotHandler) isAccessibleDir(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
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

	h.cancelActiveRequest(peerID)
	if h.orchestrator != nil {
		h.orchestrator.ClearActiveSessions(peerID)
	}

	tools.UnregisterPendingQuestion(peerID)

	if s := h.aiAgent.GetSession(peerID); s != nil {
		if in := s.GetPeerInput(); in != nil {
			in.Clear()
		}
	}

	h.aiAgent.ClearPeerSession(peerID)
	tools.ClearGrants(peerID)

	if st := h.aiAgent.GetStore(); st != nil {
		if err := st.ClearPeerData(peerID); err != nil && h.log != nil {
			h.log.WarnLogf("ClearPeerData for peer %d: %v", peerID, err)
		}
	}
	tools.GlobalTodo.Reset()

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

	h.bumpPeerGeneration(peerID)

	if h.log != nil {
		h.log.InfoLogf("Session reset for peer %d, working dir: %s", peerID, absPath)
	}

	return fmt.Sprintf("Сессия сброшена.\nРабочая директория: %s", absPath)
}

func (h *BotHandler) handleTestLlama() string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
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

func (h *BotHandler) logDirPath() string {
	logPath := "debug/debug.log"
	if h.log != nil {
		if configured := h.log.LogFilePath(); configured != "" {
			logPath = configured
		}
	}

	absPath, err := filepath.Abs(logPath)
	if err != nil {
		return logPath
	}
	return filepath.Dir(absPath)
}

func (h *BotHandler) collectDebugFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read debug dir: %w", err)
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(files)
	return files, nil
}

func (h *BotHandler) sendDebugFiles(peerID int64, files []string) error {
	targetPeer := peerID
	if h.mainPeerID > 0 {
		targetPeer = h.mainPeerID
	}

	for _, file := range files {
		logger.DebugToFile("[handleLogCommand] sending %s to peer %d", file, targetPeer)
		if _, err := h.vkClient.UploadAndSendDocument(file, targetPeer, "📋 Логи"); err != nil {
			return fmt.Errorf("send %s: %w", filepath.Base(file), err)
		}
	}
	return nil
}

func (h *BotHandler) handleLogCommand(peerID int64) string {
	dir := h.logDirPath()

	files, err := h.collectDebugFiles(dir)
	if err != nil || len(files) == 0 {
		if h.log != nil {
			h.log.WarnLogf("/log: debug dir is empty or missing: %s (%v)", dir, err)
		}
		return fmt.Sprintf("❌ Файлы логов не найдены в: %s", dir)
	}
	if h.vkClient == nil {
		if h.log != nil {
			h.log.WarnLogf("/log: VK client is nil, cannot send files from %s", dir)
		}
		return "❌ VK client не настроен"
	}

	if err := h.sendDebugFiles(peerID, files); err != nil {
		if h.log != nil {
			h.log.ErrorLogf("/log: failed to send log files: %v", err)
		}
		return fmt.Sprintf("❌ Ошибка отправки логов: %v", err)
	}
	return fmt.Sprintf("📋 Отправлено файлов: %d (из %s)", len(files), dir)
}

func (h *BotHandler) clearHandlerSession(peerID int64) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	delete(h.sessions, peerID)
}

func (h *BotHandler) ScheduleResume(peerID int64) {
	h.spawnCancellableTurn(peerID, func(ctx context.Context) {
		h.aiAgent.ResumeInterruptedTask(ctx, peerID)
	})
}

func (h *BotHandler) ScheduleChainResume() {
	if h.orchestrator == nil {
		return
	}
	for _, peerID := range h.orchestrator.ActiveChainPeers() {
		peer := peerID
		h.spawnCancellableTurn(peer, func(ctx context.Context) {
			if err := h.orchestrator.ResumeActiveChainsForPeer(ctx, peer); err != nil && h.log != nil {
				h.log.WarnLogf("Chain resume for peer %d: %v", peer, err)
			}
		})
	}
}

func (h *BotHandler) spawnCancellableTurn(peerID int64, run func(ctx context.Context)) {
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		entry := &cancelEntry{cancel: cancel}
		if !h.setCancelEntryIfIdle(peerID, entry) {
			cancel()
			if h.log != nil {
				h.log.InfoLogf("Skip resume for peer %d: another request is already active", peerID)
			}
			return
		}
		defer h.clearCancelFunc(peerID, entry)
		run(ctx)
	}()
}

func (h *BotHandler) setCancelEntryIfIdle(peerID int64, entry *cancelEntry) bool {
	h.cancelMu.Lock()
	defer h.cancelMu.Unlock()
	if cur, ok := h.cancelFuncs[peerID]; ok && !cur.cancelled {
		return false
	}
	h.cancelFuncs[peerID] = entry
	return true
}

type cancelEntry struct {
	cancel    context.CancelFunc
	cancelled bool
}

func (h *BotHandler) cancelActiveRequest(peerID int64) {
	h.cancelMu.Lock()
	defer h.cancelMu.Unlock()
	if entry, ok := h.cancelFuncs[peerID]; ok {
		entry.cancel()
		entry.cancelled = true
		delete(h.cancelFuncs, peerID)
		logger.DebugToFile("[cancelActiveRequest] Cancelled active request for peer %d", peerID)
	}
}

func (h *BotHandler) setCancelFunc(peerID int64, cancel context.CancelFunc) {
	h.cancelMu.Lock()
	defer h.cancelMu.Unlock()

	if prev, ok := h.cancelFuncs[peerID]; ok && !prev.cancelled {
		prev.cancel()
	}
	h.cancelFuncs[peerID] = &cancelEntry{cancel: cancel}
}

func (h *BotHandler) clearCancelFunc(peerID int64, entry *cancelEntry) {
	h.cancelMu.Lock()
	defer h.cancelMu.Unlock()

	if cur, ok := h.cancelFuncs[peerID]; ok && cur == entry {
		delete(h.cancelFuncs, peerID)
	}
}

func (h *BotHandler) beginProcessingWait(peerID int64) (release func(), generation uint64) {
	h.queueMu.Lock()
	h.waitingCounts[peerID]++
	waiting := h.waitingCounts[peerID]
	generation = h.generations[peerID]
	h.queueMu.Unlock()

	if waiting > 1 {
		logger.DebugToFile("[queue] peer %d: %d message(s) waiting for session", peerID, waiting)
	}
	return func() {
		h.queueMu.Lock()
		defer h.queueMu.Unlock()
		if h.waitingCounts[peerID] > 0 {
			h.waitingCounts[peerID]--
		}
	}, generation
}

func (h *BotHandler) bumpPeerGeneration(peerID int64) {
	h.queueMu.Lock()
	defer h.queueMu.Unlock()
	h.generations[peerID]++
}

func (h *BotHandler) peerGeneration(peerID int64) uint64 {
	h.queueMu.Lock()
	defer h.queueMu.Unlock()
	return h.generations[peerID]
}

func (h *BotHandler) waitingMessages(peerID int64) int {
	h.queueMu.Lock()
	defer h.queueMu.Unlock()
	return h.waitingCounts[peerID]
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
	ids := make([]int64, 0, len(messages))
	for _, m := range messages {
		if m.EventID != "" || m.ID == 0 {
			continue
		}
		ids = append(ids, m.ID)
	}
	if len(ids) == 0 {
		return nil
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

	isCallback := msg.EventID != ""

	if isCallback {
		logger.DebugToFile("[handler] callback received: peerID=%d, eventID=%s, payload=%s", msg.PeerID, msg.EventID, msg.Payload)
		err := h.vkClient.SendMessageEventAnswer(msg.EventID, msg.FromID, msg.PeerID, "")
		if err != nil && h.log != nil {
			h.log.ErrorLogf("Failed to answer callback event: %v", err)
		}
	}

	fullText := h.buildFullText(&msg, fullMsgMap)
	if msg.Payload != "" {
		if cmd := h.payloadToCommand(msg.Payload); cmd != "" {
			logger.DebugToFile("[handler] callback payload: peerID=%d, cmd=%s", msg.PeerID, cmd)
			fullText = cmd
		}
	}

	logger.DebugToFile("[handler] goroutine: peerID=%d, targetPeer=%d, text=%s",
		msg.PeerID, targetPeer, stringutil.Truncate(fullText, 100, "..."))

	response := h.ProcessMessage(fullText, msg.PeerID)
	if response == "" {
		return
	}

	kb := h.popPendingKeyboard(msg.PeerID)
	if kb != nil {
		_, err := h.vkClient.SendMessageWithKeyboard(targetPeer, response, kb)
		if err != nil && h.log != nil {
			h.log.ErrorLogf("Failed to send response with keyboard to peer %d: %v", targetPeer, err)
		}
		return
	}

	_, err := h.vkClient.SendMessage(targetPeer, response)
	if err != nil && h.log != nil {
		h.log.ErrorLogf("Failed to send response to peer %d: %v", targetPeer, err)
	}
}

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
		text := strings.TrimSpace(msg.Text)
		if msg.EventID == "" && text != "" && !strings.HasPrefix(text, "/") {
			logger.DebugToFile("[handler] semaphore saturated: admitting text from peer %d into steer inbox instead of dropping", msg.PeerID)
			h.admitPeerInput(msg.PeerID, text)
		} else if h.log != nil {
			h.log.WarnLogf("Dropping message from peer %d: max concurrent handlers (%d) reached", msg.PeerID, maxConcurrentHandlers)
		}
	}
}

func (h *BotHandler) buildFullText(msg *VKMessage, fullMsgMap map[int64]VKMessage) string {
	full, found := fullMsgMap[msg.ID]

	atts := full.Attachments
	attSource := "getById"
	if !found || len(atts) == 0 {
		if len(msg.Attachments) > 0 {
			atts = msg.Attachments
			attSource = "longpoll"
			if !found {
				logger.DebugToFile("[buildFullText] msg id=%d: full message not fetched (id=%d), falling back to long-poll attachments", msg.ID, msg.ID)
			}
		} else {
			return msg.Text
		}
	}

	absAttachmentsDir, _ := filepath.Abs(h.attachmentsDir)
	if ctrl := tools.GetAccessController(); ctrl != nil {
		ctrl.AddAllowedDir(absAttachmentsDir)
	}

	logger.DebugToFile("[buildFullText] msg id=%d: %d attachment(s) from %s: %s", msg.ID, len(atts), attSource, describeAttachments(atts))
	downloaded, err := h.downloadAttachments(atts)
	if err != nil {
		logger.DebugToFile("[buildFullText] msg id=%d: download failed: %v (downloaded %d of %d)", msg.ID, err, len(downloaded), len(atts))
	}
	info := FormatAttachmentInfo(downloaded)
	if info == "" {
		logger.DebugToFile("[buildFullText] msg id=%d: no attachments could be downloaded, LLM will not see file paths", msg.ID)
		return msg.Text
	}
	logger.DebugToFile("[buildFullText] msg id=%d: %d file(s) saved, paths appended to prompt", msg.ID, len(downloaded))
	return msg.Text + "\n\n" + info
}

func (h *BotHandler) downloadAttachments(atts []VKAttachment) ([]DownloadedAttachment, error) {
	return DownloadAttachments(toRawAttachments(atts), h.attachmentsDir)
}

func describeAttachments(atts []VKAttachment) string {
	parts := make([]string, 0, len(atts))
	for _, a := range atts {
		parts = append(parts, a.Type)
	}
	return strings.Join(parts, ",")
}

func toRawAttachments(attachments []VKAttachment) []map[string]interface{} {
	result := make([]map[string]interface{}, len(attachments))
	for i, a := range attachments {
		result[i] = a.ToRaw()
	}
	return result
}

func (h *BotHandler) writeSignalFile(name string, data string) {
	if h.log != nil {
		h.log.InfoLogf("Writing signal file: %s", name)
	}
	os.WriteFile(filepath.Join(".", name), []byte(data), 0644)
}
