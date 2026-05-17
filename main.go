package main

import (
	"bufio"
	"bytes"
	"flag"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/opencode/llama-client/pkg/logger"
	"github.com/opencode/llama-client/session"
)

// ============================================================
// Конфигурация
// ============================================================

type Config struct {
	LlamaServerURL string `json:"llama_server_url"`
	VKToken        string `json:"token_vk"`
	Debug          bool   `json:"debug"`
	LogFile        string `json:"log_file"`
}

// ============================================================
// Модели данных (OpenAI-compatible Streaming API)
// ============================================================

// ChoiceDelta — часть ответа в потоке
type ChoiceDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// ChoiceChunk — один чанк в потоке
type ChoiceChunk struct {
	Index        int         `json:"index"`
	Delta        ChoiceDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

// StreamChunk — событие SSE-потока (OpenAI format)
type StreamChunk struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChoiceChunk `json:"choices"`
	Usage   *Usage       `json:"usage"`
}

// Usage содержит статистику токенов
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ============================================================
// Типы событий и хендлеры
// ============================================================

type EventType string

const (
	DeltaEvent EventType = "delta"    // Новый токен ответа
	StopEvent  EventType = "stop"     // Завершение генерации
	ErrorEvent EventType = "error"    // Ошибка
)

// EventCallback — функция-обработчик события
type EventCallback func(chunk StreamChunk, eventType EventType)

// ============================================================
// Асинхронный клиент llama-server
// ============================================================

// LlamaClient — асинхронный клиент для работы с llama-server
type LlamaClient struct {
	baseURL    string
	httpClient *http.Client

	// Каналы для асинхронной обработки событий
	deltaCh   chan StreamChunk
	stopCh    chan StreamChunk
	errorCh   chan StreamChunk

	// Хендлеры событий
	deltaHandler EventCallback
	stopHandler  EventCallback
	errorHandler EventCallback

	// Состояние
	done       chan struct{}
	lastAnswer string
	finalUsage *Usage
}

// NewLlamaClient создаёт новый клиент
func NewLlamaClient(baseURL string) *LlamaClient {
	return &LlamaClient{
		baseURL:    baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
		deltaCh:   make(chan StreamChunk, 100),
		stopCh:    make(chan StreamChunk, 1),
		errorCh:   make(chan StreamChunk, 1),
		done:      make(chan struct{}),
	}
}

// SetHandler регистрирует обработчик для конкретного типа события
func (c *LlamaClient) SetHandler(eventType EventType, handler EventCallback) {
	switch eventType {
	case DeltaEvent:
		c.deltaHandler = handler
	case StopEvent:
		c.stopHandler = handler
	case ErrorEvent:
		c.errorHandler = handler
	}
}

// startEventDispatcherWithChannels запускает диспетчер событий в фоновом горутине
func (c *LlamaClient) startEventDispatcherWithChannels(deltaCh, stopCh, errorCh chan StreamChunk, done chan struct{}) {
	go func() {
		for {
			select {
			case chunk, ok := <-deltaCh:
				if !ok {
					return
				}
				if c.deltaHandler != nil {
					c.deltaHandler(chunk, DeltaEvent)
				}
			case chunk, ok := <-stopCh:
				if !ok {
					return
				}
				if c.stopHandler != nil {
					c.stopHandler(chunk, StopEvent)
				}
			case chunk, ok := <-errorCh:
				if !ok {
					return
				}
				if c.errorHandler != nil {
					c.errorHandler(chunk, ErrorEvent)
				}
			case <-done:
				return
			}
		}
	}()
}

// sendEvent отправляет событие в соответствующий канал (локальная версия для многократных запросов)
func (c *LlamaClient) sendEventLocal(deltaCh, stopCh, errorCh chan StreamChunk, eventType EventType, chunk StreamChunk) {
	// Игнорируем пустые события
	if len(chunk.Choices) == 0 {
		return
	}

	// Отправляем в правильный канал в зависимости от типа события
	switch eventType {
	case DeltaEvent:
		select {
		case deltaCh <- chunk:
		default:
			// Канал переполнен, пропускаем
		}
	case StopEvent:
		select {
		case stopCh <- chunk:
		default:
			// Канал переполнен, пропускаем
		}
	case ErrorEvent:
		select {
		case errorCh <- chunk:
		default:
			// Канал переполнен, пропускаем
		}
	}
}

