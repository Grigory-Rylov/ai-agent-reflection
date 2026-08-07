package agentloop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opencode/llama-client/pkg/agent"
	"github.com/opencode/llama-client/pkg/agentpolicy"
	"github.com/opencode/llama-client/pkg/modelsconfig"
	"github.com/opencode/llama-client/pkg/store"
	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/session"
)

type OrchestratorConfig struct {
	ModelHolder     *modelsconfig.Holder
	ContextResolver *ModelContextResolver
	MaxTokens       int
	ModelLimitInput int
	Temperature     float64
	ToolRegistry    *tools.Registry
	Debug           bool
	Logger          Logger
	ThinkingPeerID  int64
	VKClient        VKClient
	SystemPromptDir      string
	MaxReviewIterations  int
	ToolOutputMaxLines   int
	ToolOutputMaxBytes   int
	AgentManager         *agentpolicy.AgentManager
	Store                store.Store
	SlotManager          *SlotManager
	Slots                *SlotClient
}

type Orchestrator struct {
	config      OrchestratorConfig
	thoughtPeer int64
	activeAgent string
	activeMu    sync.RWMutex
}

func NewOrchestrator(cfg OrchestratorConfig) *Orchestrator {
	return &Orchestrator{
		config:      cfg,
		thoughtPeer: cfg.ThinkingPeerID,
	}
}

func (o *Orchestrator) GetCurrentAgent() string {
	o.activeMu.RLock()
	defer o.activeMu.RUnlock()
	return o.activeAgent
}

func (o *Orchestrator) setActiveAgent(name string) {
	o.activeMu.Lock()
	o.activeAgent = name
	o.activeMu.Unlock()
}

func (o *Orchestrator) ExecuteTask(ctx context.Context, task string, peerID int64) (string, error) {
	o.debugLog("Mode activated. Task: %s", truncate(task, 200))
	startTime := time.Now()
	defer o.setActiveAgent("")

	o.debugLog("Starting worker...")
	o.setActiveAgent("worker")
	workerResult, err := o.runWorker(ctx, task, peerID)
	if err != nil {
		return "", fmt.Errorf("worker failed: %w", err)
	}
	o.debugLog("Worker completed: %d chars", len(workerResult))

	o.debugLog("Starting QA review...")
	o.setActiveAgent("qa")
	qaPrompt := fmt.Sprintf("Review the following implementation result:\n\n%s\n\nBuild and test the code. If issues found, fix them and approve when done.", workerResult)
	qaResult, err := o.runQA(ctx, qaPrompt, peerID)
	if err != nil {
		o.debugLog("QA failed, returning worker result: %v", err)
		return workerResult, nil
	}
	o.debugLog("QA completed: %d chars", len(qaResult))

	elapsed := time.Since(startTime)
	o.debugLog("Agent mode completed. Duration: %v", elapsed)
	return qaResult, nil
}

