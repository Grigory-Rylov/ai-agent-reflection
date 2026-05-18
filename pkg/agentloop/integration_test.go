package agentloop

import (
	"context"
	"testing"
)

// ============================================================
// Тесты Loop Detection — полный функционал
// ============================================================

func TestLoopDetectionExactDuplicate(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()
	config.EnableLoopDetection = true
	config.LoopThreshold = 0.9

	loop, _ := NewAgentLoop(config, vk, reg)
	al := loop.(*agentLoop)

	// Симулируем добавление одинаковых ответов
	al.checkLoopDetection("Hello, how can I help you?", 123)
	al.checkLoopDetection("Hello, how can I help you?", 123)

	// Второй вызов должен вернуть true (цикл обнаружен)
	// Но т.к. checkLoopDetection очищает историю после обнаружения,
	// третий вызов должен снова начать проверку
}

func TestLoopDetectionNoLoop(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()
	config.EnableLoopDetection = true
	config.LoopThreshold = 0.9

	loop, _ := NewAgentLoop(config, vk, reg)
	al := loop.(*agentLoop)

	// Разные ответы не должны вызывать loop detection
	al.checkLoopDetection("Hello", 123)
	result := al.checkLoopDetection("How are you?", 123)

	if result {
		t.Error("expected no loop detection for different responses")
	}
}

func TestLoopDetectionThreshold(t *testing.T) {
	// Тестируем что similarity работает корректно
	// Одинаковые строки = 1.0
	sim := similarity("test", "test")
	if sim != 1.0 {
		t.Errorf("expected similarity 1.0 for identical strings, got %f", sim)
	}

	// Частичное совпадение
	sim = similarity("hello world", "hello there")
	// "hello" совпадает, "world" != "there"
	if sim <= 0.0 || sim >= 1.0 {
		t.Errorf("expected partial similarity, got %f", sim)
	}
}

// ============================================================
// Тесты Thinking Messages — полный функционал
// ============================================================

func TestThinkingMessageDelivery(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()
	config.EnableThinking = true
	config.ThinkingPeerID = 456

	loop, _ := NewAgentLoop(config, vk, reg)
	al := loop.(*agentLoop)

	// Отправляем thinking сообщение
	al.sendThinking(123, "Processing request...")

	// Проверяем что сообщение отправлено в thinkingPeerID
	thinking := vk.GetThinking()
	if len(thinking) != 1 {
		t.Errorf("expected 1 thinking message, got %d", len(thinking))
	}

	// Проверяем формат сообщения
	if thinking[0] == "" {
		t.Error("expected non-empty thinking message")
	}
}

func TestThinkingDisabled(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()
	config.EnableThinking = false

	loop, _ := NewAgentLoop(config, vk, reg)
	al := loop.(*agentLoop)

	// Не должно отправлять thinking сообщение
	al.sendThinking(123, "Thinking...")

	thinking := vk.GetThinking()
	if len(thinking) != 0 {
		t.Errorf("expected no thinking messages when disabled, got %d", len(thinking))
	}
}

func TestThinkingNoPeerID(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()
	config.EnableThinking = true
	config.ThinkingPeerID = 0 // Не установлен

	loop, _ := NewAgentLoop(config, vk, reg)
	al := loop.(*agentLoop)

	// Не должно отправлять thinking сообщение
	al.sendThinking(123, "Thinking...")

	thinking := vk.GetThinking()
	if len(thinking) != 0 {
		t.Errorf("expected no thinking messages when peerID is 0, got %d", len(thinking))
	}
}

// ============================================================
// Тесты Tool Processing — полный функционал
// ============================================================

func TestToolProcessingMultipleCalls(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()

	loop, _ := NewAgentLoop(config, vk, reg)
	al := loop.(*agentLoop)

	toolCalls := []map[string]interface{}{
		{"name": "file_read", "arguments": `{"path": "/test1"}`},
		{"name": "time_get", "arguments": `{}`},
		{"name": "dir_list", "arguments": `{"path": "/test2"}`},
	}

	results, err := al.processToolCalls(context.Background(), toolCalls, nil, 123)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

func TestToolProcessingLogging(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()
	config.EnableLogging = true

	loop, _ := NewAgentLoop(config, vk, reg)
	al := loop.(*agentLoop)

	toolCalls := []map[string]interface{}{
		{"name": "test_tool", "arguments": `{}`},
	}

	// Должно логировать обработку
	results, _ := al.processToolCalls(context.Background(), toolCalls, nil, 123)

	// Проверяем что есть результат
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}