// ============================================================
// Утилиты
// ============================================================

// approximateTokens — приблизительная оценка количества токенов
// (1 токен ≈ 0.75 символа для английского, ≈ 1.5 для русского)
func approximateTokens(text string) int {
	// Простая эвристика: 1 токен ≈ 4 символа
	return len([]rune(text)) / 4
}

// ============================================================
// Чтение SSE-потока (OpenAI-compatible API)
// ============================================================

func (c *LlamaClient) StreamChatCompletion(messages []session.Message) error {
	// Формируем запрос
	requestMessages := make([]map[string]string, len(messages))
	for i, msg := range messages {
		requestMessages[i] = map[string]string{
			"role":    string(msg.Role),
			"content": msg.Content,
		}
	}

	requestBody := map[string]interface{}{
		"model":     "local-model",
		"messages":  requestMessages,
		"temperature": 0.7,
		"max_tokens":  512,
		"stream":      true,
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST",
		fmt.Sprintf("http://%s/v1/chat/completions", c.baseURL), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Отправляем запрос
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	// Создаём новые каналы для каждого запроса
	deltaCh := make(chan StreamChunk, 100)
	stopCh := make(chan StreamChunk, 1)
	errorCh := make(chan StreamChunk, 1)
	done := make(chan struct{})

	// Запускаем диспетчер событий
	c.startEventDispatcherWithChannels(deltaCh, stopCh, errorCh, done)

	// Читаем SSE-поток
	startTime := time.Now()
	var fullAnswer bytes.Buffer
	var chunkCount int
	var hasUsage bool

	// Используем bufio.Reader для корректного парсинга SSE
	reader := bufio.NewReader(resp.Body)
	for {
		// Читаем строку до перевода строки
		line, err := reader.ReadSlice('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to read SSE line: %w", err)
		}

		// Убираем перевод строки
		line = bytes.TrimSpace(line)

		// Пустая строка — конец события SSE
		if len(line) == 0 {
			continue
		}

		// SSE формат: "data: {json}" или "[DONE]"
		if bytes.Equal(line, []byte("[DONE]")) {
			// После [DONE] может прийти финальное событие с usage
			// Пробуем прочитать одну строку
			nextLine, err := reader.Peek(10)
			if err == nil && len(nextLine) > 0 {
				// Проверяем, начинается ли со "data:"
				if bytes.HasPrefix(nextLine, []byte("data: ")) {
					// Читаем эту строку до конца
					fullLine, _ := reader.ReadBytes('\n')
					jsonData := bytes.TrimSpace(fullLine[6:]) // Убираем "data: "
					if len(jsonData) > 0 {
						var chunk StreamChunk
						if err := json.Unmarshal(jsonData, &chunk); err == nil && chunk.Usage != nil {
							c.finalUsage = chunk.Usage
							hasUsage = true
						}
					}
				}
			}
			break
		}

		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}

		jsonData := line[6:] // Убираем "data: "
		if len(jsonData) == 0 {
			continue
		}

		// Парсим событие
		var chunk StreamChunk
		if err := json.Unmarshal(jsonData, &chunk); err != nil {
			continue
		}

		// Получаем контент из первого выбора
		if len(chunk.Choices) > 0 {
			choice := &chunk.Choices[0]
			deltaContent := choice.Delta.Content

			// Если есть контент — добавляем к ответу
			if deltaContent != "" {
				fullAnswer.WriteString(deltaContent)
				chunkCount++
			}

			// Если есть finish_reason — это конец
			if choice.FinishReason != nil {
				c.sendEventLocal(deltaCh, stopCh, errorCh, StopEvent, chunk)
			} else if deltaContent != "" {
				// Иначе — это delta
				c.sendEventLocal(deltaCh, stopCh, errorCh, DeltaEvent, chunk)
			}
		}

		// Сохраняем usage из последнего события
		if chunk.Usage != nil {
			c.finalUsage = chunk.Usage
			hasUsage = true
		}
	}

	elapsed := time.Since(startTime)

	// Закрываем каналы и ждём завершения диспетчера
	close(deltaCh)
	close(stopCh)
	close(errorCh)
	close(done)

	// Сохраняем ответ
	c.lastAnswer = fullAnswer.String()

	// Выводим статистику
	fmt.Printf("\n[Statistics]\n")
	fmt.Printf("  Time elapsed:       %.1fs\n", elapsed.Seconds())
	fmt.Printf("  Answer length:      %d chars\n", len(fullAnswer.String()))
	fmt.Printf("  SSE chunks:         %d\n", chunkCount)

	// Попытка получить точную статистику от сервера
	if hasUsage && c.finalUsage != nil {
		tokensPerSecond := 0.0
		if c.finalUsage.CompletionTokens > 0 && elapsed.Seconds() > 0 {
			tokensPerSecond = float64(c.finalUsage.CompletionTokens) / elapsed.Seconds()
		}

		fmt.Printf("  Prompt tokens:      %d\n", c.finalUsage.PromptTokens)
		fmt.Printf("  Completion tokens:  %d\n", c.finalUsage.CompletionTokens)
		fmt.Printf("  Total tokens:       %d\n", c.finalUsage.TotalTokens)
		fmt.Printf("  Tokens/second:      %.2f\n", tokensPerSecond)
		fmt.Printf("  Time per token:     %.1fms\n", elapsed.Seconds()*1000/float64(c.finalUsage.CompletionTokens))
	} else {
		// Приблизительный расчёт токенов (без точных данных от сервера)
		approxTokens := approximateTokens(fullAnswer.String())
		tokensPerSecond := 0.0
		if approxTokens > 0 && elapsed.Seconds() > 0 {
			tokensPerSecond = float64(approxTokens) / elapsed.Seconds()
		}

		fmt.Printf("  ~Completion tokens: %d (approx)\n", approxTokens)
		fmt.Printf("  Tokens/second:      %.2f\n", tokensPerSecond)
		fmt.Printf("  Time per token:     %.1fms\n", elapsed.Seconds()*1000/float64(approxTokens))
	}

	return nil
}