func (o *Orchestrator) RunAgent(ctx context.Context, agentName, task string, peerID int64) (string, error) {
	o.debugLog("RunAgent: %s. Task: %s", agentName, truncate(task, 200))
	o.setActiveAgent(agentName)
	defer o.setActiveAgent("")

	prompt, err := o.loadSystemPrompt(agentName)
	if err != nil {
		return "", fmt.Errorf("failed to load prompt for agent %q: %w", agentName, err)
	}

	if o.config.MaxReviewIterations > 0 {
		prompt += fmt.Sprintf("\n\nMaximum review iterations: %d. After this many developer↔reviewer cycles, move forward regardless.", o.config.MaxReviewIterations)
	}

	a, sessionID, err := o.makeSubAgent(agentName, prompt, peerID)
	if err != nil {
		return "", err
	}

	// Персистим корневую сессию и стартовую цепочку, чтобы после рестарта
	// цепочка lead → worker → reviewer восстановилась вплоть до лида.
	rootID := o.beginRootSession(agentName, prompt, task, peerID, sessionID)
	var rootChain []string
	if rootID != "" {
		rootChain = []string{rootID}
	}

	if err := o.setupAgentTools(agentName, a, peerID, rootID, rootChain); err != nil {
		o.releaseAgentSlot(sessionID)
		return "", err
	}

	response, err := a.ProcessMessage(ctx, task, peerID)
	if err != nil {
		// Слот освобождаем (KV-cache после падения всё равно устарел), но
		// цепочку/БД не чистим — незавершённую работу восстановит ResumeActiveChains.
		// Сохраняем историю, чтобы восстановленная сессия не была пустой.
		if rootID != "" {
			o.saveAgentHistory(a, rootID, peerID, task)
		}
		o.releaseAgentSlot(sessionID)
		return "", fmt.Errorf("agent %q failed: %w", agentName, err)
	}

	// KV-cache сохраняется per-response внутри agent_impl (через SlotSaver),
	// поэтому здесь только освобождаем слот.
	if rootID != "" {
		o.endRootSession(peerID, rootID)
	} else {
		// Стора нет, но слот мог быть выделен — освободим.
		o.releaseAgentSlot(sessionID)
	}

	return response, nil
}

// beginRootSession сохраняет корневую сессию агента и цепочку [rootID] в БД.
// sessionID — тот же ID, что привязан к слоту и к session.SessionID агента.
// Возвращает пустую строку, если стор не настроен.
func (o *Orchestrator) beginRootSession(agentName, systemPrompt, task string, peerID int64, sessionID string) string {
	if o.config.Store == nil || sessionID == "" {
		return ""
	}
	o.config.Store.SaveAgentSession(&store.AgentSessionData{
		ID:           sessionID,
		AgentName:    agentName,
		PeerID:       peerID,
		SystemPrompt: systemPrompt,
		LastPrompt:   task,
		Status:       "active",
	})
	o.config.Store.SaveAgentChain(peerID, []string{sessionID})
	return sessionID
}

// saveAgentHistory сохраняет историю сообщений сессии агента в БД
// (agent_sessions.messages). Нужно, чтобы ResumeActiveChains восстановил
// непустой контекст, а не пустую сессию.
func (o *Orchestrator) saveAgentHistory(a agent.Agent, sessionID string, peerID int64, lastPrompt string) {
	if o.config.Store == nil || sessionID == "" || a == nil {
		return
	}
	data, err := json.Marshal(a.GetSession(peerID).GetHistory())
	if err != nil {
		o.debugLog("save history for %s failed: %v", sessionID, err)
		return
	}
	if err := o.config.Store.UpdateAgentSession(sessionID, lastPrompt, string(data)); err != nil {
		o.debugLog("save history for %s failed: %v", sessionID, err)
	}
}

// releaseAgentSlot освобождает слот агента (удаляет файл, очищает серверный
// слот, возвращает в пул). No-op без SlotManager. Вызывается при завершении
// агента — успехе, ошибке или сбросе, — чтобы слот не утёк и следующий агент
// стартовал со свободного слота.
func (o *Orchestrator) releaseAgentSlot(sessionID string) {
	ReleaseSessionSlot(o.config.SlotManager, o.config.Slots, o.config.ModelHolder, sessionID, o.config.Logger)
}

// endRootSession освобождает слот корневой сессии, удаляет её строку в БД и
// очищает цепочку после успешного завершения агента.
func (o *Orchestrator) endRootSession(peerID int64, rootID string) {
	o.releaseAgentSlot(rootID)
	if o.config.Store == nil || rootID == "" {
		return
	}
	o.config.Store.DeleteAgentSession(rootID)
	o.config.Store.SaveAgentChain(peerID, nil)
}

