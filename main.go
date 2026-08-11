package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/opencode/llama-client/pkg/access"
	"github.com/opencode/llama-client/pkg/agent"
	"github.com/opencode/llama-client/pkg/agentloop"
	"github.com/opencode/llama-client/pkg/agentpolicy"
	"github.com/opencode/llama-client/pkg/buildinfo"
	"github.com/opencode/llama-client/pkg/logger"
	"github.com/opencode/llama-client/pkg/mcp"
	"github.com/opencode/llama-client/pkg/modelsconfig"
	"github.com/opencode/llama-client/pkg/store"
	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/pkg/util/stringutil"
	"github.com/opencode/llama-client/pkg/vk"
	"github.com/opencode/llama-client/session"
)

var Version = "dev"

type Config struct {
	TokenVK             string                          `json:"token_vk"`
	PeerID              int64                           `json:"peer_id"`
	ThinkingPeerID      int64                           `json:"thinking_peer_id"`
	MaxTokens           int                             `json:"max_tokens"`
	ModelLimitInput     int                             `json:"model_limit_input"`
	Temperature         float64                         `json:"temperature"`
	MCPConfigPath       string                          `json:"mcp_config_path"`
	AllowedDirs         []string                        `json:"allowed_dirs"`
	DBPath              string                          `json:"db_path"`
	PromptsDir          string                          `json:"prompts_dir"`
	MaxReviewIterations int                             `json:"max_review_iterations"`
	Agents              map[string]agentpolicy.AgentCfg `json:"agents"`
	ToolOutput          ToolOutputConfig                `json:"tool_output"`
	// SkipShellPermissionForPathless — не запрашивать разрешение для shell-команд
	// без явных файловых операций (нет путей и редиректов).
	SkipShellPermissionForPathless bool `json:"skip_shell_permission_without_paths"`
}