// LastAnswer возвращает последний полученный ответ
func (c *LlamaClient) LastAnswer() string {
	return c.lastAnswer
}

// FinalUsage возвращает финальную статистику токенов
func (c *LlamaClient) FinalUsage() *Usage {
	return c.finalUsage
}

// ============================================================
// Обработчики событий
// ============================================================

// OnDelta — обработчик событий дельты (новый токен)
func OnDelta(chunk StreamChunk, _ EventType) {
	if len(chunk.Choices) > 0 {
		content := chunk.Choices[0].Delta.Content
		if content != "" {
			fmt.Print(content)
		}
	}
}

// OnStop — обработчик завершения генерации
func OnStop(chunk StreamChunk, _ EventType) {
	fmt.Println("\n[Complete] Generation finished")
}

// OnError — обработчик ошибок
func OnError(chunk StreamChunk, _ EventType) {
	fmt.Fprintf(os.Stderr, "[Error] %v\n", chunk)
}

// ============================================================
// main — точка входа
// ============================================================

func loadConfig() (Config, error) {
	data, err := os.ReadFile("config.json")
	if err != nil {
		return Config{}, fmt.Errorf("failed to read config.json: %w", err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("failed to parse config.json: %w", err)
	}

	return config, nil
}

func main() {
	// Парсинг командной строки
	debugMode := flag.Bool("d", false, "Enable debug logging (console + file)")
	flag.Parse()

	// Загрузка конфигурации
	fmt.Println("Loading configuration...")
	config, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Переопределяем debug из флага командной строки
	if *debugMode {
		config.Debug = true
		// Устанавливаем дефолтный путь для лога если не указан
		if config.LogFile == "" {
			config.LogFile = "logs/agent-debug.log"
		}
	}

	// Инициализация логгера
	logConfig := logger.Config{
		Level:   logger.LevelInfo,
		File:    config.LogFile,
		MaxSizeMB: 10,
		MaxAgeDays: 7,
	}

	// Если debug mode — включаем дебаг и в файл
	if config.Debug {
		logConfig.Level = logger.LevelDebug
		// Создаём директорию для логов
		if logConfig.File != "" {
			os.MkdirAll(filepath.Dir(logConfig.File), 0755)
		}
	}

	log, err := logger.New(logConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to init logger: %v\n", err)
		// Продолжаем без логгера
		log = nil
	}
	if log != nil {
		defer log.Close()
		log.InfoLogf("Application starting (debug=%v, log_file=%s)", config.Debug, config.LogFile)
	}

	// Логируем конфигурацию
	if log != nil {
		log.InfoLogf("Server URL: %s", config.LlamaServerURL)
		if config.VKToken != "" {
			log.InfoLogf("VK Token configured: %s...", config.VKToken[:8])
		}
	}
	fmt.Printf("Server URL: %s\n", config.LlamaServerURL)

	// Создаём клиент
	client := NewLlamaClient(config.LlamaServerURL)

	// Регистрируем обработчики событий
	client.SetHandler(DeltaEvent, OnDelta)
	client.SetHandler(StopEvent, OnStop)
	client.SetHandler(ErrorEvent, OnError)

	// Настраиваем обработку сигналов (Ctrl+C)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		if log != nil {
			log.InfoLog("Shutting down...")
		}
		fmt.Println("\nShutting down...")
		os.Exit(0)
	}()

	// Создаём сессию для хранения истории диалога
	sessionConfig := session.DefaultConfig()
	sessionConfig.AutoSave = config.Debug
	if config.Debug {
		os.MkdirAll("sessions", 0755)
		sessionConfig.SessionFile = "sessions/cli_session.json"
	}
	sessionConfig.MaxHistory = 50
	sessionConfig.SystemPrompt = "You are a helpful assistant."

	currentSession := session.NewSession(sessionConfig)

	// Интерактивный цикл диалога
	fmt.Println("\n═══════════════════════════════════════════")
	fmt.Println("Interactive Chat Mode")
	fmt.Println("═══════════════════════════════════════════")
	fmt.Println("Type your message and press Enter")
	fmt.Println("Commands:")
	fmt.Println("  /reset  - Clear conversation history")
	fmt.Println("  /quit   - Exit the application")
	fmt.Println("═══════════════════════════════════════════")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}

		input := scanner.Text()

		// Обработка команд
		if input == "/reset" {
			currentSession.Reset()
			if log != nil {
				log.InfoLog("Session reset by user")
			}
			fmt.Println("[History cleared]")
			continue
		}
		if input == "/quit" || input == "/exit" {
			if log != nil {
				log.InfoLog("User exited the application")
			}
			fmt.Println("Goodbye!")
			break
		}
		if input == "" {
			continue
		}

		// Логируем вход пользователя
		if log != nil && config.Debug {
			log.DebugLogf("User input: %s", input)
		}

		// Добавляем сообщение пользователя в сессию
		currentSession.AddUserMessage(input)

		// Получаем все сообщения для отправки в API
		history := currentSession.GetHistory()

		if log != nil {
			log.InfoLogf("Sending request to: http://%s", config.LlamaServerURL)
			log.DebugLogf("Messages in context: %d", currentSession.HistoryLength())
		}
		fmt.Printf("\nSending request to: http://%s\n", config.LlamaServerURL)
		fmt.Printf("Messages in context: %d\n", currentSession.HistoryLength())

		// Отправляем запрос
		if err := client.StreamChatCompletion(history); err != nil {
			if log != nil {
				log.ErrorLogf("Request failed: %v", err)
			}
			fmt.Fprintf(os.Stderr, "Request failed: %v\n", err)
			continue
		}

		// Сохраняем ответ ассистента в сессию
		currentSession.AddAssistantMessage(client.LastAnswer())

		// Логируем ответ
		if log != nil {
			log.InfoLogf("Assistant response received: %d chars", len(client.LastAnswer()))
			if finalUsage := client.FinalUsage(); finalUsage != nil {
				log.DebugLogf("Token usage - prompt: %d, completion: %d, total: %d",
					finalUsage.PromptTokens, finalUsage.CompletionTokens, finalUsage.TotalTokens)
			}
		}

		// Выводим финальный ответ
		fmt.Println("\n═══════════════════════════════════════════")
		fmt.Println("FINAL RESPONSE")
		fmt.Println("═══════════════════════════════════════════")
		fmt.Println(client.LastAnswer())
		fmt.Println("═══════════════════════════════════════════")
	}
}
