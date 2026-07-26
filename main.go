package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/opencode/llama-client/pkg/access"
	"github.com/opencode/llama-client/pkg/agent"
	"github.com/opencode/llama-client/pkg/agentloop"
	"github.com/opencode/llama-client/pkg/logger"
	"github.com/opencode/llama-client/pkg/mcp"
	"github.com/opencode/llama-client/pkg/store"
	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/pkg/vk"
	"github.com/opencode/llama-client/session"
)

var Version = "dev"

type Config struct {
	LlamaServerURL string   `json:"llama_server_url"`
	Model          string   `json:"model"`
	MaxTokens      int      `json:"max_tokens"`
	Temperature    float64  `json:"temperature"`
	TokenVK        string   `json:"token_vk"`
	PeerID         int64    `json:"peer_id"`
	ThinkingPeerID int64    `json:"thinking_peer_id"`
	MCPConfigPath  string   `json:"mcp_config_path"`
	AllowedDirs    []string `json:"allowed_dirs"`
	DBPath         string   `json:"db_path"`
	PromptsDir     string   `json:"prompts_dir"`
}

func main() {
	debug := flag.Bool("d", false, "Enable debug mode")
	reset := flag.Bool("r", false, "Reset session on startup")
	workDir := flag.String("w", "", "Force working directory (default: current dir)")
	initialPrompt := flag.String("p", "", "Send initial prompt after startup")
	flag.Parse()

	agentDir, _ := os.Getwd()

	config, err := loadConfig(filepath.Join(agentDir, "config.json"))
	if err != nil {
		println("Error loading config:", err.Error())
		os.Exit(1)
	}

	sysPromptPath := filepath.Join(agentDir, "system_prompt.txt")

	if *workDir != "" {
		absDir, err := filepath.Abs(*workDir)
		if err != nil {
			println("Error resolving working directory:", err.Error())
			os.Exit(1)
		}
		if err := os.Chdir(absDir); err != nil {
			println("Error changing working directory:", err.Error())
			os.Exit(1)
		}
		tools.SetWorkingDir(absDir)
	}

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
	logger.InitGlobalLogger(logConfig)
	log.InfoLog("VK Bot Gateway v%s starting...", Version)

	dbPath := config.DBPath
	if dbPath == "" {
		dbPath = "./agent.db"
	}
	if *reset {
		os.Remove(dbPath)
	}

	var dbStore store.Store
	dbStore, err = store.NewStore(dbPath)
	if err != nil {
		log.WarnLogf("Failed to open SQLite store: %v, using JSON fallback", err)
		dbStore = nil
	} else {
		log.InfoLog("SQLite store initialized: %s", dbPath)
	}

	vkClient := vk.NewBotClient(config.TokenVK)

	toolRegistry := tools.NewRegistry()
	registerTools(toolRegistry)

	allowedDirs := []string{tools.WorkingDir}
	allowedDirs = append(allowedDirs, config.AllowedDirs...)
	accessController := access.NewController(allowedDirs)
	tools.SetAccessController(accessController)
	log.InfoLogf("Access control: allowed dirs = %v", accessController.AllowedDirs())

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
				log.InfoLogf("MCP servers: %s", mcpManager.Stats())
			}
		}
	}

	loopConfig := agentloop.DefaultLoopConfig()
	llamaURL := config.LlamaServerURL
	if !strings.HasPrefix(llamaURL, "http://") && !strings.HasPrefix(llamaURL, "https://") {
		llamaURL = "http://" + llamaURL
	}
	loopConfig.LlamaServerURL = llamaURL
	loopConfig.Model = config.Model
	loopConfig.MaxTokens = config.MaxTokens
	loopConfig.Temperature = config.Temperature

	if dbStore != nil {
		loopConfig.SessionConfig.Store = dbStore
	} else {
		loopConfig.SessionConfig.SessionFile = "./sessions/vk_session.json"
	}
	loopConfig.SessionConfig.AutoSave = true
	loopConfig.SessionConfig.WorkingDir = tools.WorkingDir

	loopConfig.SystemPromptFile = sysPromptPath
	loopConfig.EnableTools = true
	loopConfig.EnableThinking = true
	loopConfig.ThinkingPeerID = config.ThinkingPeerID
	loopConfig.EnableLogging = true
	loopConfig.Debug = *debug

	agentLoop, err := agentloop.NewAgentLoop(loopConfig, vkClient, toolRegistry)
	if err != nil {
		println("Error creating AgentLoop:", err.Error())
		os.Exit(1)
	}

	if config.PeerID > 0 {
		agentLoop.EnsureSession(config.PeerID)
	}

	agentLoop.SetThinkingCallback(func(peerID int64, content string) error {
		if vkClient == nil || config.ThinkingPeerID <= 0 {
			return nil
		}
		_, err := vkClient.SendThinking(config.ThinkingPeerID, content)
		return err
	})

	tools.SetQuestionCallback(func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		return handleQuestion(vkClient, peerID, q)
	})

	// Регистрируем subagent tool как обычный тул (как task в opencode)
	sysPromptDir := filepath.Join(agentDir, "system_prompt")
	subAgentCfg := agent.Config{
		LlamaServerURL: llamaURL,
		Model:          config.Model,
		MaxTokens:      config.MaxTokens,
		Temperature:    config.Temperature,
		EnableTools:    true,
		MaxToolCalls:   10,
		Debug:          *debug,
		SessionConfig: session.Config{
			AutoSave:    false,
			SessionFile: "",
			MaxHistory:  100,
		},
	}
	toolRegistry.Register(&agentloop.SubAgentTool{
		AgentConfig:     subAgentCfg,
		MainTools:       toolRegistry,
		SystemPromptDir: sysPromptDir,
		CurrentDepth:    0,
		MaxDepth:        2,
		ThinkingPeerID:  config.ThinkingPeerID,
		VKClient:        vkClient,
		Log:             log,
		Debug:           *debug,
		SetActiveAgent:  func(name string) {},
	})

	botHandler := vk.NewBotHandlerWithPeerID(vkClient, agentLoop, log,
		config.PeerID, config.ThinkingPeerID, nil)

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
		if dbStore != nil {
			dbStore.Close()
		}
		cancel()
	}()

	if config.PeerID > 0 {
		startMsg := fmt.Sprintf("AI Agent started.\nDir: %s\nTools: %d\nDB: %s",
			tools.WorkingDir, len(toolRegistry.GetAll()), dbPath)
		keyboard := vk.CreateCommandKeyboard()
		if _, err := vkClient.SendMessageWithKeyboard(config.PeerID, startMsg, keyboard); err != nil {
			log.WarnLogf("Failed to send startup message: %v", err)
		}
	}

	// Запускаем обработчик бота
	log.InfoLog("Starting VK Bot Handler...")
	handlerCtx, handlerCancel := context.WithCancel(ctx)
	defer handlerCancel()

	go func() {
		if err := botHandler.Start(handlerCtx); err != nil {
			log.ErrorLogf("Bot handler error: %v", err)
			os.Exit(1)
		}
	}()

	// Если указан начальный промпт — отправляем его в обработку
	if *initialPrompt != "" && config.PeerID > 0 {
		log.InfoLogf("Processing initial prompt: %s", truncate(*initialPrompt, 100))

		promptCtx, promptCancel := context.WithTimeout(ctx, 10*time.Minute)
		response, err := agentLoop.ProcessPrompt(promptCtx, *initialPrompt, config.PeerID)
		promptCancel()

		if err != nil {
			errMsg := fmt.Sprintf("Initial prompt failed: %v", err)
			log.ErrorLog(errMsg)
			vkClient.SendMessage(config.PeerID, "❌ "+errMsg)
		} else if response != "" {
			log.InfoLogf("Initial prompt response: %s", truncate(response, 200))
			vkClient.SendMessage(config.PeerID, "✅ Result:\n"+response)
		} else {
			vkClient.SendMessage(config.PeerID, "⚠️ Initial prompt returned empty response")
		}
	}

	<-ctx.Done()
	log.InfoLog("VK Bot Gateway stopped")
}