// ToolOutputConfig задаёт лимиты вывода инструментов перед отправкой в LLM
// (стиль opencode). Если поля не заданы — используются дефолты opencode.
type ToolOutputConfig struct {
	MaxLines int `json:"max_lines"`
	MaxBytes int `json:"max_bytes"`
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

	modelHolder, err := modelsconfig.NewHolder(filepath.Join(agentDir, "models.json"))
	if err != nil {
		println("Error loading models.json:")
		println(err.Error())
		os.Exit(1)
	}

	sysPromptPath := filepath.Join(agentDir, "system_prompt.txt")

	if *workDir != "" {
		absDir, err := filepath.Abs(*workDir)
		if err != nil {
			println("Error resolving working directory:", err.Error())
			os.Exit(1)
		}
		tools.SetWorkingDir(absDir)
	}

	logConfig := logger.DefaultConfig()
	logConfig.Level = logger.LevelDebug
	logConfig.MaxSizeMB = 5
	logConfig.File = "debug/debug.log"
	if !*debug {
		logConfig.Level = logger.LevelInfo
	}
	log, err := logger.New(logConfig)
	if err != nil {
		println("Error creating logger:", err.Error())
		os.Exit(1)
	}
	logger.InitGlobalLogger(logConfig)
	log.InfoLog("VK Bot Gateway v%s starting...", Version)
	log.InfoLog("Build time: %s", buildinfo.HumanReadable())

	dbPath := config.DBPath
	if dbPath == "" {
		dbPath = "./agent.db"
	}
	if *reset {
		os.Remove(dbPath)
		tools.GlobalTodo.Reset()
	}

	var dbStore store.Store
	dbStore, err = store.NewStore(dbPath)
	if err != nil {
		log.WarnLogf("Failed to open SQLite store: %v, using JSON fallback", err)
		dbStore = nil
	} else {
		log.InfoLog("SQLite store initialized: %s", dbPath)
	}

	if dbStore != nil {
		tools.SetGrantPersistence(
			func(peerID int64, path string) {
				sessionID := fmt.Sprintf("%d", peerID)
				if err := dbStore.SavePermission(sessionID, "*", path, "allow"); err != nil {
					log.WarnLogf("Failed to persist path grant: %v", err)
				}
			},
			func(peerID int64) {
				sessionID := fmt.Sprintf("%d", peerID)
				if err := dbStore.ClearPermissions(sessionID); err != nil {
					log.WarnLogf("Failed to clear grants: %v", err)
				}
			},
		)
		sessions, err := dbStore.GetDistinctGrantSessions()
		if err != nil {
			log.WarnLogf("Failed to list grant sessions: %v", err)
		} else {
			for _, sessionID := range sessions {
				perms, err := dbStore.GetPermissions(sessionID)
				if err != nil {
					log.WarnLogf("Failed to load permissions for session %s: %v", sessionID, err)
					continue
				}
				for _, p := range perms {
					if p.Decision == "allow" && p.ToolName == "*" {
						peerID, _ := strconv.ParseInt(sessionID, 10, 64)
						tools.ApplyPathGrant(peerID, p.Resource)
						log.DebugLogf("Loaded path grant: peer=%d path=%s", peerID, p.Resource)
					}
				}
			}
		}

		// Восстанавливаем workingDir из стора для основного peer
		if config.PeerID > 0 {
			sd, err := dbStore.GetSession(config.PeerID)
			if err != nil {
				log.WarnLogf("Failed to restore working dir from store: %v", err)
			} else if sd != nil && sd.WorkingDir != "" {
				tools.SetWorkingDir(sd.WorkingDir)
				log.InfoLogf("Restored working dir from store: %s", sd.WorkingDir)
			}
		}
	}

	tools.GlobalTodo.Reset()

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

	// Лимит контекста: из models.json, иначе реальный контекст с llama-server.
	// Кэшируется по алиасу модели; при /r <alias> для новой модели запрашивается заново.
	ctxResolver := agentloop.NewModelContextResolver(modelHolder, log)
	maxTokens, err := retryResolveContext(ctxResolver, log)
	if err != nil {
		println("Error resolving model context:", err.Error())
		os.Exit(1)
	}
	log.InfoLogf("Model context: %d tokens", maxTokens)

	tools.SetImage2TextConfig(tools.Image2TextConfig{
		ModelHolder: modelHolder,
		MaxTokens:   maxTokens,
	})

	if modelHolder.GetCurrentVision() {
		toolRegistry.Register(&tools.Image2TextTool{})
		log.InfoLogf("image2text tool registered (vision model)")
	} else {
		log.InfoLogf("image2text tool NOT registered (model is not vision-capable)")
	}

	loopConfig := agentloop.DefaultLoopConfig()
	loopConfig.ModelHolder = modelHolder
	loopConfig.ContextResolver = ctxResolver
	loopConfig.MaxTokens = maxTokens
	loopConfig.ModelLimitInput = config.ModelLimitInput
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
	loopConfig.SkipShellPermissionForPathless = config.SkipShellPermissionForPathless
	loopConfig.ToolOutputMaxLines = config.ToolOutput.MaxLines
	loopConfig.ToolOutputMaxBytes = config.ToolOutput.MaxBytes

	agentLoop, err := agentloop.NewAgentLoop(loopConfig, vkClient, toolRegistry)
	if err != nil {
		println("Error creating AgentLoop:", err.Error())
		os.Exit(1)
	}

	if config.PeerID > 0 {
		if *reset {
			// Сброс слотов: очищаем серверные слоты и KV-cache файлы от
			// предыдущего запуска, затем сбрасываем сессию основного пира.
			clearCtx, clearCancel := context.WithTimeout(context.Background(), 30*time.Second)
			agentLoop.ClearAllSlots(clearCtx)
			clearCancel()
			agentLoop.ResetSession(config.PeerID)
		} else {
			agentLoop.EnsureSession(config.PeerID)
			// Продолжаем незавершённую задачу главного агента после рестарта
			// (сессия уже восстановлена из БД вместе с resume_prompt).
			go func() {
				resumeCtx, resumeCancel := context.WithTimeout(context.Background(), 30*time.Minute)
				defer resumeCancel()
				agentLoop.ResumeInterruptedTask(resumeCtx, config.PeerID)
			}()
		}
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

	agentManager := initAgentManager(config.Agents, agentDir, log)

	alias, modelName, llamaURL := modelHolder.GetCurrent()
	sysPromptDir := filepath.Join(agentDir, "agents")
	subAgentCfg := agent.Config{
		LlamaServerURL:      llamaURL,
		Model:               modelName,
		MaxTokens:           maxTokens,
		ModelLimitInput:     config.ModelLimitInput,
		Temperature:         config.Temperature,
		EnableTools:         true,
		MaxToolCalls:        10,
		ToolOutputMaxLines:  config.ToolOutput.MaxLines,
		ToolOutputMaxBytes:  config.ToolOutput.MaxBytes,
		Debug:               *debug,
		SessionConfig: session.Config{
			AutoSave:    false,
			SessionFile: "",
		},
	}
	toolRegistry.Register(&agentloop.SubAgentTool{
		AgentConfig:     subAgentCfg,
		ContextResolver: ctxResolver,
		MainTools:       toolRegistry,
		SystemPromptDir: sysPromptDir,
		AgentManager:    agentManager,
		CurrentDepth:    0,
		MaxDepth:        4,
		PeerID:          config.PeerID,
		ThinkingPeerID:  config.ThinkingPeerID,
		VKClient:        vkClient,
		Log:             log,
		Debug:           *debug,
		ModelHolder:     modelHolder,
		SetActiveAgent:  func(name string) {},
		Store:           dbStore,
		SlotManager:     agentLoop.GetSlotManager(),
		Slots:           agentLoop.GetSlots(),
	})

	orchestrator := agentloop.NewOrchestrator(agentloop.OrchestratorConfig{
		ModelHolder:         modelHolder,
		ContextResolver:     ctxResolver,
		MaxTokens:           maxTokens,
		ModelLimitInput:     config.ModelLimitInput,
		Temperature:         config.Temperature,
		ToolRegistry:        toolRegistry,
		ToolOutputMaxLines:  config.ToolOutput.MaxLines,
		ToolOutputMaxBytes:  config.ToolOutput.MaxBytes,
		Debug:               *debug,
		Logger:              log,
		ThinkingPeerID:      config.ThinkingPeerID,
		VKClient:            vkClient,
		SystemPromptDir:     sysPromptDir,
		AgentManager:        agentManager,
		MaxReviewIterations: config.MaxReviewIterations,
		Store:               dbStore,
		SlotManager:         agentLoop.GetSlotManager(),
		Slots:               agentLoop.GetSlots(),
	})

	botHandler := vk.NewBotHandlerWithPeerID(vkClient, agentLoop, log,
		config.PeerID, config.ThinkingPeerID, orchestrator, modelHolder)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Восстанавливаем незавершённые цепочки сабагентов после рестарта
	if dbStore != nil {
		go func() {
			resumeCtx, resumeCancel := context.WithTimeout(ctx, 30*time.Minute)
			defer resumeCancel()
			if err := orchestrator.ResumeActiveChains(resumeCtx); err != nil {
				log.WarnLogf("Resume active agent chains: %v", err)
			}
		}()
	}

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
		startMsg := fmt.Sprintf("AI Agent started.\nDir: %s\nTools: %d\nModel: %s (%s)",
			tools.WorkingDir, len(toolRegistry.GetAll()), alias, modelName)
		if modelHolder.GetCurrentVision() {
			startMsg += "\nVision: yes"
		}
		startMsg += fmt.Sprintf("\nBuild: %s", buildinfo.HumanReadable())
		keyboard := vk.CreateCommandKeyboard()
		if _, err := vkClient.SendMessageWithKeyboard(config.PeerID, startMsg, keyboard); err != nil {
			log.WarnLogf("Failed to send startup message: %v", err)
		}
	}

	log.InfoLog("Starting VK Bot Handler...")
	handlerCtx, handlerCancel := context.WithCancel(ctx)
	defer handlerCancel()

	go func() {
		if err := botHandler.Start(handlerCtx); err != nil {
			log.ErrorLogf("Bot handler error: %v", err)
			cancel()
		}
	}()

	if *initialPrompt != "" && config.PeerID > 0 {
		prompt := *initialPrompt

		knownNames := agentManager.ListAgentNames()
		agentName, task := vk.ParseAgentHashMention(prompt, knownNames)
		if agentName != "" && orchestrator != nil && task != "" {
			if orchestrator.IsPrimary(agentName) {
				// Primary-агент — на главном персистентном агенте с общим
				// контекстом и временно добавленным системным промптом.
				log.InfoLogf("Processing initial prompt via main agent (#%s): %s", agentName, stringutil.Truncate(task, 100, "..."))
				extraPrompt, perr := orchestrator.GetSystemPrompt(agentName)
				if perr != nil {
					errMsg := fmt.Sprintf("Initial prompt failed: %v", perr)
					log.ErrorLogf("Initial prompt failed: %v", perr)
					vkClient.SendMessage(config.PeerID, "❌ "+errMsg)
					return
				}
				promptCtx, promptCancel := context.WithTimeout(ctx, 15*time.Minute)
				response, err := agentLoop.ProcessPromptWithExtraSystem(promptCtx, task, config.PeerID, extraPrompt)
				promptCancel()

				if err != nil {
					errMsg := fmt.Sprintf("Initial prompt failed: %v", err)
					log.ErrorLogf("Initial prompt failed: %v", err)
					vkClient.SendMessage(config.PeerID, "❌ "+errMsg)
				} else if response != "" {
log.InfoLogf("Initial prompt response: %s", stringutil.Truncate(response, 200, "..."))
				vkClient.SendMessage(config.PeerID, "✅ Result:\n"+response)
			} else {
				vkClient.SendMessage(config.PeerID, "⚠️ Initial prompt returned empty response")
			}
			return
		}

		log.InfoLogf("Processing initial prompt via RunAgent (#%s): %s", agentName, stringutil.Truncate(task, 100, "..."))

			promptCtx, promptCancel := context.WithTimeout(ctx, 15*time.Minute)
			response, err := orchestrator.RunAgent(promptCtx, agentName, task, config.PeerID)
			promptCancel()

			if err != nil {
				errMsg := fmt.Sprintf("Initial prompt failed: %v", err)
				log.ErrorLogf("Initial prompt failed: %v", err)
				vkClient.SendMessage(config.PeerID, "❌ "+errMsg)
			} else if response != "" {
				log.InfoLogf("Initial prompt response: %s", stringutil.Truncate(response, 200, "..."))
				vkClient.SendMessage(config.PeerID, "✅ Result:\n"+response)
			} else {
				vkClient.SendMessage(config.PeerID, "⚠️ Initial prompt returned empty response")
			}
		} else {
			log.InfoLogf("Processing initial prompt: %s", stringutil.Truncate(prompt, 100, "..."))

			promptCtx, promptCancel := context.WithTimeout(ctx, 10*time.Minute)
			response, err := agentLoop.ProcessPrompt(promptCtx, prompt, config.PeerID)
			promptCancel()

			if err != nil {
				errMsg := fmt.Sprintf("Initial prompt failed: %v", err)
				log.ErrorLogf("Initial prompt failed: %v", err)
				vkClient.SendMessage(config.PeerID, "❌ "+errMsg)
			} else if response != "" {
				log.InfoLogf("Initial prompt response: %s", stringutil.Truncate(response, 200, "..."))
				vkClient.SendMessage(config.PeerID, "✅ Result:\n"+response)
			} else {
				vkClient.SendMessage(config.PeerID, "⚠️ Initial prompt returned empty response")
			}
		}
	}

	<-ctx.Done()
	log.InfoLog("VK Bot Gateway stopped")
}