// setupAgentTools регистрирует инструменты агента в зависимости от его типа.
// sessionID — UUID текущей сессии агента, chain — цепочка от корня до неё
// (нужны SubAgentTool для персистентности вложенных вызовов).
func (o *Orchestrator) setupAgentTools(name string, a agent.Agent, peerID int64, sessionID string, chain []string) error {
	switch {
	case o.isCoordinator(name):
		o.debugLog("[TOOL] Coordinator mode for %s: read-only + task tool", name)
		o.addReadOnlyTools(a)
		return o.registerSubAgentTool(name, a, peerID, sessionID, chain)

	case o.isReviewAgent(name):
		o.debugLog("[TOOL] Review mode for %s: read-only + approve tool", name)
		o.addReadOnlyTools(a)
		o.registerReviewTool(a)
	default:
		o.debugLog("[TOOL] Full mode for %s: all tools", name)
		o.addMainTools(a)
		if !o.isLeafAgent(name) {
			o.debugLog("[TOOL] Adding subagent tool for %s", name)
			return o.registerSubAgentTool(name, a, peerID, sessionID, chain)
		} else {
			o.debugLog("[TOOL] %s is leaf — no subagent tool", name)
		}
	}
	return nil
}

func (o *Orchestrator) ListAgentNames() []string {
	if o.config.AgentManager != nil {
		return o.config.AgentManager.ListAgentNames()
	}
	return nil
}

func (o *Orchestrator) GetActiveAgentSessions(peerID int64) (string, error) {
	if o.config.Store == nil {
		return "No store configured", nil
	}

	sessions, err := o.config.Store.GetActiveAgentSessions(peerID)
	if err != nil {
		return "", err
	}

	chainData, err := o.config.Store.GetAgentChain(peerID)
	if err != nil {
		chainData = nil
	}

	var b strings.Builder
	if len(sessions) == 0 {
		b.WriteString("No active agent sessions.")
		return b.String(), nil
	}

	b.WriteString("Active agent sessions:\n\n")
	for _, s := range sessions {
		arrow := "  "
		for i, id := range chainData.Chain {
			if id == s.ID {
				arrow = strings.Repeat("  ", i) + "→ "
				break
			}
		}
		b.WriteString(fmt.Sprintf("%s[%s] %s (id: %s)\n", arrow, s.Status, s.AgentName, s.ID))
	}

	return b.String(), nil
}

func (o *Orchestrator) ClearActiveSessions(peerID int64) {
	if o.config.Store == nil {
		return
	}
	o.config.Store.ClearAgentChain(peerID)
}

// ResumeActiveChains восстанавливает незавершённые цепочки сабагентов после рестарта.
// Проходит цепочки от самой глубокой сессии к корневой, «всплывая» результат наверх.
func (o *Orchestrator) ResumeActiveChains(ctx context.Context) error {
	if o.config.Store == nil {
		return nil
	}
	chains, err := o.config.Store.GetAllActiveChains()
	if err != nil {
		return fmt.Errorf("get active chains: %w", err)
	}
	for _, chain := range chains {
		if err := o.resumeChain(ctx, chain); err != nil {
			o.debugLog("Resume chain for peer %d failed: %v", chain.PeerID, err)
		}
	}
	return nil
}

// resumeChain восстанавливает одну цепочку: от самой глубокой сессии к корневой.
func (o *Orchestrator) resumeChain(ctx context.Context, chain store.AgentChainData) error {
	if len(chain.Chain) == 0 {
		return nil
	}
	var childResult string
	for i := len(chain.Chain) - 1; i >= 0; i-- {
		id := chain.Chain[i]
		sd, err := o.config.Store.GetAgentSession(id)
		if err != nil {
			return err
		}
		if sd == nil {
			o.config.Store.SaveAgentChain(chain.PeerID, chain.Chain[:i])
			continue
		}
		result, err := o.runResumedAgent(ctx, sd, childResult, chain.Chain[:i+1])
		if err != nil {
			o.config.Store.DeleteAgentSession(id)
			o.config.Store.SaveAgentChain(chain.PeerID, chain.Chain[:i])
			return fmt.Errorf("resume agent %q: %w", sd.AgentName, err)
		}
		childResult = result
		o.config.Store.DeleteAgentSession(id)
		o.config.Store.SaveAgentChain(chain.PeerID, chain.Chain[:i])
	}
	if childResult != "" && o.config.VKClient != nil {
		o.config.VKClient.SendMessage(chain.PeerID, "✅ Resumed result:\n"+childResult)
	}
	return nil
}

