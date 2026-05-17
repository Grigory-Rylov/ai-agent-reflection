package vk

import (
	"context"
	"sync"
	"time"

	"github.com/opencode/llama-client/pkg/agent"
	"github.com/opencode/llama-client/pkg/logger"
	"github.com/opencode/llama-client/session"
)

// ============================================================
// VK Bot Handler — связующее звено между VK Bot API и AI Agent
// ============================================================

// BotHandler управляет взаимодействием с пользователями через VK Bot
type BotHandler struct {
	vkClient  *BotClient
	aiAgent   agent.Agent
	log       *logger.Logger
	sessions  map[int64]*session.Session
	sessionMu sync.RWMutex
}

// ============================================================
// Инициализация
// ============================================================

// NewBotHandler создаёт новый обработчик VK Bot
func NewBotHandler(vkClient *BotClient, aiAgent agent.Agent, log *logger.Logger) *BotHandler {
	return &BotHandler{
		vkClient: vkClient,
		aiAgent:  aiAgent,
		log:      log,
		sessions: make(map[int64]*session.Session),
	}
}

// ============================================================
// Обработка сообщений
// ============================================================

// ProcessMessage обрабатывает входящее сообщение от пользователя
func (h *BotHandler) ProcessMessage(message string, peerID int64) string {
	// Обновляем сессию
	h.ensureSession(peerID)

	// Проверяем команды
	if result := h.handleCommand(message, peerID); result != "" {
		return result
	}

	// Проверяем, не зациклилась ли AI
	s := h.getSession(peerID)
	if s != nil && s.IsLoopDetected() {
		// Получаем alert-сообщение и добавляем его в контекст
		alert := s.GetLoopAlertMessage()
		if alert != "" {
			message = "[LOOP DETECTED] " + alert + "\n\n" + message
		}
	}

	// Обрабатываем сообщение через AI Agent
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

	// Обновляем сессию
	h.ensureSession(peerID)

	// Проверяем команды
	if result := h.handleCommand(message, peerID); result != "" {
		return result
	}

	// Проверяем, не зациклилась ли AI
	s := h.getSession(peerID)
	if s != nil && s.IsLoopDetected() {
		alert := s.GetLoopAlertMessage()
		if alert != "" {
			message = "[LOOP DETECTED] " + alert + "\n\n" + message
		}
	}

	// Обрабатываем сообщение через AI Agent
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
		if h.log != nil {
			h.log.InfoLogf("User %d reset session", peerID)
		}
		return "История диалога очищена."

	case "/help":
		return "Доступные команды:\n" +
			"/reset - Очистить историю диалога\n" +
			"/help - Показать эту справку\n" +
			"/status - Показать статус агента"

	case "/status":
		// Показываем статус сессии
		s := h.getSession(peerID)
		status := "AI Agent активен и готов к работе."
		if s != nil {
			status += "\nСессия: peer_id=" + string(rune(peerID)) +
				"\nСообщений в истории: " + string(rune(s.HistoryLength()))
		}
		return status

	default:
		return ""
	}
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

// Start запускает цикл обработки сообщений через long polling
func (h *BotHandler) Start(ctx context.Context) error {
	// Получаем параметры long polling
	server, key, ts, err := h.vkClient.GetLongPollServer()
	if err != nil {
		return err
	}

	if h.log != nil {
		h.log.InfoLog("Long Polling server started")
	}

	// Запускаем цикл проверки обновлений
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if h.log != nil {
				h.log.InfoLog("Bot handler stopped")
			}
			return nil
		case <-ticker.C:
			// Периодическая очистка сессий
			h.cleanupInactiveSessions()

			// Получаем обновления
			messages, newTs, err := h.vkClient.CheckUpdates(server, key, ts)
			if err != nil {
				if h.log != nil {
					h.log.WarnLogf("Failed to check updates: %v", err)
				}
				continue
			}

			// Обновляем ts
			ts = newTs

			// Обрабатываем каждое сообщение
			for _, msg := range messages {
				if h.log != nil {
					h.log.InfoLogf("Received message from peer %d: %s", msg.PeerID, msg.Text)
				}

				// Обрабатываем сообщение в отдельной goroutine
				go func(messageText string, peerID int64) {
					response := h.ProcessMessage(messageText, peerID)

					// Отправляем ответ
					_, err := h.vkClient.SendMessage(peerID, response)
					if err != nil && h.log != nil {
						h.log.ErrorLogf("Failed to send response to peer %d: %v", peerID, err)
					}
				}(msg.Text, msg.PeerID)
			}
		}
	}
}
