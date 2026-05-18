package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/opencode/llama-client/session"
)

// ============================================================
// Mock LLM Server — перехватывает запросы и проверяет их
// ============================================================

// LLMRequestCapturer захватывает и валидирует запросы к LLM
type LLMRequestCapturer struct {
	mu          sync.Mutex
	requests    []capturedRequest
	responseFn  func(req CapturedRequest) (interface{}, []byte)
	streamFn    func(req CapturedRequest) io.ReadCloser
}

type capturedRequest struct {
	Method  string
	URL     string
	Body    map[string]interface{}
	Headers http.Header
}

// CapturedRequest — публичная структура для тестов
type CapturedRequest struct {
	Method    string
	URL       string
	HasModel  bool
	ModelName string
	HasTools  bool
	HasStream bool
	StreamVal bool
	Messages  []Message
}

func NewLLMRequestCapturer() *LLMRequestCapturer {
	return &LLMRequestCapturer{
		requests: make([]capturedRequest, 0),
	}
}

// CaptureRequest сохраняет захваченный запрос
func (c *LLMRequestCapturer) CaptureRequest(req *http.Request, body []byte) CapturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()

	var bodyMap map[string]interface{}
	json.Unmarshal(body, &bodyMap)

	captured := capturedRequest{
		Method:  req.Method,
		URL:     req.URL.String(),
		Body:    bodyMap,
		Headers: req.Header,
	}
	c.requests = append(c.requests, captured)

	// Конвертируем в публичную структуру
	capturedPub := CapturedRequest{
		Method:  req.Method,
		URL:     req.URL.String(),
		Messages: make([]Message, 0),
	}

	if model, ok := bodyMap["model"]; ok {
		capturedPub.HasModel = true
		capturedPub.ModelName = model.(string)
	}

	if tools, ok := bodyMap["tools"]; ok {
		capturedPub.HasTools = len(tools.([]interface{})) > 0
	}

	if stream, ok := bodyMap["stream"]; ok {
		capturedPub.HasStream = true
		switch v := stream.(type) {
		case bool:
			capturedPub.StreamVal = v
		case float64:
			capturedPub.StreamVal = v == 1
		}
	}

	if msgs, ok := bodyMap["messages"]; ok {
		msgBytes, _ := json.Marshal(msgs)
		json.Unmarshal(msgBytes, &capturedPub.Messages)
	}

	return capturedPub
}

// GetCapturedRequests возвращает все захваченные запросы
func (c *LLMRequestCapturer) GetCapturedRequests() []CapturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()

	result := make([]CapturedRequest, len(c.requests))
	for i, req := range c.requests {
		r := CapturedRequest{
			Method: req.Method,
			URL:    req.URL,
		}

		if model, ok := req.Body["model"]; ok {
			r.HasModel = true
			r.ModelName = model.(string)
		}

		if tools, ok := req.Body["tools"]; ok {
			r.HasTools = len(tools.([]interface{})) > 0
		}

		if stream, ok := req.Body["stream"]; ok {
			r.HasStream = true
			switch v := stream.(type) {
			case bool:
				r.StreamVal = v
			case float64:
				r.StreamVal = v == 1
			}
		}

		if msgs, ok := req.Body["messages"]; ok {
			msgBytes, _ := json.Marshal(msgs)
			json.Unmarshal(msgBytes, &r.Messages)
		}

		result[i] = r
	}
	return result
}

