package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/opencode/llama-client/pkg/agentloop"
	"github.com/opencode/llama-client/pkg/logger"
	"github.com/opencode/llama-client/pkg/mcp"
	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/pkg/vk"
)

// Version - текущая версия бота, инкрементируйте при изменениях
const Version = "2026.05.20-18:28"

// ============================================================
// VK Gateway — точка входа для VK Bot Gateway режима
// ============================================================

func main() {
	// Парсим флаги
	debug := flag.Bool("d", false, "Enable debug mode")
	reset := flag.Bool("r", false, "Reset session on startup (clear history)")
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
	if *debug {
		logConfig.File = "debug.log"
	} else {
		logConfig.Level = logger.LevelInfo
	}
	log, err := logger.New(logConfig)
	if err != nil {
		println("Error creating logger:", err.Error())
		os.Exit(1)
	}

	// Инициализируем глобальный логгер для DebugToFile
	logger.InitGlobalLogger(logConfig)

	log.InfoLog("VK Bot Gateway starting... (v%s)", Version)

	// Сброс сессии если указан флаг -r
	if *reset {
		sessionFile := "./sessions/vk_session.json"
		if _, err := os.Stat(sessionFile); err == nil {
			if err := os.Remove(sessionFile); err != nil {
				log.WarnLogf("Failed to remove session file: %v", err)
			} else {
				log.InfoLog("Session reset: history cleared")
			}
		}
	}

	// Создаём VK Bot Client
	vkClient := vk.NewBotClient(config.TokenVK)

	// Создаём реестр инструментов
	toolRegistry := tools.NewRegistry()
	toolRegistry.Register(&tools.FileReadTool{})
	toolRegistry.Register(&tools.FileWriteTool{})
	toolRegistry.Register(&tools.TimeGetTool{})
	toolRegistry.Register(&tools.DirListTool{})
	toolRegistry.Register(&tools.ShellExecuteTool{})
	toolRegistry.Register(&tools.WebFetchTool{})
	toolRegistry.Register(&tools.WebSearchTool{})
	toolRegistry.Register(&tools.GlobTool{})
	toolRegistry.Register(&tools.GrepTool{})
	toolRegistry.Register(&tools.CalcTool{})
	toolRegistry.Register(&tools.EditTool{})

	// Инициализируем MCP Manager если указан конфиг
	var mcpManager *mcp.Manager
	if config.MCPConfigPath != "" {
		mcpManager = mcp.NewManager(toolRegistry, log)
		mcpConfig, err := loadMCPConfig(config.MCPConfigPath)
		if err != nil {
			log.WarnLogf("Failed to load MCP config: %v", err)
		} else {
			if err := mcpManager.LoadConfig(context.Background(), mcpConfig); err != nil {
				log.WarnLogf("Failed to initialize MCP servers: %v", err)
			} else {
				log.InfoLogf("MCP servers initialized: %s", mcpManager.Stats())
			}
		}
	}

// Создаём конфигурацию AgentLoop
	loopConfig := agentloop.DefaultLoopConfig()
	// Добавляем http:// если нет
	llamaURL := config.LlamaServerURL
	if !strings.HasPrefix(llamaURL, "http://") && !strings.HasPrefix(llamaURL, "https://") {
		llamaURL = "http://" + llamaURL
	}
	loopConfig.LlamaServerURL = llamaURL
	loopConfig.Model = config.Model
	loopConfig.MaxTokens = config.MaxTokens
	loopConfig.Temperature = config.Temperature
	loopConfig.SessionConfig.SessionFile = "./sessions/vk_session.json"
	loopConfig.SessionConfig.AutoSave = true
	loopConfig.SessionConfig.WorkingDir = tools.WorkingDir
	loopConfig.SystemPromptFile = "system_prompt.txt"
	loopConfig.EnableTools = true
	loopConfig.EnableThinking = true
	loopConfig.ThinkingPeerID = config.ThinkingPeerID
	loopConfig.EnableLogging = true
	loopConfig.Debug = *debug

	// Создаём AgentLoop
	agentLoop, err := agentloop.NewAgentLoop(loopConfig, vkClient, toolRegistry)
	if err != nil {
		println("Error creating AgentLoop:", err.Error())
		os.Exit(1)
	}

	// Загружаем сессию для основного peerID при старте
	if config.PeerID > 0 {
		agentLoop.EnsureSession(config.PeerID)
	}

	// Устанавливаем callback для отправки thinking сообщений
	agentLoop.SetThinkingCallback(func(peerID int64, content string) error {
		if vkClient == nil || config.ThinkingPeerID <= 0 {
			return nil
		}
		_, err := vkClient.SendThinking(config.ThinkingPeerID, content)
		return err
	})

	// Создаём Bot Handler с mainPeerID
	botHandler := vk.NewBotHandlerWithPeerID(vkClient, agentLoop, log, config.PeerID, config.ThinkingPeerID)

	// Настраиваем обработку сигналов
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.InfoLog("Shutting down...")
		if mcpManager != nil {
			mcpManager.Close()
		}
		cancel()
	}()

	// Отправляем статус о запуске в peer_id
	if config.PeerID > 0 {
		startMsg := fmt.Sprintf("🤖 AI Agent запущен и готов к работе.\nРабочая директория: %s\nДоступно инструментов: %d",
			tools.WorkingDir, len(toolRegistry.GetAll()))
		keyboard := vk.CreateCommandKeyboard()
		if _, err := vkClient.SendMessageWithKeyboard(config.PeerID, startMsg, keyboard); err != nil {
			log.WarnLogf("Failed to send startup message: %v", err)
		}
	}

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
	PeerID         int64   `json:"peer_id"`          // Основной чат для ответов
	ThinkingPeerID int64   `json:"thinking_peer_id"` // Чат для thinking сообщений
	MCPConfigPath  string  `json:"mcp_config_path"`  // Путь к конфигурации MCP серверов
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

// loadMCPConfig загружает конфигурацию MCP серверов
func loadMCPConfig(path string) (*mcp.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config mcp.Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}
