package compress

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opencode/llama-client/pkg/tokenizers"
)

// ============================================================
// LLM Summarizer — суммаризация через LLM
// ============================================================

// LLMSummarizer суммаризирует контекст через модель
type LLMSummarizer struct {
	serverURL   string
	model       string
	client      *http.Client
	temperature float64
}

// NewLLMSummarizer создаёт новый суммаризатор
func NewLLMSummarizer(serverURL, model string, temperature float64) *LLMSummarizer {
	return &LLMSummarizer{
		serverURL: serverURL,
		model:     model,
		client: &http.Client{
			Timeout: 2 * time.Minute,
		},
		temperature: temperature,
	}
}

// SummarizeRequest — запрос на суммаризацию
type SummarizeRequest struct {
	Messages     []tokenizers.Message
	MaxTokens    int
	CurrentState *ContextState
}

// SummarizeResponse — ответ суммаризации
type SummarizeResponse struct {
	State       *ContextState
	RawResponse string
	TokensUsed  int
}

// Summarize выполняет суммаризацию через LLM
func (s *LLMSummarizer) Summarize(ctx context.Context, req *SummarizeRequest) (*SummarizeResponse, error) {
	systemPrompt := s.buildSystemPrompt()
	userPrompt := s.buildUserPrompt(req)

	response, err := s.sendRequest(ctx, systemPrompt, userPrompt, req.MaxTokens)
	if err != nil {
		return nil, fmt.Errorf("LLM request failed: %w", err)
	}

	state := s.parseOrCreateState(response)

	return &SummarizeResponse{
		State:       state,
		RawResponse: response,
	}, nil
}

// parseOrCreateState парсит ответ или создаёт состояние из raw текста
func (s *LLMSummarizer) parseOrCreateState(response string) *ContextState {
	state, err := s.parseResponse(response)
	if err != nil {
		// Fallback: используем raw response как working memory
		return &ContextState{
			WorkingMemory: []string{truncateString(response, 500)},
			LastUpdated:   time.Now(),
		}
	}
	return state
}

// buildSystemPrompt создаёт системный промпт для суммаризации
func (s *LLMSummarizer) buildSystemPrompt() string {
	return `You are a context compression assistant. Extract structured information from the conversation.

Output ONLY a JSON object with this structure:
{
  "goal": "main goal",
  "decisions": ["decision 1"],
  "working_memory": ["fact 1"],
  "artifacts": [{"path": "file.go", "description": "purpose"}],
  "open_questions": ["question"],
  "next_steps": ["step 1"]
}

Keep each item concise. Focus on information needed to continue the task.`
}

// buildUserPrompt создаёт пользовательский промпт
func (s *LLMSummarizer) buildUserPrompt(req *SummarizeRequest) string {
	var sb strings.Builder

	sb.WriteString(s.formatCurrentState(req.CurrentState))
	sb.WriteString("Messages to summarize:\n\n")

	for i, msg := range req.Messages {
		if i >= 50 {
			sb.WriteString("... [older messages truncated]\n")
			break
		}
		sb.WriteString(formatMessage(msg))
	}

	sb.WriteString("\nProvide a structured summary.")
	return sb.String()
}

// formatCurrentState форматирует текущее состояние для промпта
func (s *LLMSummarizer) formatCurrentState(state *ContextState) string {
	if state == nil {
		return ""
	}
	return "Current state:\n" + state.ToPrompt() + "\n---\n\n"
}

// formatMessage форматирует сообщение для промпта
func formatMessage(msg tokenizers.Message) string {
	content := truncateString(msg.Content, 2000)
	return fmt.Sprintf("[%s]: %s\n\n", msg.Role, content)
}

// sendRequest отправляет запрос к LLM
func (s *LLMSummarizer) sendRequest(ctx context.Context, systemPrompt, userPrompt string, maxTokens int) (string, error) {
	reqBody := s.buildRequestBody(systemPrompt, userPrompt, maxTokens)

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := s.createHTTPRequest(ctx, jsonData)
	if err != nil {
		return "", err
	}

	return s.executeRequest(req)
}

