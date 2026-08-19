package agentloop

import (
	"context"
	"testing"
)


func TestLoopDetectionExactDuplicate(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()
	config.ModelHolder = testHolder()
	config.EnableLoopDetection = true
	config.LoopThreshold = 0.9

	loop, _ := NewAgentLoop(config, vk, reg)
	al := loop.(*agentLoop)

	
	al.checkLoopDetection("Hello, how can I help you?", 123)
	al.checkLoopDetection("Hello, how can I help you?", 123)

	
	
	
}

func TestLoopDetectionNoLoop(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()
	config.ModelHolder = testHolder()
	config.EnableLoopDetection = true
	config.LoopThreshold = 0.9

	loop, _ := NewAgentLoop(config, vk, reg)
	al := loop.(*agentLoop)

	
	al.checkLoopDetection("Hello", 123)
	result := al.checkLoopDetection("How are you?", 123)

	if result {
		t.Error("expected no loop detection for different responses")
	}
}

func TestLoopDetectionThreshold(t *testing.T) {
	
	
	sim := similarity("test", "test")
	if sim != 1.0 {
		t.Errorf("expected similarity 1.0 for identical strings, got %f", sim)
	}

	
	sim = similarity("hello world", "hello there")
	
	if sim <= 0.0 || sim >= 1.0 {
		t.Errorf("expected partial similarity, got %f", sim)
	}
}


func TestThinkingMessageDelivery(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()
	config.ModelHolder = testHolder()
	config.EnableThinking = true
	config.ThinkingPeerID = 456

	loop, _ := NewAgentLoop(config, vk, reg)
	al := loop.(*agentLoop)

	
	al.sendThinking(123, "Processing request...")

	
	thinking := vk.GetThinking()
	if len(thinking) != 1 {
		t.Errorf("expected 1 thinking message, got %d", len(thinking))
	}

	
	if thinking[0] == "" {
		t.Error("expected non-empty thinking message")
	}
}

func TestThinkingDisabled(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()
	config.ModelHolder = testHolder()
	config.EnableThinking = false

	loop, _ := NewAgentLoop(config, vk, reg)
	al := loop.(*agentLoop)

	
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
	config.ModelHolder = testHolder()
	config.EnableThinking = true
	config.ThinkingPeerID = 0 

	loop, _ := NewAgentLoop(config, vk, reg)
	al := loop.(*agentLoop)

	
	al.sendThinking(123, "Thinking...")

	thinking := vk.GetThinking()
	if len(thinking) != 0 {
		t.Errorf("expected no thinking messages when peerID is 0, got %d", len(thinking))
	}
}


func TestToolProcessingMultipleCalls(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()
	config.ModelHolder = testHolder()

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
	config.ModelHolder = testHolder()
	config.EnableLogging = true

	loop, _ := NewAgentLoop(config, vk, reg)
	al := loop.(*agentLoop)

	toolCalls := []map[string]interface{}{
		{"name": "test_tool", "arguments": `{}`},
	}

	
	results, _ := al.processToolCalls(context.Background(), toolCalls, nil, 123)

	
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}