// runResumedAgent пересоздаёт агента из сохранённой сессии и запускает его.
// chain — цепочка сессий от корня до восстанавливаемого агента (включая его).
func (o *Orchestrator) runResumedAgent(ctx context.Context, sd *store.AgentSessionData, childResult string, chain []string) (string, error) {
	a, sessionID, err := o.makeSubAgent(sd.AgentName, sd.SystemPrompt, sd.PeerID)
	if err != nil {
		return "", err
	}
	// Привязываем инструменты к исходной сессии (sd.ID), чтобы цепочка
	// продолжалась от того же корня; слот же — у свежей сессии sessionID.
	if err := o.setupAgentTools(sd.AgentName, a, sd.PeerID, sd.ID, chain); err != nil {
		o.releaseAgentSlot(sessionID)
		return "", err
	}
	o.restoreSessionMessages(a.GetSession(sd.PeerID), sd.Messages)

	prompt := "The process was restarted. Continue your task from where you left off."
	switch {
	case childResult != "":
		prompt = fmt.Sprintf("Your sub-agent completed with this result:\n\n%s\n\nContinue your task from where you left off.", childResult)
	case sd.LastPrompt != "":
		prompt = fmt.Sprintf("Continue your task: %s", sd.LastPrompt)
	}
	result, err := a.ProcessMessage(ctx, prompt, sd.PeerID)
	// KV-cache сохраняется per-response внутри agent_impl (SlotSaver).
	o.releaseAgentSlot(sessionID)
	return result, err
}

// restoreSessionMessages восстанавливает историю сообщений в сессии агента
// из сохранённого JSON (формат []session.Message).
func (o *Orchestrator) restoreSessionMessages(s *session.Session, messagesJSON string) {
	if messagesJSON == "" {
		return
	}
	var msgs []session.Message
	if err := json.Unmarshal([]byte(messagesJSON), &msgs); err != nil {
		o.debugLog("Resume: failed to parse saved messages: %v", err)
		return
	}
	// Восстанавливаем историю целиком, сохраняя метаданные компактизации
	// (Summary/Compacted/TailStartID) — маркеры переживают резюм после рестарта.
	s.RestoreMessages(msgs)
}

func (o *Orchestrator) isLeafAgent(name string) bool {
	if o.config.AgentManager != nil {
		if info, err := o.config.AgentManager.GetAgent(name); err == nil {
			return info.Leaf
		}
	}
	return false
}

func (o *Orchestrator) isReviewAgent(name string) bool {
	if o.config.AgentManager != nil {
		if info, err := o.config.AgentManager.GetAgent(name); err == nil {
			return info.Review
		}
	}
	return false
}

func (o *Orchestrator) isCoordinator(name string) bool {
	if o.config.AgentManager != nil {
		info, err := o.config.AgentManager.GetAgent(name)
		if err != nil {
			o.debugLog("isCoordinator(%q): GetAgent error: %v", name, err)
			return false
		}
		o.debugLog("isCoordinator(%q): Coordinator=%v Leaf=%v Review=%v", name, info.Coordinator, info.Leaf, info.Review)
		return info.Coordinator
	}
	o.debugLog("isCoordinator(%q): AgentManager is nil", name)
	return false
}

