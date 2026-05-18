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
	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/session"
)

// ============================================================
// VK Bot Handler — связующее звено между VK Bot API и AI Agent
// ============================================================

// BotHandler управляет взаимодействием с пользователями через VK Bot
type BotHandler struct {
	vkClient     *BotClient
	aiAgent      agentloop.AgentLoop
	log          *logger.Logger
	sessions     map[int64]*session.Session
	sessionMu    sync.RWMutex
	mainPeerID   int64   // Основной чат для отправки ответов
	thinkingPeerID int64  // Чат для thinking сообщений (используется через AI Agent)
}

// ============================================================
// Инициализация
// ============================================================

// NewBotHandler создаёт новый обработчик VK Bot
func NewBotHandler(vkClient *BotClient, aiAgent agentloop.AgentLoop, log *logger.Logger) *BotHandler {
	return &BotHandler{
		vkClient: vkClient,
		aiAgent:  aiAgent,
		log:      log,
		sessions: make(map[int64]*session.Session),
	}
}

// NewBotHandlerWithPeerID создаёт новый обработчик VK Bot с mainPeerID
func NewBotHandlerWithPeerID(vkClient *BotClient, aiAgent agentloop.AgentLoop, log *logger.Logger, mainPeerID, thinkingPeerID int64) *BotHandler {
	return &BotHandler{
		vkClient:     vkClient,
		aiAgent:      aiAgent,
		log:          log,
		sessions:     make(map[int64]*session.Session),
		mainPeerID:   mainPeerID,
		thinkingPeerID: thinkingPeerID,
	}
}

// ============================================================
// Обработка сообщений
// ============================================================

// ProcessMessage обрабатывает входящее сообщение от пользователя
func (h *BotHandler) ProcessMessage(message string, peerID int64) string {
	h.ensureSession(peerID)

	// Команды бота не передаются модели
	if strings.HasPrefix(message, "/") {
		result := h.handleCommand(message, peerID)
		if result != "" {
			return result
		}
		return fmt.Sprintf("Неизвестная команда: %s. Напишите /help для списка команд.", message)
	}

	s := h.getSession(peerID)
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
		return "Произошла ошибка при обработке запроса. Попробуйте позже."
	}

	return response
}

// ProcessMessageWithTimeout обрабатывает сообщение с таймаутом
func (h *BotHandler) ProcessMessageWithTimeout(message string, peerID int64, timeout time.Duration) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	h.ensureSession(peerID)

	if strings.HasPrefix(message, "/") {
		result := h.handleCommand(message, peerID)
		if result != "" {
			return result
		}
		return fmt.Sprintf("Неизвестная команда: %s. Напишите /help для списка команд.", message)
	}

	s := h.getSession(peerID)
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
		return "Произошла ошибка при обработке запроса. Попробуйте позже."
	}

	return response
}

// ============================================================
// Команды
// ============================================================

// handleCommand обрабатывает системные команды
func (h *BotHandler) handleCommand(input string, peerID int64) string {
	switch input {
	case "/reset", "/clear":
		h.aiAgent.ResetSession(peerID)
		h.clearHandlerSession(peerID)
		if h.log != nil {
			h.log.InfoLogf("User %d reset session", peerID)
		}
		return "История диалога очищена. Напишите /newsession [path] чтобы сменить рабочую директорию."

	case "/newsession":
		return h.handleNewSession(input, peerID)

	case "/help":
		return "Доступные команды:\n" +
			"/reset - Очистить историю диалога\n" +
			"/newsession [path] - Сбросить сессию и сменить рабочую директорию\n" +
			"/help - Показать эту справку\n" +
			"/status - Показать статус агента"

	case "/status":
		s := h.getSession(peerID)
		status := "AI Agent активен и готов к работе."
		if s != nil {
			status += "\nPeer ID: " + fmt.Sprintf("%d", peerID) +
				"\nИстория: " + fmt.Sprintf("%d", s.HistoryLength()) + " сообщений" +
				"\nРабочая директория: " + s.GetWorkingDir()
		}
		return status

	default:
		return ""
	}
}

