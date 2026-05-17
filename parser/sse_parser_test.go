package parser

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// ============================================================
// Тесты парсинга одной строки
// ============================================================

func TestParseSSELine(t *testing.T) {
	t.Run("parses delta event", func(t *testing.T) {
		line := `data: {"choices":[{"finish_reason":null,"index":0,"delta":{"role":"assistant","content":"Go"}}],"created":1779001107,"id":"test","model":"qwen","object":"chat.completion.chunk"}`
		
		chunk, err := ParseSSELine(line)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		
		if chunk.Content != "Go" {
			t.Errorf("expected content 'Go', got '%s'", chunk.Content)
		}
		if chunk.Delta.Role != "assistant" {
			t.Errorf("expected role 'assistant', got '%s'", chunk.Delta.Role)
		}
		if chunk.FinishReason != nil {
			t.Error("expected nil finish_reason")
		}
	})

	t.Run("parses stop event", func(t *testing.T) {
		line := `data: {"choices":[{"finish_reason":"stop","index":0,"delta":{}}],"created":1779001107,"id":"test","model":"qwen","object":"chat.completion.chunk","timings":{"prompt_n":30,"prompt_ms":134.52,"prompt_per_token_ms":4.48,"prompt_per_second":223.01,"predicted_n":200,"predicted_ms":3738.41,"predicted_per_token_ms":18.69,"predicted_per_second":53.49}}`
		
		chunk, err := ParseSSELine(line)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		
		if chunk.FinishReason == nil {
			t.Fatal("expected non-nil finish_reason")
		}
		if *chunk.FinishReason != "stop" {
			t.Errorf("expected finish_reason 'stop', got '%s'", *chunk.FinishReason)
		}
	})

	t.Run("parses reasoning content", func(t *testing.T) {
		line := `data: {"choices":[{"finish_reason":null,"index":0,"delta":{"reasoning_content":"Let"}}],"created":1779001107,"id":"test","model":"qwen","object":"chat.completion.chunk"}`
		
		chunk, err := ParseSSELine(line)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		
		if chunk.ReasoningContent != "Let" {
			t.Errorf("expected reasoning_content 'Let', got '%s'", chunk.ReasoningContent)
		}
	})

	t.Run("returns error for invalid format", func(t *testing.T) {
		_, err := ParseSSELine("invalid line")
		if err == nil {
			t.Error("expected error for invalid format")
		}
	})

	t.Run("returns error for invalid JSON", func(t *testing.T) {
		_, err := ParseSSELine("data: invalid json")
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})
}

// ============================================================
// Тесты EventType
// ============================================================

func TestSSEChunkEventType(t *testing.T) {
	t.Run("delta event type", func(t *testing.T) {
		chunk := SSEChunk{Content: "Hello"}
		if chunk.EventType() != EventDelta {
			t.Errorf("expected EventDelta, got %s", chunk.EventType())
		}
	})

	t.Run("reasoning event type", func(t *testing.T) {
		chunk := SSEChunk{ReasoningContent: "thinking..."}
		if chunk.EventType() != EventReasoning {
			t.Errorf("expected EventReasoning, got %s", chunk.EventType())
		}
	})

	t.Run("stop event type", func(t *testing.T) {
		reason := "stop"
		chunk := SSEChunk{FinishReason: &reason}
		if chunk.EventType() != EventStop {
			t.Errorf("expected EventStop, got %s", chunk.EventType())
		}
	})
}

func TestSSEChunkIsCompletion(t *testing.T) {
	t.Run("is completion when finish_reason set", func(t *testing.T) {
		reason := "stop"
		chunk := SSEChunk{FinishReason: &reason}
		if !chunk.IsCompletion() {
			t.Error("expected IsCompletion to be true")
		}
	})

	t.Run("not completion when no finish_reason", func(t *testing.T) {
		chunk := SSEChunk{Content: "Hello"}
		if chunk.IsCompletion() {
			t.Error("expected IsCompletion to be false")
		}
	})
}

func TestSSEChunkIsStopReason(t *testing.T) {
	t.Run("stop reason", func(t *testing.T) {
		reason := "stop"
		chunk := SSEChunk{FinishReason: &reason}
		if !chunk.IsStopReason() {
			t.Error("expected IsStopReason to be true for 'stop'")
		}
	})

	t.Run("length reason", func(t *testing.T) {
		reason := "length"
		chunk := SSEChunk{FinishReason: &reason}
		if !chunk.IsStopReason() {
			t.Error("expected IsStopReason to be true for 'length'")
		}
	})

	t.Run("not stop reason", func(t *testing.T) {
		chunk := SSEChunk{Content: "Hello"}
		if chunk.IsStopReason() {
			t.Error("expected IsStopReason to be false")
		}
	})
}