func (o *Orchestrator) runWorker(ctx context.Context, task string, peerID int64) (string, error) {
	prompt, err := o.loadSystemPrompt("worker")
	if err != nil {
		return "", err
	}
	a, sessionID, err := o.makeSubAgent("worker", prompt, peerID)
	if err != nil {
		return "", err
	}
	o.addMainTools(a)
	o.beginLeafSession("worker", prompt, task, peerID, sessionID)
	result, err := a.ProcessMessage(ctx, task, peerID)
	if err != nil {
		o.saveAgentHistory(a, sessionID, peerID, task)
		o.releaseAgentSlot(sessionID)
		return "", err
	}
	// KV-cache сохраняется per-response внутри agent_impl (SlotSaver).
	o.endLeafSession(peerID, sessionID)
	o.releaseAgentSlot(sessionID)
	return result, err
}

func (o *Orchestrator) runQA(ctx context.Context, task string, peerID int64) (string, error) {
	prompt, err := o.loadSystemPrompt("qa")
	if err != nil {
		return "", err
	}
	a, sessionID, err := o.makeSubAgent("qa", prompt, peerID)
	if err != nil {
		return "", err
	}
	o.addMainTools(a)
	if err := o.registerSubAgentTool("qa", a, peerID, "", nil); err != nil {
		o.releaseAgentSlot(sessionID)
		return "", err
	}
	o.beginLeafSession("qa", prompt, task, peerID, sessionID)
	result, err := a.ProcessMessage(ctx, task, peerID)
	if err != nil {
		o.saveAgentHistory(a, sessionID, peerID, task)
		o.releaseAgentSlot(sessionID)
		return "", err
	}
	// KV-cache сохраняется per-response внутри agent_impl (SlotSaver).
	o.endLeafSession(peerID, sessionID)
	o.releaseAgentSlot(sessionID)
	return result, err
}

// beginLeafSession персистит сессию worker/qa и цепочку [sessionID] в БД,
// чтобы прерванную задачу восстановил ResumeActiveChains.
func (o *Orchestrator) beginLeafSession(agentName, systemPrompt, task string, peerID int64, sessionID string) {
	if o.config.Store == nil || sessionID == "" {
		return
	}
	o.config.Store.SaveAgentSession(&store.AgentSessionData{
		ID:           sessionID,
		AgentName:    agentName,
		PeerID:       peerID,
		SystemPrompt: systemPrompt,
		LastPrompt:   task,
		Status:       "active",
	})
	o.config.Store.SaveAgentChain(peerID, []string{sessionID})
}

// endLeafSession удаляет сессию worker/qa и цепочку после успешного завершения.
func (o *Orchestrator) endLeafSession(peerID int64, sessionID string) {
	if o.config.Store == nil || sessionID == "" {
		return
	}
	o.config.Store.DeleteAgentSession(sessionID)
	o.config.Store.SaveAgentChain(peerID, nil)
}

func (o *Orchestrator) makeSubAgent(name, systemPrompt string, peerID int64) (agent.Agent, string, error) {
	cfg, err := o.makeAgentConfig()
	if err != nil {
		return nil, "", err
	}
	cfg.SystemPromptFile = ""
	// Предгенерируем sessionID и прокидываем в сессию: a.GetSession().GetSessionID()
	// совпадёт с DB/slot ID — единый идентификатор сессии (как в главном цикле).
	sessionID := newSessionUUID(name, strconv.FormatInt(peerID, 10))
	cfg.SessionConfig = session.Config{
		AutoSave:    false,
		SessionFile: "",
		SessionID:   sessionID,
	}
	cfg.EnableLoopAlert = false
	cfg.EnableCompression = true
	cfg.MaxToolCalls = 10
	cfg.AgentName = name
	cfg.SlotID = -1
	cfg.SlotSave = false

	// Назначаем слот агенту (lead/worker/qa). cfg.SlotID пинит каждый LLM-запрос
	// к слоту этой сессии, пока агент жив — KV-cache переиспользуется между
	// вызовами инструментов и не конфликтует с другими активными агентами.
	// SlotSaver вызывается из agent_impl после каждого ответа LLM (только при
	// slot-save: true в models.json), сохраняя актуальный кэш в {model}_slot{N}.bin.
	if o.config.ModelHolder != nil && o.config.ModelHolder.GetCurrentSlotSave() {
		cfg.SlotSave = true
		if slotID := AssignSessionSlot(o.config.SlotManager, o.config.Slots, o.config.ModelHolder, sessionID, o.config.Logger); slotID >= 0 {
			cfg.SlotID = slotID
			cfg.SlotSaver = NewSlotSaver(o.config.SlotManager, o.config.Slots, o.config.ModelHolder, sessionID, o.config.Logger)
			if o.config.Logger != nil {
				o.config.Logger.InfoLogf("[SLOT] assigned slot %d to agent %s (session %s)", slotID, name, sessionID)
			}
		}
	}

	a := agent.NewAgent(cfg)

	if o.config.AgentManager != nil {
		if info, err := o.config.AgentManager.GetAgent(name); err == nil && info.Permission != nil && len(info.Permission) > 0 {
			a.SetPermissionChecker(agentpolicy.NewPermissionAdapter(info.Permission))
		}
	}

	a.SetThinkingCallback(o.makeThinkingCallback(name))
	a.GetSession(peerID).UpdateSystemPrompt(systemPrompt)
	return a, sessionID, nil
}