func extractQuestionOptions(q map[string]interface{}) []map[string]string {
	optsRaw, ok := q["options"]
	if !ok {
		return nil
	}
	data, err := json.Marshal(optsRaw)
	if err != nil {
		return nil
	}
	var items []map[string]interface{}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil
	}
	var result []map[string]string
	for _, item := range items {
		label, _ := item["label"].(string)
		if label != "" {
			result = append(result, map[string]string{"label": label})
		}
	}
	return result
}

func handleQuestion(vkClient interface {
	SendMessage(int64, string) (int64, error)
	SendMessageWithKeyboard(int64, string, map[string]interface{}) (int64, error)
}, peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
	if peerID <= 0 {
		logger.DebugToFile("[handleQuestion] invalid peerID=%d, cannot ask question", peerID)
		return nil, fmt.Errorf("invalid peerID: %d", peerID)
	}

	text := buildQuestionText(q)

	ch := tools.RegisterPendingQuestion(peerID)
	defer tools.UnregisterPendingQuestion(peerID)

	options := extractQuestionOptions(q)
	sendFailed := false
	if len(options) > 0 {
		header, _ := q["header"].(string)
		qText, _ := q["question"].(string)
		keyboard := vk.CreateQuestionKeyboard(header, qText, options)
		logger.DebugToFile("[handleQuestion] Sending question to peer %d: %s", peerID, text)
		if _, err := vkClient.SendMessageWithKeyboard(peerID, text, keyboard); err != nil {
			logger.DebugToFile("[handleQuestion] SendMessageWithKeyboard failed: %v", err)
			if _, err2 := vkClient.SendMessage(peerID, fmt.Sprintf("\u26a0\ufe0f %s\n\n%s", "Keyboard unavailable, reply with text:", text)); err2 != nil {
				logger.DebugToFile("[handleQuestion] fallback SendMessage also failed: %v", err2)
				sendFailed = true
			}
		}
	} else {
		if _, err := vkClient.SendMessage(peerID, text); err != nil {
			logger.DebugToFile("[handleQuestion] SendMessage failed: %v", err)
			sendFailed = true
		}
	}

	if sendFailed {
		return nil, fmt.Errorf("failed to send question to peer %d", peerID)
	}

	logger.DebugToFile("[handleQuestion] Waiting for answer from peer %d...", peerID)
	answer, err := waitForAnswer(ch)
	logger.DebugToFile("[handleQuestion] Got answer from peer %d: err=%v", peerID, err)
	if _, err2 := vkClient.SendMessageWithKeyboard(peerID, "\u2705 Done", vk.CreateCommandKeyboard()); err2 != nil {
		logger.DebugToFile("[handleQuestion] Reset keyboard failed: %v", err2)
	}
	return answer, err
}

