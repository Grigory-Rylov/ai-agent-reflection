package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/opencode/llama-client/pkg/agent"
	"github.com/opencode/llama-client/pkg/logger"
	"github.com/opencode/llama-client/pkg/vk"
)

// ============================================================
// VK Gateway — точка входа для VK Bot Gateway режима
// ============================================================

func main() {
	// Парсим флаги
	debug := flag.Bool("d", false, "Enable debug mode")
	flag.Parse()

	// Загружаем конфигурацию
	config, err := loadConfig("config.json")
	if err != nil {
		println("Error loading config:", err.Error())
		os.Exit(1)
	}

	// Инициализируем логгер
	logConfig := logger.DefaultConfig()
	logConfig.Level = logger.LevelDebug
	if !*debug {
		logConfig.Level = logger.LevelInfo
	}
	log, err := logger.New(logConfig)
	if err != nil {
		println("Error creating logger:", err.Error())
		os.Exit(1)
	}

	log.InfoLog("VK Bot Gateway starting...")

	// Создаём VK Bot Client
	vkClient := vk.NewBotClient(config.TokenVK)

	// Создаём AI Agent
	agentConfig := agent.DefaultConfig()
	agentConfig.LlamaServerURL = config.LlamaServerURL
	agentConfig.Model = config.Model
	agentConfig.MaxTokens = config.MaxTokens
	agentConfig.Temperature = config.Temperature
	agentConfig.SessionConfig.SessionFile = "./sessions/vk_session.json"
	agentConfig.SessionConfig.AutoSave = true

	aiAgent := agent.NewAgent(agentConfig)

	// Создаём Bot Handler
	botHandler := vk.NewBotHandler(vkClient, aiAgent, log)

	// Настраиваем обработку сигналов
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.InfoLog("Shutting down...")
		cancel()
	}()

	// Запускаем Bot Handler
	log.InfoLog("Starting VK Bot Handler...")
	if err := botHandler.Start(ctx); err != nil {
		log.ErrorLogf("Bot handler error: %v", err)
		os.Exit(1)
	}

	log.InfoLog("VK Bot Gateway stopped")
}

// ============================================================
// Config — структура конфигурации
// ============================================================

// Config представляет конфигурацию приложения
type Config struct {
	LlamaServerURL string  `json:"llama_server_url"`
	Model          string  `json:"model"`
	MaxTokens      int     `json:"max_tokens"`
	Temperature    float64 `json:"temperature"`
	TokenVK        string  `json:"token_vk"`
}

// loadConfig загружает конфигурацию из файла
func loadConfig(path string) (Config, error) {
	var config Config
	data, err := os.ReadFile(path)
	if err != nil {
		return config, err
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return config, err
	}

	return config, nil
}
