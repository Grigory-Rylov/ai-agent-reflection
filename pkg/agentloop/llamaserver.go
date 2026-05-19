package agentloop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TestLlamaServerResult содержит результат теста llama-server
type TestLlamaServerResult struct {
	Model        string
	ResponseTime time.Duration
	TokensPerSec float64
	Error        error
}

// TestLlamaServer тестирует соединение с llama-server
func TestLlamaServer(ctx context.Context, serverURL, model string) TestLlamaServerResult {
	reqBody := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": "Write a short poem about programming with at least 20 words."},
		},
		"max_tokens":  100,
		"temperature": 0.7,
		"stream":      false,
	}

	jsonData, _ := json.Marshal(reqBody)
	reqURL := serverURL + "/v1/chat/completions"

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewReader(jsonData))
	if err != nil {
		return TestLlamaServerResult{Error: fmt.Errorf("create request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return TestLlamaServerResult{Error: fmt.Errorf("connection failed: %w", err)}
	}
	defer resp.Body.Close()

	responseTime := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return TestLlamaServerResult{
			ResponseTime: responseTime,
			Error:        fmt.Errorf("status %d: %s", resp.StatusCode, string(body)),
		}
	}

	var result struct {
		Model string `json:"model"`
		Usage struct {
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return TestLlamaServerResult{
			ResponseTime: responseTime,
			Error:        fmt.Errorf("decode response: %w", err),
		}
	}

	tokensPerSec := 0.0
	if result.Usage.CompletionTokens > 0 {
		tokensPerSec = float64(result.Usage.CompletionTokens) / responseTime.Seconds()
	}

	return TestLlamaServerResult{
		Model:        result.Model,
		ResponseTime: responseTime,
		TokensPerSec: tokensPerSec,
	}
}
