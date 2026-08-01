package vk

import (
	"context"
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
}

func NewBotHandler(vkClient *BotClient, aiAgent agentloop.AgentLoop, log *logger.Logger) *BotHandler {
	return &BotHandler{
		vkClient: vkClient,
		aiAgent:  aiAgent,
		log:      log,
		sessions: make(map[int64]*session.Session),
	}
}

func NewBotHandlerWithPeerID(vkClient *BotClient, aiAgent agentloop.AgentLoop, log *logger.Logger, mainPeerID, thinkingPeerID int64, orchestrator AgentOrchestrator, modelHolder *modelsconfig.Holder) *BotHandler {
	return &BotHandler{
		vkClient:      vkClient,
		aiAgent:       aiAgent,
		orchestrator:  orchestrator,
		log:           log,
		sessions:      make(map[int64]*session.Session),
		mainPeerID:    mainPeerID,
		thinkingPeerID: thinkingPeerID,
		modelHolder:   modelHolder,
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

	if tools.HasPendingQuestion(peerID) {
		logger.DebugToFile("[ProcessMessage] HasPendingQuestion=true for peer %d, command=%s", peerID, truncateStr(command, 100))
		if tools.ResolvePendingQuestion(peerID, command) {
			logger.DebugToFile("[ProcessMessage] Resolved pending question for peer %d with: %s", peerID, truncateStr(command, 50))
			return ""
		} else {
			logger.DebugToFile("[ProcessMessage] ResolvePendingQuestion returned false for peer %d, command=%s", peerID, truncateStr(command, 50))
		}
	} else {
		logger.DebugToFile("[ProcessMessage] HasPendingQuestion=false for peer %d, command=%s", peerID, truncateStr(command, 100))
	}

	agentName, task := ParseAgentHashMention(command, h.agentNames())
	if agentName != "" {
		if h.log != nil {
			h.log.InfoLogf("Agent #%s invoked by peer %d with task: %s", agentName, peerID, truncateStr(task, 100))
		}

		if task == "" {
			return fmt.Sprintf("Укажите задачу для #%s. Например: #%s создай простой HTTP сервер", agentName, agentName)
		}

		if h.orchestrator != nil {
			ctx := context.Background()
			response, err := h.orchestrator.RunAgent(ctx, agentName, task, peerID)
			if err != nil {
				if h.log != nil {
					h.log.ErrorLogf("Orchestrator error for #%s: %v", agentName, err)
				}
				return fmt.Sprintf("❌ Ошибка при выполнении задачи через #%s: %v", agentName, err)
			}
			return response
		}

		message = fmt.Sprintf("[Задача для #%s]\n\n%s", agentName, task)
	}

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

	s := h.aiAgent.GetSession(peerID)
	if s != nil && s.IsLoopDetected() {
		alert := s.GetLoopAlertMessage()
		if alert != "" {
			message = "[LOOP DETECTED] " + alert + "\n\n" + message
		}
	}

	ctx := context.Background()
	response, err := h.aiAgent.ProcessMessage(ctx, message, peerID)
	if err != nil {
		if h.log != nil {
			h.log.ErrorLogf("AI Agent error: %v", err)
		}
		return fmt.Sprintf("❌ Ошибка: %v", err)
	}

	return response
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
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

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

	s := h.aiAgent.GetSession(peerID)
	if s != nil && s.IsLoopDetected() {
		alert := s.GetLoopAlertMessage()
		if alert != "" {
			message = "[LOOP DETECTED] " + alert + "\n\n" + message
		}
	}

	response, err := h.aiAgent.ProcessMessage(ctx, message, peerID)
	if err != nil {
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
	case "/reset", "/clear":
		h.aiAgent.ResetSession(peerID)
		h.clearHandlerSession(peerID)
		tools.ClearGrants(peerID)
		if h.log != nil {
			h.log.InfoLogf("User %d reset session, grants cleared", peerID)
		}
		return "История диалога очищена. Напишите /newsession [path] чтобы сменить рабочую директорию."

	case "/newsession", "/n":
		return h.handleNewSession(input, peerID)

	case "/help":
		knownNames := h.agentNames()
		helpStr := "Доступные команды:\n" +
			"/reset - Очистить историю диалога\n" +
			"/newsession [path] (/n) - Сбросить сессию и сменить рабочую директорию\n" +
			"/help - Показать эту справку\n" +
			"/status - Показать статус агента (сообщения, символы, токены)\n" +
			"/test-llama - Тест соединения с llama-server\n" +
			"/m, /models - Список доступных моделей\n" +
			"/r [alias] - Переключить текущую модель\n" +
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

func (h *BotHandler) handleAgentCommand(input string, peerID int64) string {
	parts := strings.SplitN(input, " ", 2)
	instruction := "изучи текущий проект и создай документацию с рекомендациями по доработке"
	if len(parts) > 1 {
		instruction = strings.TrimSpace(parts[1])
	}

	if h.orchestrator != nil {
		if h.log != nil {
			h.log.InfoLogf("Starting /agent mode for peer %d: %s", peerID, truncateStr(instruction, 100))
		}
		ctx := context.Background()
		response, err := h.orchestrator.ExecuteTask(ctx, instruction, peerID)
		if err != nil {
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

	ctx := context.Background()
	response, err := h.aiAgent.ProcessMessage(ctx, instruction, peerID)
	if err != nil {
		if h.log != nil {
			h.log.ErrorLogf("AI Agent error in /agent: %v", err)
		}
		return "Произошла ошибка при выполнении команды /agent. Попробуйте позже."
	}

	return response
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func extractBaseCommand(input string) string {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
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

	h.aiAgent.ResetSession(peerID)
	tools.ClearGrants(peerID)

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

func (h *BotHandler) ensureSession(peerID int64) {
	if h.aiAgent == nil {
		if h.log != nil {
			h.log.WarnLogf("AgentLoop is nil, cannot ensure session for peer %d", peerID)
		}
	}
}

func (h *BotHandler) getSession(peerID int64) *session.Session {
	if h.aiAgent != nil {
		return h.aiAgent.GetSession(peerID)
	}
	return nil
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

			for _, msg := range messages {
				if h.thinkingPeerID > 0 && msg.PeerID == h.thinkingPeerID {
					if h.log != nil {
						h.log.DebugLogf("Ignoring message from thinking_peer_id %d", msg.PeerID)
					}
					continue
				}

				if h.log != nil {
					textPreview := msg.Text
					if len(textPreview) > 100 {
						textPreview = textPreview[:100] + "..."
					}
					h.log.InfoLogf("Received message from peer %d: %s", msg.PeerID, textPreview)
				}

				replyPeerID := msg.PeerID
				if h.mainPeerID > 0 {
					replyPeerID = h.mainPeerID
				}

				go func(messageText string, peerID int64, targetPeer int64) {
					tools.SetQuestionPeerID(peerID)
					logger.DebugToFile("[handler] goroutine: peerID=%d, targetPeer=%d, text=%s", peerID, targetPeer, truncateStr(messageText, 100))
					response := h.ProcessMessage(messageText, peerID)
					logger.DebugToFile("[handler] goroutine: ProcessMessage returned response=%q (len=%d)", response, len(response))
					if response == "" {
						return
					}
					_, err := h.vkClient.SendMessage(targetPeer, response)
					if err != nil && h.log != nil {
						h.log.ErrorLogf("Failed to send response to peer %d: %v", targetPeer, err)
					}
				}(msg.Text, msg.PeerID, replyPeerID)
			}
		}
	}
}
