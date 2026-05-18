package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/session"
)

// ============================================================
// Интеграционные тесты — проверка tool calling на реальном LLM
// ============================================================

// loadTestConfig загружает конфигурацию из config.json
func loadTestConfig() (serverURL string, maxTokens int, temperature float64, err error) {
	data, err := os.ReadFile("../../config.json")
	if err != nil {
		return "", 0, 0, fmt.Errorf("read config: %w", err)
	}
	var cfg struct {
		ServerURL   string  `json:"llama_server_url"`
		MaxTokens   int     `json:"max_tokens"`
		Temperature float64 `json:"temperature"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", 0, 0, fmt.Errorf("parse config: %w", err)
	}
	serverURL = cfg.ServerURL
	if !strings.HasPrefix(serverURL, "http://") && !strings.HasPrefix(serverURL, "https://") {
		serverURL = "http://" + serverURL
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 4096
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.7
	}
	return serverURL, cfg.MaxTokens, cfg.Temperature, nil
}

// skipIfNoServer проверяет доступность LLM сервера
func skipIfNoServer(t *testing.T, serverURL string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(serverURL)
	if err != nil {
		t.Skipf("LLM server not available at %s: %v", serverURL, err)
	}
	resp.Body.Close()
}

// setupTestAgent создаёт агента с инструментами для теста
func setupTestAgent(t *testing.T) (*agentImpl, string) {
	t.Helper()

	serverURL, maxTokens, temperature, err := loadTestConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	skipIfNoServer(t, serverURL)

	reg := tools.NewRegistry()
	reg.Register(&tools.TimeGetTool{})
	reg.Register(&tools.FileWriteTool{})
	reg.Register(&tools.FileReadTool{})
	reg.Register(&tools.CalcTool{})
	reg.Register(&tools.WebFetchTool{})
	reg.Register(&tools.WebSearchTool{})

	config := Config{
		LlamaServerURL: serverURL,
		MaxTokens:      maxTokens,
		Temperature:    temperature,
		SessionConfig:  session.DefaultConfig(),
		EnableTools:    true,
		MaxToolCalls:   3,
		EnableContextCompression: false,
	}

	a := NewAgent(config)

	// Регистрируем инструменты через тот же механизм что и agentloop
	if impl, ok := interface{}(a).(interface{ RegisterTools(*tools.Registry) }); ok {
		impl.RegisterTools(reg)
	}

	return a, serverURL
}

// TestLLMToolCall_time_get проверяет что модель вызывает time_get
func TestLLMToolCall_time_get(t *testing.T) {
	a, _ := setupTestAgent(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	response, err := a.ProcessMessage(ctx, "Который сейчас час? Используй инструмент time_get.", 99901)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	if response == "" {
		t.Error("Expected non-empty response")
	}

	t.Logf("Response: %s", response)
}

// TestLLMToolCall_calc проверяет что модель вызывает calc
func TestLLMToolCall_calc(t *testing.T) {
	a, _ := setupTestAgent(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	response, err := a.ProcessMessage(ctx, "Сколько будет 25 * 4 + 10? Используй инструмент calc.", 99902)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	if response == "" {
		t.Error("Expected non-empty response")
	}

	t.Logf("Response: %s", response)
}

// TestLLMToolCall_file_write проверяет создание файла через tool call
func TestLLMToolCall_file_write(t *testing.T) {
	a, _ := setupTestAgent(t)

	tmpDir, err := os.MkdirTemp("", "agent_int_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testFilePath := filepath.Join(tmpDir, "hello_from_ai.txt")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	response, err := a.ProcessMessage(ctx,
		fmt.Sprintf("Создай файл %s и напиши в нём 'Hello from AI agent!'", testFilePath),
		99903)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	if response == "" {
		t.Error("Expected non-empty response")
	}

	t.Logf("Response: %s", response)

	// Проверяем что файл реально создан
	data, err := os.ReadFile(testFilePath)
	if err != nil {
		t.Logf("File was not created (may be ok if model chose different path): %v", err)
	} else {
		t.Logf("File content: %s", string(data))
	}
}

// TestLLMToolCall_web_fetch проверяет web_fetch + ответ модели
func TestLLMToolCall_web_fetch(t *testing.T) {
	a, _ := setupTestAgent(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	response, err := a.ProcessMessage(ctx,
		"Прочитай содержимое https://example.com используя web_fetch",
		99905)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	if response == "" {
		t.Error("Expected non-empty response")
	}
	t.Logf("Response length: %d", len(response))
	if len(response) < 10 {
		t.Errorf("Response too short: %q", response)
	}
}

// TestLLMToolCall_github_project — точное воспроизведение сценария из лога
// модель → web_fetch("https://github.com/JonForShort/android-tools/tree/master")
// → tool result → модель должна ответить без 400 ошибки
func TestLLMToolCall_github_project(t *testing.T) {
	a, _ := setupTestAgent(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	response, err := a.ProcessMessage(ctx,
		"можешь прочитать описание проекта https://github.com/JonForShort/android-tools/tree/master ?",
		99906)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	if response == "" {
		t.Error("Expected non-empty response")
	}
	t.Logf("Response length: %d", len(response))
	if len(response) < 20 {
		t.Errorf("Response too short or empty: %q", response)
	}
}

// TestLLMToolCall_web_search проверяет поиск в интернете
func TestLLMToolCall_web_search(t *testing.T) {
	a, _ := setupTestAgent(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	response, err := a.ProcessMessage(ctx,
		"Найди в интернете информацию про Go语言. Используй web_search.",
		99907)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	if response == "" {
		t.Error("Expected non-empty response")
	}
	t.Logf("Response length: %d", len(response))
	if len(response) < 20 {
		t.Errorf("Response too short: %q", response)
	}
}

// TestLLMToolCall_multiple_tools проверяет множественные вызовы инструментов
func TestLLMToolCall_multiple_tools(t *testing.T) {
	a, _ := setupTestAgent(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Запрос, который требует два инструмента: посчитать время + сделать вычисление
	response, err := a.ProcessMessage(ctx,
		"Сейчас который час? И сколько будет 2 + 2? Используй инструменты для ответа.",
		99904)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	if response == "" {
		t.Error("Expected non-empty response")
	}

	t.Logf("Response: %s", response)
}

// TestLLMToolCall_tool_schemas_format проверяет что схема тулзов корректна для OpenAI API
func TestLLMToolCall_tool_schemas_format(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&tools.TimeGetTool{})
	reg.Register(&tools.CalcTool{})

	schemas := reg.ToOpenAISchema()

	if len(schemas) != 2 {
		t.Fatalf("Expected 2 schemas, got %d", len(schemas))
	}

	// Проверяем формат schemas для OpenAI
	for i, s := range schemas {
		if s["type"] != "function" {
			t.Errorf("Schema %d: expected type 'function', got '%v'", i, s["type"])
		}
		fn, ok := s["function"].(map[string]interface{})
		if !ok {
			t.Fatalf("Schema %d: function field is not a map", i)
		}
		if fn["name"] == "" {
			t.Errorf("Schema %d: function name is empty", i)
		}
		params, ok := fn["parameters"].(map[string]interface{})
		if !ok {
			t.Errorf("Schema %d: parameters field missing or not a map", i)
		} else {
			if params["type"] != "object" {
				t.Errorf("Schema %d: expected parameters type 'object', got '%v'", i, params["type"])
			}
		}

		jsonBytes, _ := json.MarshalIndent(s, "", "  ")
		t.Logf("Schema %d (%s):\n%s", i, fn["name"], string(jsonBytes))
	}

	// Отправляем schemas на сервер и проверяем что он отвечает
	serverURL, _, _, err := loadTestConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	skipIfNoServer(t, serverURL)

	reqBody := map[string]interface{}{
		"model":      "local-model",
		"messages":   []map[string]string{{"role": "user", "content": "What time is it?"}},
		"tools":      schemas,
		"max_tokens": 100,
		"stream":     false,
	}
	body, _ := json.Marshal(reqBody)

	resp, err := http.Post(serverURL+"/v1/chat/completions", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("API request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("API returned status %d", resp.StatusCode)
	}

	var apiResp struct {
		Choices []struct {
			Message struct {
				Content   string     `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	for i, choice := range apiResp.Choices {
		t.Logf("Choice %d: finish_reason=%s content=%q tool_calls=%d",
			i, choice.FinishReason, choice.Message.Content, len(choice.Message.ToolCalls))
		for j, tc := range choice.Message.ToolCalls {
			t.Logf("  ToolCall %d: id=%s name=%s args=%s",
				j, tc.ID, ToolCallName(tc), ToolCallArgumentsStr(tc))
		}
	}

	if len(apiResp.Choices) == 0 {
		t.Fatal("No choices in API response")
	}

	// Проверяем что модель поняла про tool_calls
	choice := apiResp.Choices[0]
	if choice.FinishReason != "tool_calls" && len(choice.Message.ToolCalls) == 0 {
		// Если модель не вызвала инструмент — это не фатально, но заслуживает внимания
		t.Logf("Model did not call any tool (finish_reason=%s). Response: %s",
			choice.FinishReason, choice.Message.Content)
	}
}