func waitForAnswer(ch chan map[string]interface{}) (map[string]interface{}, error) {
	answer, ok := <-ch
	if !ok {
		return nil, fmt.Errorf("question cancelled")
	}
	return answer, nil
}

func buildQuestionText(q map[string]interface{}) string {
	question, _ := q["question"].(string)
	header, _ := q["header"].(string)

	var b strings.Builder
	if header != "" {
		b.WriteString(fmt.Sprintf("[%s]\n", header))
	}
	b.WriteString(question)

	options := extractQuestionOptions(q)
	if len(options) > 0 {
		b.WriteString("\n\nOptions:")
		for _, opt := range options {
			b.WriteString(fmt.Sprintf("\n- %s", opt["label"]))
		}
		b.WriteString("\n\nReply with your choice")
	} else {
		b.WriteString("\n\nReply with your answer")
	}

	return truncateQuestion(b.String())
}

// truncateQuestion обрезает текст вопроса до лимита VK (4096 символов),
// сохраняя при этом заголовок и список опций в конце.
func truncateQuestion(text string) string {
	const vkMessageLimit = 4096
	runes := []rune(text)
	if len(runes) <= vkMessageLimit {
		return text
	}

	// Находим блок опций в конце сообщения и сохраняем его целиком.
	marker := "\n\nOptions:"
	markerIdx := strings.LastIndex(text, marker)
	if markerIdx == -1 {
		runes = runes[:vkMessageLimit-len("...")]
		return string(runes) + "..."
	}

	optionsPart := text[markerIdx:]
	headLimit := vkMessageLimit - len([]rune(optionsPart)) - len("...")
	head := []rune(text[:markerIdx])
	if len(head) > headLimit {
		head = head[:headLimit]
	}
	return string(head) + "..." + optionsPart
}