// NewTestServerWithCapturer создаёт тестовый сервер который захватывает запросы
func NewTestServerWithCapturer(capturer *LLMRequestCapturer) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured := capturer.CaptureRequest(r, body)

		// Проверяем есть ли custom response handler
		if capturer.responseFn != nil {
			result, _ := capturer.responseFn(captured)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(result)
			return
		}

		// Default response — простой текст без tools
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"finish_reason":"stop","index":0,"message":{"role":"assistant","content":"Hello"}}]}`))
	}))
}

// ============================================================
// Тесты
// ============================================================

func TestStreamingRequestDoesNotSendModel(t *testing.T) {
	capturer := NewLLMRequestCapturer()
	server := NewTestServerWithCapturer(capturer)
	defer server.Close()

	// Создаём агента с mock-сервером
	config := Config{
		LlamaServerURL:   server.URL,
		Model:            "qwen3.6", // Модель указана в конфиге
		MaxTokens:        100,
		Temperature:      0.7,
		SessionConfig:    session.DefaultConfig(),
		EnableTools:      false,
	}

	agent := NewAgent(config)

	// Отправляем сообщение
	ctx := context.Background()
	_, err := agent.ProcessMessage(ctx, "Привет", 123)

	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	// Проверяем что модель НЕ была отправлена
	requests := capturer.GetCapturedRequests()
	if len(requests) == 0 {
		t.Fatal("No requests captured")
	}

	// Должен быть streaming запрос
	if !requests[0].HasStream {
		t.Fatal("Expected streaming request (stream=true)")
	}

	// Ключевая проверка: модель НЕ должна быть в запросе
	if requests[0].HasModel {
		t.Errorf("FAIL: Model field should NOT be in request, but found: %s", requests[0].ModelName)
	} else {
		t.Log("PASS: Model field correctly omitted from request")
	}
}

func TestNonStreamingRequestDoesNotSendModel(t *testing.T) {
	_ = NewLLMRequestCapturer() // unused, testing buildNonStreamingRequestJSON directly
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		// Парсим запрос
		var bodyMap map[string]interface{}
		json.Unmarshal(body, &bodyMap)

		// Проверяем что нет модели
		if _, hasModel := bodyMap["model"]; hasModel {
			t.Errorf("FAIL: Model field should NOT be in non-streaming request")
		}

		// Отвечаем
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"finish_reason":"stop","index":0,"message":{"role":"assistant","content":"Hi"}}]}`))
	}))
	defer server.Close()

	// Тестируем buildNonStreamingRequestJSON напрямую
	agentConfig := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
	}

	agent := &agentImpl{
		config: agentConfig,
	}

	messages := []Message{
		{Role: "user", Content: "Test"},
	}

	jsonData := agent.buildNonStreamingRequestJSON(messages, nil)

	// Проверяем JSON
	var bodyMap map[string]interface{}
	json.Unmarshal(jsonData, &bodyMap)

	if _, hasModel := bodyMap["model"]; hasModel {
		t.Errorf("FAIL: Model field should NOT be in non-streaming request JSON")
	} else {
		t.Log("PASS: Model field correctly omitted from non-streaming JSON")
	}

	if _, hasStream := bodyMap["stream"]; !hasStream {
		t.Error("FAIL: stream field should be present")
	}

	if bodyMap["stream"] != false {
		t.Errorf("FAIL: stream should be false, got %v", bodyMap["stream"])
	}

	t.Logf("Request JSON: %s", string(jsonData))
}

func TestToolsRequestDoesNotSendModel(t *testing.T) {
	capturer := NewLLMRequestCapturer()
	server := NewTestServerWithCapturer(capturer)
	defer server.Close()

	// Создаём агента с включёнными tools
	config := Config{
		LlamaServerURL:   server.URL,
		Model:            "qwen3.6",
		MaxTokens:        100,
		Temperature:      0.7,
		SessionConfig:    session.DefaultConfig(),
		EnableTools:      true,
		MaxToolCalls:     5,
	}

	agent := NewAgent(config)

	ctx := context.Background()
	_, err := agent.ProcessMessage(ctx, "Какое время?", 123)

	// Ожидаем ошибку (mock не возвращает tool calls), но нас интересует запрос
	t.Logf("ProcessMessage result: %v (expected to fail on mock)", err)

	requests := capturer.GetCapturedRequests()
	if len(requests) == 0 {
		t.Fatal("No requests captured")
	}

	// Первый запрос — streaming с tools
	t.Logf("Request 1: HasTools=%v, HasModel=%v, Stream=%v",
		requests[0].HasTools, requests[0].HasModel, requests[0].StreamVal)

	if requests[0].HasModel {
		t.Errorf("FAIL: Model field should NOT be in tools streaming request")
	} else {
		t.Log("PASS: Model field correctly omitted from tools streaming request")
	}
}

func TestRequestJSONStructure(t *testing.T) {
	// Прямое тестирование buildRequestJSON
	agentConfig := Config{
		LlamaServerURL: "http://localhost:8081",
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
	}

	agent := &agentImpl{
		config: agentConfig,
	}

	// Streaming запрос
	streamConfig := StreamingConfig{
		Model:       "test-model",
		MaxTokens:   100,
		Temperature: 0.7,
		Stream:      true,
	}

	messages := []Message{
		{Role: "user", Content: "Hello"},
	}

	jsonData := agent.buildRequestJSON(streamConfig, messages)

	var bodyMap map[string]interface{}
	json.Unmarshal(jsonData, &bodyMap)

	t.Logf("Streaming request JSON: %s", string(jsonData))

	// Проверяем структуру
	if _, hasModel := bodyMap["model"]; hasModel {
		t.Error("FAIL: Model field should NOT be in streaming request JSON")
	} else {
		t.Log("PASS: Model field correctly omitted from streaming JSON")
	}

	if _, hasStream := bodyMap["stream"]; !hasStream {
		t.Error("FAIL: stream field should be present")
	}

	if bodyMap["stream"] != true {
		t.Errorf("FAIL: stream should be true, got %v", bodyMap["stream"])
	}

	if _, hasMessages := bodyMap["messages"]; !hasMessages {
		t.Error("FAIL: messages field should be present")
	}

	if _, hasTemperature := bodyMap["temperature"]; !hasTemperature {
		t.Error("FAIL: temperature field should be present")
	}

	if _, hasMaxTokens := bodyMap["max_tokens"]; !hasMaxTokens {
		t.Error("FAIL: max_tokens field should be present")
	}
}
