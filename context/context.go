package context

import (
	"fmt"
)

// Role определяет роль сообщения в диалоге
type Role string

const (
	SystemRole    Role = "system"
	UserRole      Role = "user"
	AssistantRole Role = "assistant"
)

// Message представляет одно сообщение в истории диалога
type Message struct {
	Role    Role
	Content string
}

// Config содержит настройки контекста
type Config struct {
	// MaxMessages — максимальное количество сообщений в истории
	MaxMessages int
	// KeepSystemMessage — сохранять ли системное сообщение после сброса
	KeepSystemMessage bool
	// SystemPrompt — системный промпт
	SystemPrompt string
}

// DefaultConfig возвращает настройки по умолчанию
func DefaultConfig() Config {
	return Config{
		MaxMessages:       50,
		KeepSystemMessage: true,
		SystemPrompt:      "You are a helpful assistant.",
	}
}

// Manager управляет историей диалога
type Manager struct {
	config   Config
	messages []Message
}

// NewManager создаёт новый менеджер контекста
func NewManager(config Config) *Manager {
	m := &Manager{
		config:   config,
		messages: make([]Message, 0),
	}

	// Добавляем системное сообщение при создании
	if config.KeepSystemMessage && config.SystemPrompt != "" {
		m.messages = append(m.messages, Message{
			Role:    SystemRole,
			Content: config.SystemPrompt,
		})
	}

	return m
}

// AddUserMessage добавляет сообщение пользователя в историю
func (m *Manager) AddUserMessage(content string) {
	m.messages = append(m.messages, Message{
		Role:    UserRole,
		Content: content,
	})
	m.enforceLimits()
}

// AddAssistantMessage добавляет сообщение ассистента в историю
func (m *Manager) AddAssistantMessage(content string) {
	m.messages = append(m.messages, Message{
		Role:    AssistantRole,
		Content: content,
	})
	m.enforceLimits()
}

// GetMessages возвращает все сообщения для отправки в API
func (m *Manager) GetMessages() []Message {
	result := make([]Message, len(m.messages))
	copy(result, m.messages)
	return result
}

// Reset полностью очищает историю, оставляя только системное сообщение
func (m *Manager) Reset() {
	m.messages = m.messages[:0]

	// Восстанавливаем системное сообщение
	if m.config.KeepSystemMessage && m.config.SystemPrompt != "" {
		m.messages = append(m.messages, Message{
			Role:    SystemRole,
			Content: m.config.SystemPrompt,
		})
	}
}

// HistoryLength возвращает количество сообщений (без системного)
func (m *Manager) HistoryLength() int {
	return len(m.messages) - 1 // вычитаем системное сообщение
}

// HistoryText возвращает текстовое представление истории
func (m *Manager) HistoryText() string {
	var result string
	for i, msg := range m.messages {
		result += fmt.Sprintf("%d. [%s]: %s\n", i+1, msg.Role, msg.Content)
	}
	return result
}

// enforceLimits удаляет старые сообщения при превышении лимита
func (m *Manager) enforceLimits() {
	// Пропускаем системное сообщение при проверке лимита
	systemOffset := 0
	if m.config.KeepSystemMessage {
		systemOffset = 1
	}

	// Удаляем самые старые пользовательские сообщения
	for len(m.messages)-systemOffset > m.config.MaxMessages {
		// Ищем первое сообщение не системное
		for i := systemOffset; i < len(m.messages); i++ {
			if m.messages[i].Role != SystemRole {
				m.messages = append(m.messages[:i], m.messages[i+1:]...)
				break
			}
		}
	}
}
