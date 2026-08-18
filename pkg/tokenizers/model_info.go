package tokenizers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// ModelInfo содержит информацию о модели от llama-server или vLLM
type ModelInfo struct {
	ID           string       `json:"id"`
	Object       string       `json:"object"`
	Created      int64        `json:"created"`
	OwnedBy      string       `json:"owned_by"`
	Meta         *ModelMeta   `json:"meta"`
	Status       *ModelStatus `json:"status"`
	MaxModelLen  int          `json:"max_model_len"` // vLLM
}

// ModelStatus содержит статус модели (аргументы запуска сервера)
type ModelStatus struct {
	Value string   `json:"value"`
	Args  []string `json:"args"`
}

// ModelMeta содержит метаданные модели
type ModelMeta struct {
	VocabType int `json:"vocab_type"`
	NVocab    int `json:"n_vocab"`
	NCtxTrain int `json:"n_ctx_train"` // Тренировочный контекст (не используем)
	NCtx      int `json:"n_ctx"`       // Реальный контекст сервера
	NEmbd     int `json:"n_embd"`
	NParams   int `json:"n_params"`
	Size      int `json:"size"`
}

// ModelsResponse - ответ от /v1/models
type ModelsResponse struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`
}

// PropsResponse - ответ от /props
type PropsResponse struct {
	DefaultGenerationSettings *GenerationSettings `json:"default_generation_settings"`
	TotalSlots                int                 `json:"total_slots"`
	ModelPath                 string              `json:"model_path"`
}

// GenerationSettings содержит настройки генерации
type GenerationSettings struct {
	NCtx int `json:"n_ctx"` // Текущий контекст сервера
}

// ServerInfoClient - клиент для получения информации о сервере/модели
type ServerInfoClient struct {
	serverURL string
	client    *http.Client
	debug     bool
}

// NewServerInfoClient создаёт новый клиент
func NewServerInfoClient(serverURL string) *ServerInfoClient {
	return &ServerInfoClient{
		serverURL: serverURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		debug: false,
	}
}

// SetDebug включает отладочные сообщения
func (c *ServerInfoClient) SetDebug(debug bool) {
	c.debug = debug
}

// GetModelContextLength получает реальный контекст модели от llama-server.
// Ищет модель по имени в /v1/models (n_ctx_train), иначе фоллбэк на /props (n_ctx).
// Возвращает -1 если не удалось получить информацию.
func (c *ServerInfoClient) GetModelContextLength(model string) int {
	// Сначала пробуем /v1/models
	if ctxLen := c.getContextFromV1Models(model); ctxLen > 0 {
		return ctxLen
	}

	// Фоллбэк на /props
	if ctxLen := c.getContextFromProps(); ctxLen > 0 {
		return ctxLen
	}

	return -1
}

// getContextFromV1Models получает контекст сервера для модели из /v1/models.
// Приоритет: --ctx-size/-c из status.args (аргумент запуска сервера),
// затем meta.n_ctx (реальный контекст).
func (c *ServerInfoClient) getContextFromV1Models(model string) int {
	reqURL := fmt.Sprintf("%s/v1/models", c.serverURL)
	req, err := http.NewRequestWithContext(context.Background(), "GET", reqURL, nil)
	if err != nil {
		if c.debug {
			fmt.Printf("[server-info] Failed to create request: %v\n", err)
		}
		return -1
	}

	resp, err := c.client.Do(req)
	if err != nil {
		if c.debug {
			fmt.Printf("[server-info] Failed to get /v1/models: %v\n", err)
		}
		return -1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if c.debug {
			body, _ := io.ReadAll(resp.Body)
			fmt.Printf("[server-info] /v1/models returned status %d: %s\n", resp.StatusCode, string(body))
		}
		return -1
	}

	var modelsResp ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		if c.debug {
			fmt.Printf("[server-info] Failed to decode /v1/models response: %v\n", err)
		}
		return -1
	}

	if len(modelsResp.Data) == 0 {
		if c.debug {
			fmt.Println("[server-info] No models in /v1/models response")
		}
		return -1
	}

	// Ищем модель по id; если не найдена — берём первую модель со статусом.
	var matched *ModelInfo
	for i := range modelsResp.Data {
		if modelsResp.Data[i].ID == model {
			matched = &modelsResp.Data[i]
			break
		}
	}
	if matched == nil {
		for i := range modelsResp.Data {
			if modelsResp.Data[i].Status != nil {
				matched = &modelsResp.Data[i]
				break
			}
		}
	}

	if matched == nil {
		if c.debug {
			fmt.Printf("[server-info] Model %q not found in /v1/models response\n", model)
		}
		return -1
	}

	// 1. Аргумент запуска --ctx-size / -c
	if ctxLen := ctxSizeFromArgs(matched.Status); ctxLen > 0 {
		if c.debug {
			fmt.Printf("[server-info] Got --ctx-size=%d for model %q from /v1/models\n", ctxLen, model)
		}
		return ctxLen
	}

	// 2. Реальный контекст сервера (llama.cpp)
	if matched.Meta != nil && matched.Meta.NCtx > 0 {
		if c.debug {
			fmt.Printf("[server-info] Got n_ctx=%d for model %q from /v1/models\n", matched.Meta.NCtx, model)
		}
		return matched.Meta.NCtx
	}

	// 3. vLLM: max_model_len
	if matched.MaxModelLen > 0 {
		if c.debug {
			fmt.Printf("[server-info] Got max_model_len=%d for model %q from /v1/models\n", matched.MaxModelLen, model)
		}
		return matched.MaxModelLen
	}

	return -1
}

// ctxSizeFromArgs извлекает значение --ctx-size/-c из аргументов запуска сервера.
func ctxSizeFromArgs(status *ModelStatus) int {
	if status == nil {
		return 0
	}
	for i, arg := range status.Args {
		if arg == "--ctx-size" || arg == "-c" {
			if i+1 < len(status.Args) {
				if n, err := strconv.Atoi(status.Args[i+1]); err == nil && n > 0 {
					return n
				}
			}
		}
	}
	return 0
}

// getContextFromProps получает n_ctx из /props
func (c *ServerInfoClient) getContextFromProps() int {
	reqURL := fmt.Sprintf("%s/props", c.serverURL)
	req, err := http.NewRequestWithContext(context.Background(), "GET", reqURL, nil)
	if err != nil {
		if c.debug {
			fmt.Printf("[server-info] Failed to create request: %v\n", err)
		}
		return -1
	}

	resp, err := c.client.Do(req)
	if err != nil {
		if c.debug {
			fmt.Printf("[server-info] Failed to get /props: %v\n", err)
		}
		return -1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if c.debug {
			body, _ := io.ReadAll(resp.Body)
			fmt.Printf("[server-info] /props returned status %d: %s\n", resp.StatusCode, string(body))
		}
		return -1
	}

	var propsResp PropsResponse
	if err := json.NewDecoder(resp.Body).Decode(&propsResp); err != nil {
		if c.debug {
			fmt.Printf("[server-info] Failed to decode /props response: %v\n", err)
		}
		return -1
	}

	if propsResp.DefaultGenerationSettings == nil {
		if c.debug {
			fmt.Println("[server-info] No default_generation_settings in /props response")
		}
		return -1
	}

	ctxLen := propsResp.DefaultGenerationSettings.NCtx
	if ctxLen > 0 {
		if c.debug {
			fmt.Printf("[server-info] Got n_ctx from /props: %d\n", ctxLen)
		}
		return ctxLen
	}

	return -1
}

// GetModelInfo получает полную информацию о модели
func (c *ServerInfoClient) GetModelInfo() (*ModelInfo, error) {
	reqURL := fmt.Sprintf("%s/v1/models", c.serverURL)
	req, err := http.NewRequestWithContext(context.Background(), "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get /v1/models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var modelsResp ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(modelsResp.Data) == 0 {
		return nil, fmt.Errorf("no models in response")
	}

	return &modelsResp.Data[0], nil
}