// handleNewSession обрабатывает /newsession [path]
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

	info, err := os.Stat(newPath)
	if err != nil || !info.IsDir() {
		return fmt.Sprintf("Ошибка: директория '%s' не существует.", newPath)
	}

	absPath, err := filepath.Abs(newPath)
	if err != nil {
		return fmt.Sprintf("Ошибка: не удалось получить абсолютный путь: %v", err)
	}

	// Сбрасываем сессию в agentloop
	h.aiAgent.ResetSession(peerID)

	// Устанавливаем рабочую директорию в сессии agentloop
	if s := h.aiAgent.GetSession(peerID); s != nil {
		s.SetWorkingDir(absPath)
	}

	// Синхронизируем с tools.WorkingDir для файловых операций
	tools.SetWorkingDir(absPath)

	// Очищаем локальную сессию хендлера
	h.clearHandlerSession(peerID)

	if h.log != nil {
		h.log.InfoLogf("Session reset for peer %d, working dir: %s", peerID, absPath)
	}

	return fmt.Sprintf("Сессия сброшена.\nРабочая директория: %s", absPath)
}

// clearHandlerSession удаляет локальную сессию хендлера
func (h *BotHandler) clearHandlerSession(peerID int64) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	delete(h.sessions, peerID)
}

// ============================================================
// Управление сессиями
// ============================================================

// ensureSession гарантирует существование сессии для пользователя
func (h *BotHandler) ensureSession(peerID int64) {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()

	if _, exists := h.sessions[peerID]; !exists {
		// Создаём новую сессию с PeerID
		config := session.DefaultConfig()
		config.PeerID = peerID
		// SessionFile можно указать в конфиге для персистентности
		h.sessions[peerID] = session.NewSession(config)
		if h.log != nil {
			h.log.InfoLogf("Created new session for peer %d", peerID)
		}
	}
}

// getSession возвращает сессию пользователя
func (h *BotHandler) getSession(peerID int64) *session.Session {
	h.sessionMu.RLock()
	defer h.sessionMu.RUnlock()
	return h.sessions[peerID]
}

// cleanupInactiveSessions удаляет неактивные сессии (старше 24 часов)
func (h *BotHandler) cleanupInactiveSessions() {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()

	// Сессии теперь управляются session.Session — просто очищаем map
	// если нужно ограничить количество сессий в памяти
	for peerID, s := range h.sessions {
		// Можно добавить логику очистки на основе updatedAt
		_ = s
		_ = peerID
	}
}

// ============================================================
// Запуск обработчика
// ============================================================

// Start запускает цикл обработки сообщений через VK Long Poll API
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
			// Получаем параметры long polling сервера
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

			// Основной цикл опроса
			if err := h.runLongPoll(ctx, server, key, ts); err != nil {
				if h.log != nil {
					h.log.WarnLogf("Long poll disconnected: %v", err)
				}
				// Пауза перед переподключением
				time.Sleep(3 * time.Second)
			}
		}
	}
}

// runLongPoll выполняет цикл опроса long poll сервера
func (h *BotHandler) runLongPoll(ctx context.Context, server, key string, ts int64) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			// Очистка неактивных сессий
			h.cleanupInactiveSessions()

			// Получаем обновления (ждём до 25 секунд на сервере)
			messages, newTs, err := h.vkClient.CheckUpdates(ctx, server, key, ts)
			if err != nil {
				// Проверяем отмену контекста (Ctrl+C)
				if ctx.Err() != nil {
					return nil
				}
				errStr := err.Error()
				if strings.Contains(errStr, "long poll failed") {
					return err
				}
				// Другие ошибки — короткая пауза и повтор
				time.Sleep(1 * time.Second)
				continue
			}

			// Обновляем ts
			ts = newTs

			// Обрабатываем каждое сообщение
			for _, msg := range messages {
				// Игнорируем сообщения из thinking_peer_id
				if h.thinkingPeerID > 0 && msg.PeerID == h.thinkingPeerID {
					if h.log != nil {
						h.log.DebugLogf("Ignoring message from thinking_peer_id %d", msg.PeerID)
					}
					continue
				}

				if h.log != nil {
					// Показываем текст сообщения (до 100 символов)
					textPreview := msg.Text
					if len(textPreview) > 100 {
						textPreview = textPreview[:100] + "..."
					}
					h.log.InfoLogf("Received message from peer %d: %s", msg.PeerID, textPreview)
				}

				// Определяем куда отправлять ответ
				replyPeerID := msg.PeerID
				if h.mainPeerID > 0 {
					replyPeerID = h.mainPeerID
				}

				// Обрабатываем сообщение в отдельной goroutine
				go func(messageText string, peerID int64, targetPeer int64) {
					response := h.ProcessMessage(messageText, peerID)
					_, err := h.vkClient.SendMessage(targetPeer, response)
					if err != nil && h.log != nil {
						h.log.ErrorLogf("Failed to send response to peer %d: %v", targetPeer, err)
					}
				}(msg.Text, msg.PeerID, replyPeerID)
			}
		}
	}
}