var (
	pendingQuestions   map[int64]chan map[string]interface{}
	pendingQuestionsMu sync.Mutex
)

func init() {
	pendingQuestions = make(map[int64]chan map[string]interface{})
}

func handleQuestion(vkClient interface{ SendMessage(int64, string) (int64, error) }, peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
	text := buildQuestionText(q)

	if _, err := vkClient.SendMessage(peerID, text); err != nil {
		return nil, fmt.Errorf("send question: %w", err)
	}

	ch := make(chan map[string]interface{}, 1)
	pendingQuestionsMu.Lock()
	pendingQuestions[peerID] = ch
	pendingQuestionsMu.Unlock()

	defer func() {
		pendingQuestionsMu.Lock()
		delete(pendingQuestions, peerID)
		pendingQuestionsMu.Unlock()
	}()

	select {
	case answer := <-ch:
		return answer, nil
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("question timed out")
	}
}

func resolvePendingQuestion(peerID int64, text string) bool {
	pendingQuestionsMu.Lock()
	ch, ok := pendingQuestions[peerID]
	pendingQuestionsMu.Unlock()
	if !ok {
		return false
	}

	answer := map[string]interface{}{
		"answer":   text,
		"selected": []string{text},
	}
	ch <- answer
	return true
}

func buildQuestionText(q map[string]interface{}) string {
	question, _ := q["question"].(string)
	header, _ := q["header"].(string)
	custom, _ := q["custom"].(bool)

	var b strings.Builder
	if header != "" {
		b.WriteString(fmt.Sprintf("[%s]\n", header))
	}
	b.WriteString(question)

	if !custom {
		if opts, ok := q["options"].([]interface{}); ok {
			b.WriteString("\n\nOptions:")
			for _, opt := range opts {
				if o, ok := opt.(map[string]interface{}); ok {
					label, _ := o["label"].(string)
					desc, _ := o["description"].(string)
					b.WriteString(fmt.Sprintf("\n- %s", label))
					if desc != "" {
						b.WriteString(fmt.Sprintf(" (%s)", desc))
					}
				}
			}
			b.WriteString("\n\nReply with your choice")
		}
	} else {
		b.WriteString("\n\nReply with your answer")
	}

	return b.String()
}

func registerTools(r *tools.Registry) {
	r.Register(&tools.FileReadTool{})
	r.Register(&tools.FileWriteTool{})
	r.Register(&tools.TimeGetTool{})
	r.Register(&tools.DirListTool{})
	r.Register(&tools.ShellExecuteTool{})
	r.Register(&tools.WebFetchTool{})
	r.Register(&tools.WebSearchTool{})
	r.Register(&tools.GlobTool{})
	r.Register(&tools.GrepTool{})
	r.Register(&tools.CalcTool{})
	r.Register(&tools.EditTool{})
	r.Register(&tools.ApplyPatchTool{})
	r.Register(&tools.QuestionTool{})
}

func loadConfig(path string) (Config, error) {
	var config Config

	homeDir, _ := os.UserHomeDir()
	globalPath := filepath.Join(homeDir, ".config", "ai-agent", "config.json")

	loadPath := path
	if _, err := os.Stat(globalPath); err == nil {
		loadPath = globalPath
	}

	data, err := os.ReadFile(loadPath)
	if err != nil {
		return config, fmt.Errorf("config not found at '%s' or '%s': %w", path, globalPath, err)
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return config, err
	}
	return config, nil
}

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

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