func (o *Orchestrator) loadSystemPrompt(name string) (string, error) {
	if o.config.AgentManager != nil {
		if info, err := o.config.AgentManager.GetAgent(name); err == nil && info.Prompt != "" {
			return info.Prompt, nil
		}
	}
	for _, ext := range []string{".txt", ".md"} {
		path := filepath.Join(o.systemPromptDir(), name+ext)
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data), nil
		}
	}
	return "", fmt.Errorf("failed to load prompt for %q from %s", name, o.systemPromptDir())
}

func (o *Orchestrator) systemPromptDir() string {
	if o.config.SystemPromptDir != "" {
		return o.config.SystemPromptDir
	}
	return "system_prompt"
}

func (o *Orchestrator) makeAgentConfig() (agent.Config, error) {
	alias, modelName, llamaURL := o.config.ModelHolder.GetCurrent()
	maxTokens := o.config.MaxTokens
	if o.config.ContextResolver != nil {
		ctx, err := o.config.ContextResolver.Resolve()
		if err != nil {
			return agent.Config{}, err
		}
		maxTokens = ctx
	} else if ctx := o.config.ModelHolder.GetModelContext(alias); ctx > 0 {
		// Контекст для текущей модели: из models.json (если указан), иначе общий.
		maxTokens = ctx
	}
	return agent.Config{
		LlamaServerURL:      llamaURL,
		Model:               modelName,
		MaxTokens:           maxTokens,
		ModelLimitInput:     o.config.ModelLimitInput,
		Temperature:         o.config.Temperature,
		SystemPromptFile:    o.systemPromptDir() + "/coordinator.txt",
		EnableTools:         true,
		MaxToolCalls:        10,
		EnableLoopAlert:     false,
		ToolOutputMaxLines:  o.config.ToolOutputMaxLines,
		ToolOutputMaxBytes:  o.config.ToolOutputMaxBytes,
		Debug:               o.config.Debug,
		AgentName:           "coordinator",
		SessionConfig: session.Config{
			AutoSave:    false,
			SessionFile: "",
		},
	}, nil
}

func (o *Orchestrator) addMainTools(a agent.Agent) {
	reg := o.config.ToolRegistry
	if reg == nil {
		return
	}
	if inserter, ok := a.(toolInserter); ok {
		inserter.RegisterTools(reg)
	} else {
		schemas := reg.ToOpenAISchema()
		if len(schemas) > 0 {
			a.SetTools(schemas)
		}
	}
}