// buildRequestBody строит тело запроса
func (s *LLMSummarizer) buildRequestBody(systemPrompt, userPrompt string, maxTokens int) map[string]interface{} {
	return map[string]interface{}{
		"model": s.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"max_tokens":  maxTokens,
		"temperature": s.temperature,
		"stream":      false,
	}
}

// createHTTPRequest создаёт HTTP запрос
func (s *LLMSummarizer) createHTTPRequest(ctx context.Context, jsonData []byte) (*http.Request, error) {
	reqURL := fmt.Sprintf("%s/v1/chat/completions", s.serverURL)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// executeRequest выполняет запрос и возвращает ответ
func (s *LLMSummarizer) executeRequest(req *http.Request) (string, error) {
	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error: status %d", resp.StatusCode)
	}

	return s.parseAPIResponse(resp)
}

// parseAPIResponse парсит ответ API
func (s *LLMSummarizer) parseAPIResponse(resp *http.Response) (string, error) {
	var apiResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(apiResponse.Choices) == 0 {
		return "", fmt.Errorf("no response from model")
	}

	return apiResponse.Choices[0].Message.Content, nil
}

// parseResponse парсит ответ LLM в структурированное состояние
func (s *LLMSummarizer) parseResponse(response string) (*ContextState, error) {
	jsonStr, err := extractJSON(response)
	if err != nil {
		return nil, err
	}

	var state ContextState
	if err := json.Unmarshal([]byte(jsonStr), &state); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}

	state.LastUpdated = time.Now()
	return &state, nil
}

// extractJSON извлекает JSON из ответа
func extractJSON(response string) (string, error) {
	jsonStart := strings.Index(response, "{")
	jsonEnd := strings.LastIndex(response, "}")

	if jsonStart == -1 || jsonEnd == -1 || jsonEnd <= jsonStart {
		return "", fmt.Errorf("no JSON found in response")
	}

	return response[jsonStart : jsonEnd+1], nil
}

// ============================================================
// FileArtifactStore — файловое хранилище артефактов
// ============================================================

// FileArtifactStore сохраняет артефакты в файлы
type FileArtifactStore struct {
	basePath string
}

// NewFileArtifactStore создаёт новое файловое хранилище
func NewFileArtifactStore(basePath string) (*FileArtifactStore, error) {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}
	return &FileArtifactStore{basePath: basePath}, nil
}

// Save сохраняет контент в файл и возвращает ссылку
func (s *FileArtifactStore) Save(name string, content string) (ArtifactRef, error) {
	filename := s.generateFilename(name)
	path := filepath.Join(s.basePath, filename)

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return ArtifactRef{}, fmt.Errorf("write file: %w", err)
	}

	return s.createArtifactRef(name, path, content), nil
}

// generateFilename генерирует уникальное имя файла
func (s *FileArtifactStore) generateFilename(name string) string {
	return fmt.Sprintf("%s_%d.txt", sanitizeFilename(name), time.Now().Unix())
}

// createArtifactRef создаёт ссылку на артефакт
func (s *FileArtifactStore) createArtifactRef(name, path, content string) ArtifactRef {
	return ArtifactRef{
		Type:        "file",
		Path:        path,
		Description: fmt.Sprintf("Stored: %s", name),
		Tokens:      EstimateTokensSimple(content),
	}
}

// Load загружает контент из файла
func (s *FileArtifactStore) Load(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	return string(data), nil
}

// sanitizeFilename очищает имя файла
func sanitizeFilename(name string) string {
	var result strings.Builder
	for _, r := range name {
		if isAllowedChar(r) {
			result.WriteRune(r)
		} else if r == ' ' {
			result.WriteRune('_')
		}
	}
	return result.String()
}

// isAllowedChar проверяет разрешённый символ для имени файла
func isAllowedChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r == '_' || r == '-'
}

// truncateString обрезает строку до максимальной длины
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "... [truncated]"
}
