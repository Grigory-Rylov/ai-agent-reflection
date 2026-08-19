package context

import (
	"fmt"
)


type Role string

const (
	SystemRole    Role = "system"
	UserRole      Role = "user"
	AssistantRole Role = "assistant"
)


type Message struct {
	Role    Role
	Content string
}


type Config struct {
	
	MaxMessages int
	
	KeepSystemMessage bool
	
	SystemPrompt string
}


func DefaultConfig() Config {
	return Config{
		MaxMessages:       50,
		KeepSystemMessage: true,
		SystemPrompt:      "You are a helpful assistant.",
	}
}


type Manager struct {
	config   Config
	messages []Message
}


func NewManager(config Config) *Manager {
	m := &Manager{
		config:   config,
		messages: make([]Message, 0),
	}

	
	if config.KeepSystemMessage && config.SystemPrompt != "" {
		m.messages = append(m.messages, Message{
			Role:    SystemRole,
			Content: config.SystemPrompt,
		})
	}

	return m
}


func (m *Manager) AddUserMessage(content string) {
	m.messages = append(m.messages, Message{
		Role:    UserRole,
		Content: content,
	})
	m.enforceLimits()
}


func (m *Manager) AddAssistantMessage(content string) {
	m.messages = append(m.messages, Message{
		Role:    AssistantRole,
		Content: content,
	})
	m.enforceLimits()
}


func (m *Manager) GetMessages() []Message {
	result := make([]Message, len(m.messages))
	copy(result, m.messages)
	return result
}


func (m *Manager) Reset() {
	m.messages = m.messages[:0]

	
	if m.config.KeepSystemMessage && m.config.SystemPrompt != "" {
		m.messages = append(m.messages, Message{
			Role:    SystemRole,
			Content: m.config.SystemPrompt,
		})
	}
}


func (m *Manager) HistoryLength() int {
	return len(m.messages) - 1 
}


func (m *Manager) HistoryText() string {
	var result string
	for i, msg := range m.messages {
		result += fmt.Sprintf("%d. [%s]: %s\n", i+1, msg.Role, msg.Content)
	}
	return result
}


func (m *Manager) enforceLimits() {
	
	systemOffset := 0
	if m.config.KeepSystemMessage {
		systemOffset = 1
	}

	
	for len(m.messages)-systemOffset > m.config.MaxMessages {
		
		for i := systemOffset; i < len(m.messages); i++ {
			if m.messages[i].Role != SystemRole {
				m.messages = append(m.messages[:i], m.messages[i+1:]...)
				break
			}
		}
	}
}