// ============================================================
// Тесты CountChunksByType
// ============================================================

func TestCountChunksByType(t *testing.T) {
	chunks := []SSEChunk{
		{Content: "Go"},
		{Content: " is"},
		{ReasoningContent: "thinking"},
		{Content: " a"},
		{ReasoningContent: "language"},
		{Content: "."},
	}

	delta, reasoning, stop := CountChunksByType(chunks)

	// 4 delta (Go, is, a, .) + 2 reasoning (thinking, language)
	if delta != 4 {
		t.Errorf("expected 4 deltas, got %d", delta)
	}
	if reasoning != 2 {
		t.Errorf("expected 2 reasoning, got %d", reasoning)
	}
	if stop != 0 {
		t.Errorf("expected 0 stops, got %d", stop)
	}
}

// ============================================================
// Тесты ExtractContent и ExtractReasoning
// ============================================================

func TestExtractContent(t *testing.T) {
	chunks := []SSEChunk{
		{Content: "Go"},
		{Content: " is"},
		{Content: " a"},
		{Content: " language"},
		{Content: "."},
	}

	content := ExtractContent(chunks)
	if content != "Go is a language." {
		t.Errorf("expected 'Go is a language.', got '%s'", content)
	}
}

func TestExtractReasoning(t *testing.T) {
	chunks := []SSEChunk{
		{ReasoningContent: "Let "},
		{ReasoningContent: "me "},
		{ReasoningContent: "think"},
	}

	reasoning := ExtractReasoning(chunks)
	if reasoning != "Let me think" {
		t.Errorf("expected 'Let me think', got '%s'", reasoning)
	}
}

// ============================================================
// Тесты с реальными данными от llama-server
// ============================================================

func TestParseRealSSEStream(t *testing.T) {
	// Читаем сохранённый SSE-поток
	file, err := os.Open("../test_data/llama_sse_stream.txt")
	if err != nil {
		t.Fatalf("failed to open test data: %v", err)
	}
	defer file.Close()

	chunks, finalEvent, err := NewParser(file).ParseStream()
	if err != nil {
		t.Fatalf("failed to parse stream: %v", err)
	}

	// Проверяем что поток завершён
	if finalEvent != EventDone {
		t.Errorf("expected final event EventDone, got %s", finalEvent)
	}

	// Считаем по типам
	delta, reasoning, stop := CountChunksByType(chunks)

	t.Logf("Total chunks: %d", len(chunks))
	t.Logf("Delta events: %d", delta)
	t.Logf("Reasoning events: %d", reasoning)
	t.Logf("Stop events: %d", stop)

	// Проверяем что есть чанки
	if len(chunks) == 0 {
		t.Error("expected non-empty chunk list")
	}

	// Проверяем что есть stop-событие в конце
	if stop == 0 {
		t.Error("expected at least one stop event")
	}

	// Проверяем что есть контент
	content := ExtractContent(chunks)
	if content == "" {
		t.Error("expected non-empty content")
	}

	maxLen := 100
	if len(content) < maxLen {
		maxLen = len(content)
	}
	t.Logf("Extracted content (%d chars): %s...", len(content), content[:maxLen])
}

func TestParseSSELineFromRealStream(t *testing.T) {
	// Читаем первые несколько строк из реального потока
	file, err := os.Open("../test_data/llama_sse_stream.txt")
	if err != nil {
		t.Fatalf("failed to open test data: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	
	var lineCount int
	var firstChunk SSEChunk
	var parsedChunks int
	
	for scanner.Scan() {
		line := scanner.Text()
		lineCount++
		
		if line == "" {
			continue
		}
		
		if strings.HasPrefix(line, "data: ") {
			// Пропускаем [DONE]
			if strings.Contains(line, "[DONE]") {
				continue
			}
			
			chunk, err := ParseSSELine(line)
			if err != nil {
				t.Fatalf("failed to parse line %d: %v", lineCount, err)
			}
			
			parsedChunks++
			
			if firstChunk.ID == "" {
				firstChunk = chunk
			}
		}
	}
	
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	t.Logf("Read %d lines, parsed %d chunks", lineCount, parsedChunks)
	t.Logf("First chunk ID: %s", firstChunk.ID)
	t.Logf("First chunk model: %s", firstChunk.Model)
}