// retryResolveContext пытается получить лимит контекста модели, ретрая
// бесконечно с паузой 5 секунд при недоступности llama-server.
// Ретрай прерывается только при отмене контекста (Ctrl+C / SIGTERM).
func retryResolveContext(resolver *agentloop.ModelContextResolver, log *logger.Logger) (int, error) {
	const retryDelay = 5 * time.Second
	for attempt := 1; ; attempt++ {
		ctx, err := resolver.Resolve()
		if err == nil {
			return ctx, nil
		}
		log.WarnLogf("Failed to resolve model context (attempt %d): %v", attempt, err)
		log.WarnLogf("Retrying in %v... (is llama-server running?)", retryDelay)
		time.Sleep(retryDelay)
	}
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
	r.Register(tools.GlobalTodo)
}

func loadConfig(path string) (Config, error) {
	var config Config

	data, err := os.ReadFile(path)
	if err != nil {
		homeDir, _ := os.UserHomeDir()
		globalPath := filepath.Join(homeDir, ".config", "ai-agent", "config.json")
		data, err = os.ReadFile(globalPath)
		if err != nil {
			return config, fmt.Errorf("config not found at '%s' or '%s': %w", path, globalPath, err)
		}
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

func initAgentManager(agents map[string]agentpolicy.AgentCfg, agentDir string, log interface{ InfoLogf(string, ...interface{}) }) *agentpolicy.AgentManager {
	am := agentpolicy.NewAgentManager()
	if agents == nil {
		log.InfoLogf("AgentManager: %d agents registered (defaults only)", len(am.ListAgentNames()))
		return am
	}
	resolved := make(map[string]agentpolicy.AgentCfg)
	for name, ac := range agents {
		if ac.Prompt != "" {
			promptPath := ac.Prompt
			if !filepath.IsAbs(promptPath) {
				promptPath = filepath.Join(agentDir, promptPath)
			}
			prompt, err := agentpolicy.LoadMDPrompt(promptPath)
			if err != nil {
				log.InfoLogf("Failed to load prompt for agent %s from %s: %v", name, promptPath, err)
				data, rErr := os.ReadFile(promptPath)
				if rErr != nil {
					log.InfoLogf("Skipping agent %s: cannot read prompt from %s", name, promptPath)
					continue
				}
				prompt = string(data)
			}
			ac.Prompt = prompt
		}
		resolved[name] = ac
	}
	am.LoadFromConfig(resolved)
	log.InfoLogf("AgentManager: %d agents registered", len(am.ListAgentNames()))
	return am
}