func (o *Orchestrator) addReadOnlyTools(a agent.Agent) {
	roReg := tools.NewRegistry()
	roReg.Register(&tools.FileReadTool{})
	roReg.Register(&tools.TimeGetTool{})
	roReg.Register(&tools.DirListTool{})
	roReg.Register(&tools.WebFetchTool{})
	roReg.Register(&tools.WebSearchTool{})
	roReg.Register(&tools.GlobTool{})
	roReg.Register(&tools.GrepTool{})
	roReg.Register(&tools.CalcTool{})
	roReg.Register(&tools.ShellExecuteTool{})
	roReg.Register(tools.GlobalTodo)
	if inserter, ok := a.(toolInserter); ok {
		inserter.ReplaceTools(roReg)
	} else {
		schemas := roReg.ToOpenAISchema()
		if len(schemas) > 0 {
			a.SetTools(schemas)
		}
	}
}

type toolInserter interface {
	RegisterTools(registry *tools.Registry)
	ReplaceTools(registry *tools.Registry)
}

// makeSubAgentTool создаёт SubAgentTool для текущего агента с привязкой к его
// сессии (sessionID/chain) — так вложенные вызовы продолжают цепочку от корня.
func (o *Orchestrator) makeSubAgentTool(name string, a agent.Agent, peerID int64, sessionID string, chain []string) (*SubAgentTool, error) {
	cfg, err := o.makeAgentConfig()
	if err != nil {
		return nil, err
	}
	return &SubAgentTool{
		AgentConfig:     cfg,
		ContextResolver: o.config.ContextResolver,
		MainTools:       o.config.ToolRegistry,
		SystemPromptDir: o.systemPromptDir(),
		AgentManager:    o.config.AgentManager,
		CurrentDepth:    0,
		MaxDepth:        4,
		PeerID:          peerID,
		ThinkingPeerID:  o.thoughtPeer,
		VKClient:        o.config.VKClient,
		Log:             o.config.Logger,
		Debug:           o.config.Debug,
		ModelHolder:     o.config.ModelHolder,
		SetActiveAgent:  func(n string) { o.setActiveAgent(n) },
		Store:           o.config.Store,
		ParentSessionID: sessionID,
		AgentSessionID:  sessionID, // placeholder; createAgent перегенерирует в собственный UUID
		ParentAgent:     a,
		Chain:           chain,
		SlotManager:     o.config.SlotManager,
		Slots:           o.config.Slots,
	}, nil
}

func (o *Orchestrator) registerSubAgentTool(name string, a agent.Agent, peerID int64, sessionID string, chain []string) error {
	if o.isLeafAgent(name) {
		return nil
	}
	subAgentTool, err := o.makeSubAgentTool(name, a, peerID, sessionID, chain)
	if err != nil {
		return err
	}
	subReg := tools.NewRegistry()
	subReg.Register(subAgentTool)
	if inserter, ok := a.(toolInserter); ok {
		inserter.RegisterTools(subReg)
	} else {
		schemas := subReg.ToOpenAISchema()
		if len(schemas) > 0 {
			a.SetTools(schemas)
		}
	}
	return nil
}

func (o *Orchestrator) registerReviewTool(a agent.Agent) {
	reviewReg := tools.NewRegistry()
	reviewReg.Register(&tools.ReviewApproveTool{})
	if inserter, ok := a.(toolInserter); ok {
		inserter.RegisterTools(reviewReg)
	} else {
		schemas := reviewReg.ToOpenAISchema()
		if len(schemas) > 0 {
			a.SetTools(schemas)
		}
	}
}

func (o *Orchestrator) makeThinkingCallback(agentName string) func(peerID int64, content string) error {
	return func(peerID int64, content string) error {
		if o.config.VKClient == nil || o.thoughtPeer <= 0 {
			return nil
		}
		prefixed := "[" + agentName + "] " + content
		_, err := o.config.VKClient.SendThinking(o.thoughtPeer, prefixed)
		return err
	}
}

func (o *Orchestrator) debugLog(format string, args ...interface{}) {
	if !o.config.Debug {
		return
	}
	if o.config.Logger != nil {
		o.config.Logger.DebugLogf("[AGENT] "+format, args...)
	}
}
