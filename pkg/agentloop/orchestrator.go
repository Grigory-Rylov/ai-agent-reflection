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

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agent"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agentpolicy"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/store"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/util/stringutil"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

type OrchestratorConfig struct {
	ModelHolder     *modelsconfig.Holder
	ContextResolver *ModelContextResolver
	MaxTokens       int
	ModelLimitInput int
	Temperature     float64
	MaxToolCallDepth int
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


type agentCtxEntry struct {
	cancel    context.CancelFunc
	sessionID string
	peerID    int64
}

type Orchestrator struct {
	config      OrchestratorConfig
	thoughtPeer int64
	activeAgent string
	activeMu    sync.RWMutex
	
	activeAgents   map[string]*agentCtxEntry 
	activeAgentsMu sync.Mutex
}

func NewOrchestrator(cfg OrchestratorConfig) *Orchestrator {
	return &Orchestrator{
		config:         cfg,
		thoughtPeer:    cfg.ThinkingPeerID,
		activeAgents:   make(map[string]*agentCtxEntry),
	}
}


func (o *Orchestrator) registerAgentContext(sessionID string, peerID int64, cancel context.CancelFunc) {
	o.activeAgentsMu.Lock()
	defer o.activeAgentsMu.Unlock()
	o.activeAgents[sessionID] = &agentCtxEntry{cancel: cancel, sessionID: sessionID, peerID: peerID}
	if o.config.Logger != nil {
		o.config.Logger.DebugLogf("[AGENT] registered context for %s (peer %d)", sessionID, peerID)
	}
}


func (o *Orchestrator) unregisterAgentContext(sessionID string) {
	o.activeAgentsMu.Lock()
	defer o.activeAgentsMu.Unlock()
	delete(o.activeAgents, sessionID)
}


func (o *Orchestrator) unregisterAndReleaseOnCancel(sessionID string) {
	o.unregisterAgentContext(sessionID)
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
	o.debugLog("Mode activated. Task: %s", stringutil.Truncate(task, 200, "..."))
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


func (o *Orchestrator) prepareAgentPrompt(agentName string) (string, error) {
	prompt, err := o.loadSystemPrompt(agentName)
	if err != nil {
		return "", fmt.Errorf("failed to load prompt for agent %q: %w", agentName, err)
	}
	if o.config.MaxReviewIterations > 0 {
		prompt += fmt.Sprintf("\n\nMaximum review iterations: %d. After this many developer↔reviewer cycles, move forward regardless.", o.config.MaxReviewIterations)
	}
	return prompt, nil
}


func (o *Orchestrator) cleanupAgentBG(sessionID string) {
	if sessionID == "" {
		return
	}
	if hub := tools.GetBackgroundHub(); hub != nil {
		hub.ReleasePending(sessionID)
		hub.UnregisterDelivery(sessionID)
	}
}

func (o *Orchestrator) handleAgentFailure(cancel context.CancelFunc, a agent.Agent, rootID, sessionID string, peerID int64, task string) {
	cancel()
	o.cleanupAgentBG(sessionID)
	if rootID != "" {
		o.saveAgentHistory(a, rootID, peerID, task)
	}
	o.releaseAgentSlot(sessionID)
}


func (o *Orchestrator) finishAgentSession(cancel context.CancelFunc, rootID, sessionID string, peerID int64) {
	cancel()
	o.cleanupAgentBG(sessionID)
	if rootID != "" {
		o.endRootSession(peerID, rootID)
	} else {
		o.releaseAgentSlot(sessionID)
	}
}

func (o *Orchestrator) RunAgent(ctx context.Context, agentName, task string, peerID int64) (string, error) {
	o.debugLog("RunAgent: %s. Task: %s", agentName, stringutil.Truncate(task, 200, "..."))
	o.setActiveAgent(agentName)
	defer o.setActiveAgent("")

	prompt, err := o.prepareAgentPrompt(agentName)
	if err != nil {
		return "", err
	}

	a, sessionID, err := o.makeSubAgent(agentName, prompt, peerID)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithCancel(ctx)
	o.registerAgentContext(sessionID, peerID, cancel)
	defer o.unregisterAndReleaseOnCancel(sessionID)

	rootID := o.beginRootSession(agentName, prompt, task, peerID, sessionID)
	var rootChain []string
	if rootID != "" {
		rootChain = []string{rootID}
	}

	if err := o.setupAgentTools(agentName, a, peerID, rootID, rootChain); err != nil {
		cancel()
		o.releaseAgentSlot(sessionID)
		return "", fmt.Errorf("setup tools for agent %q: %w", agentName, err)
	}

	response, err := a.ProcessMessage(ctx, task, peerID)
	if err != nil {
		o.handleAgentFailure(cancel, a, rootID, sessionID, peerID, task)
		return "", fmt.Errorf("agent %q failed: %w", agentName, err)
	}

	o.finishAgentSession(cancel, rootID, sessionID, peerID)
	return response, nil
}


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


func (o *Orchestrator) releaseAgentSlot(sessionID string) {
	ReleaseSessionSlot(o.config.SlotManager, o.config.Slots, o.config.ModelHolder, sessionID, o.config.Logger)
}


func (o *Orchestrator) endRootSession(peerID int64, rootID string) {
	o.releaseAgentSlot(rootID)
	if o.config.Store == nil || rootID == "" {
		return
	}
	o.config.Store.DeleteAgentSession(rootID)
	o.config.Store.SaveAgentChain(peerID, nil)
}


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
		if chainData != nil {
			for i, id := range chainData.Chain {
				if id == s.ID {
					arrow = strings.Repeat("  ", i) + "→ "
					break
				}
			}
		}
		b.WriteString(fmt.Sprintf("%s[%s] %s (id: %s)\n", arrow, s.Status, s.AgentName, s.ID))
	}

	return b.String(), nil
}

func (o *Orchestrator) ClearActiveSessions(peerID int64) {
	
	
	cancelled := o.ClearRegisteredAgents(peerID)

	
	for _, id := range cancelled {
		o.releaseAgentSlot(id)
		if o.config.Logger != nil {
			o.config.Logger.InfoLogf("[AGENT] ClearActiveSessions: cancelled and released slot for %s", id)
		}
	}

	if o.config.Store == nil {
		return
	}
	o.config.Store.ClearAgentChain(peerID)
}


func (o *Orchestrator) ClearRegisteredAgents(peerID int64) []string {
	var cancelled []string
	o.activeAgentsMu.Lock()
	for id, entry := range o.activeAgents {
		if entry.peerID == peerID {
			entry.cancel()
			cancelled = append(cancelled, id)
			delete(o.activeAgents, id)
		}
	}
	o.activeAgentsMu.Unlock()
	return cancelled
}


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

func (o *Orchestrator) ActiveChainPeers() []int64 {
	if o.config.Store == nil {
		return nil
	}
	chains, err := o.config.Store.GetAllActiveChains()
	if err != nil {
		o.debugLog("ActiveChainPeers: %v", err)
		return nil
	}
	var peers []int64
	for _, chain := range chains {
		peers = append(peers, chain.PeerID)
	}
	return peers
}

func (o *Orchestrator) ResumeActiveChainsForPeer(ctx context.Context, peerID int64) error {
	if o.config.Store == nil {
		return nil
	}
	chains, err := o.config.Store.GetAllActiveChains()
	if err != nil {
		return fmt.Errorf("get active chains: %w", err)
	}
	for _, chain := range chains {
		if chain.PeerID != peerID {
			continue
		}
		if err := o.resumeChain(ctx, chain); err != nil {
			o.debugLog("Resume chain for peer %d failed: %v", chain.PeerID, err)
			return err
		}
	}
	return nil
}


func (o *Orchestrator) resumeChain(ctx context.Context, chain store.AgentChainData) error {
	if len(chain.Chain) == 0 {
		return nil
	}
	var childResult string
	for i := len(chain.Chain) - 1; i >= 0; i-- {
		id := chain.Chain[i]
		sd, err := o.config.Store.GetAgentSession(id)
		if err != nil {
			return fmt.Errorf("get session %s: %w", id, err)
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


func (o *Orchestrator) runResumedAgent(ctx context.Context, sd *store.AgentSessionData, childResult string, chain []string) (string, error) {
	a, sessionID, err := o.makeSubAgent(sd.AgentName, sd.SystemPrompt, sd.PeerID)
	if err != nil {
		return "", err
	}
	
	
	if err := o.setupAgentTools(sd.AgentName, a, sd.PeerID, sd.ID, chain); err != nil {
		o.cleanupAgentBG(sessionID)
		o.releaseAgentSlot(sessionID)
		return "", fmt.Errorf("setup tools for resumed agent %q: %w", sd.AgentName, err)
	}
	s := a.GetSession(sd.PeerID)
	o.restoreSessionMessages(s, sd.Messages)

	prompt := pickResumeContinuationPrompt(childResult, lastRestoredRole(s), sd.LastToolCall, sd.LastPrompt)
	result, err := a.ProcessMessage(ctx, prompt, sd.PeerID)
	o.cleanupAgentBG(sessionID)
	o.releaseAgentSlot(sessionID)
	if err != nil {
		return "", fmt.Errorf("resumed agent %q failed: %w", sd.AgentName, err)
	}
	return result, nil
}


func lastRestoredRole(s *session.Session) session.Role {
	history := s.GetHistory()
	if len(history) == 0 {
		return ""
	}
	return history[len(history)-1].Role
}


func pickResumeContinuationPrompt(childResult string, lastRole session.Role, lastToolCall, lastPrompt string) string {
	const defaultText = "The process was restarted. Continue your task from where you left off."
	switch {
	case childResult != "":
		return fmt.Sprintf("Your sub-agent completed with this result:\n\n%s\n\nContinue your task from where you left off.", childResult)
	case lastRole == session.ToolRole:
		label := lastToolCall
		if label == "" {
			label = "your previous tool calls"
		}
		return fmt.Sprintf("Your previous tool calls finished executing; review their results above and continue. Last batch: %s.", label)
	case lastRole == "":
		return defaultText
	case lastPrompt != "":
		return fmt.Sprintf("Continue your task: %s", lastPrompt)
	default:
		return defaultText
	}
}


func (o *Orchestrator) restoreSessionMessages(s *session.Session, messagesJSON string) {
	if s == nil || messagesJSON == "" {
		return
	}
	var msgs []session.Message
	if err := json.Unmarshal([]byte(messagesJSON), &msgs); err != nil {
		o.debugLog("Resume: failed to parse saved messages: %v", err)
		return
	}
	
	
	s.RestoreMessages(sanitizeRestoredMessages(msgs))
}


func sanitizeRestoredMessages(msgs []session.Message) []session.Message {
	out := make([]session.Message, len(msgs))
	copy(out, msgs)
	for {
		n := len(out)
		if n == 0 {
			break
		}
		last := out[n-1]
		if !(last.Role == session.AssistantRole && len(last.ToolCalls) > 0) {
			break
		}
		out = out[:n-1]
	}
	return out
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


func (o *Orchestrator) IsPrimary(name string) bool {
	if o.config.AgentManager != nil {
		info, err := o.config.AgentManager.GetAgent(name)
		if err != nil {
			o.debugLog("IsPrimary(%q): GetAgent error: %v", name, err)
			return false
		}
		return info.Mode == agentpolicy.ModePrimary || info.Mode == agentpolicy.ModeAll
	}
	return false
}


func (o *Orchestrator) GetSystemPrompt(name string) (string, error) {
	return o.loadSystemPrompt(name)
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
	
	if err := o.registerSubAgentTool("worker", a, peerID, sessionID, []string{sessionID}); err != nil {
		o.cleanupAgentBG(sessionID)
		o.releaseAgentSlot(sessionID)
		return "", err
	}
	o.beginLeafSession("worker", prompt, task, peerID, sessionID)
	result, err := a.ProcessMessage(ctx, task, peerID)
	o.cleanupAgentBG(sessionID)
	if err != nil {
		o.saveAgentHistory(a, sessionID, peerID, task)
		o.releaseAgentSlot(sessionID)
		return "", err
	}
	
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
		o.cleanupAgentBG(sessionID)
		o.releaseAgentSlot(sessionID)
		return "", err
	}
	o.beginLeafSession("qa", prompt, task, peerID, sessionID)
	result, err := a.ProcessMessage(ctx, task, peerID)
	o.cleanupAgentBG(sessionID)
	if err != nil {
		o.saveAgentHistory(a, sessionID, peerID, task)
		o.releaseAgentSlot(sessionID)
		return "", err
	}
	
	o.endLeafSession(peerID, sessionID)
	o.releaseAgentSlot(sessionID)
	return result, err
}


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

	sessionID := newSessionUUID(name, strconv.FormatInt(peerID, 10))
	o.configureAgentBase(&cfg, name, sessionID)
	o.assignAgentSlot(&cfg, name, sessionID)

	a := agent.NewAgent(cfg)
	o.setupAgentPermissions(a, name)
	a.SetThinkingCallback(o.makeThinkingCallback(name))
	a.GetSession(peerID).UpdateSystemPrompt(systemPrompt)
	o.attachCheckpointSaving(a, sessionID, peerID)
	o.registerAgentBGDelivery(name, a, peerID, sessionID)
	return a, sessionID, nil
}


func (o *Orchestrator) attachCheckpointSaving(a agent.Agent, sessionID string, peerID int64) {
	if o.config.Store == nil || sessionID == "" {
		return
	}
	if cs, ok := a.(agent.CheckpointSetter); ok {
		cs.SetCheckpoint(func(lastToolCall string) { o.persistCheckpoint(a, sessionID, peerID, lastToolCall) })
	}
}

func (o *Orchestrator) registerAgentBGDelivery(name string, a agent.Agent, peerID int64, sessionID string) {
	hub := tools.GetBackgroundHub()
	if hub == nil || sessionID == "" {
		return
	}
	hub.SetDelivery(sessionID, o.makeAgentBGDelivery(name, a, peerID))
}

func (o *Orchestrator) makeAgentBGDelivery(name string, a agent.Agent, peerID int64) func(peerID int64, text string) {
	return func(p int64, text string) {
		if p <= 0 {
			p = peerID
		}
		if sess := a.GetSession(p); sess != nil {
			if in := sess.GetPeerInput(); in != nil {
				in.Admit(text)
			}
		}
		if o.config.VKClient != nil && o.thoughtPeer > 0 {
			if _, err := o.config.VKClient.SendThinking(o.thoughtPeer, "["+name+"] "+text); err != nil && o.config.Logger != nil {
				o.config.Logger.DebugLogf("[BG] thinking delivery for agent %s failed: %v", name, err)
			}
		}
	}
}


func (o *Orchestrator) persistCheckpoint(a agent.Agent, sessionID string, peerID int64, lastToolCall string) {
	data, err := json.Marshal(a.GetSession(peerID).GetHistory())
	if err != nil {
		o.debugLog("checkpoint marshal for %s failed: %v", sessionID, err)
		return
	}
	if err := o.config.Store.SaveAgentCheckpoint(sessionID, lastToolCall, string(data)); err != nil {
		o.debugLog("checkpoint save for %s failed: %v", sessionID, err)
	}
}


func (o *Orchestrator) configureAgentBase(cfg *agent.Config, name string, sessionID string) {
	cfg.SystemPromptFile = ""
	
	
	cfg.SessionConfig = session.Config{
		SessionFile: "",
		SessionID:   sessionID,
	}
	cfg.EnableLoopAlert = false
	cfg.EnableCompression = true
	cfg.AgentName = name
	cfg.SlotID = -1
	cfg.SlotSave = false
	cfg.BGOwner = sessionID
}


func (o *Orchestrator) assignAgentSlot(cfg *agent.Config, name string, sessionID string) {
	if o.config.ModelHolder == nil || !o.config.ModelHolder.GetCurrentSlotSave() {
		return
	}
	cfg.SlotSave = true
	
	
	
	
	
	slotID := AssignSessionSlot(o.config.SlotManager, o.config.Slots, o.config.ModelHolder, sessionID, o.config.Logger)
	if slotID >= 0 {
		cfg.SlotID = slotID
		cfg.SlotSaver = NewSlotSaver(o.config.SlotManager, o.config.Slots, o.config.ModelHolder, sessionID, o.config.Logger)
		if o.config.Logger != nil {
			o.config.Logger.InfoLogf("[SLOT] assigned slot %d to agent %s (session %s)", slotID, name, sessionID)
		}
	}
}


func (o *Orchestrator) setupAgentPermissions(a agent.Agent, name string) {
	if o.config.AgentManager == nil {
		return
	}
	info, err := o.config.AgentManager.GetAgent(name)
	if err != nil || info.Permission == nil || len(info.Permission) == 0 {
		return
	}
	if ps, ok := a.(interface{ SetPermissionChecker(agent.PermissionChecker) }); ok {
		ps.SetPermissionChecker(agentpolicy.NewPermissionAdapter(info.Permission))
	}
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
		
		maxTokens = ctx
	}
	return agent.Config{
		LlamaServerURL:      llamaURL,
		EngineType:          o.config.ModelHolder.GetCurrentEngineType(),
		Model:               modelName,
		MaxTokens:           maxTokens,
		ModelLimitInput:     o.config.ModelLimitInput,
		Temperature:         o.config.Temperature,
		MaxToolCallDepth:    o.config.MaxToolCallDepth,
		SystemPromptFile:    o.systemPromptDir() + "/coordinator.txt",
		EnableTools:         true,
		EnableLoopAlert:     false,
		ToolOutputMaxLines:  o.config.ToolOutputMaxLines,
		ToolOutputMaxBytes:  o.config.ToolOutputMaxBytes,
		Debug:               o.config.Debug,
		AgentName:           "coordinator",
		SessionConfig: session.Config{
			SessionFile: "",
		},
	}, nil
}

func (o *Orchestrator) addMainTools(a agent.Agent) {
	reg := mainToolsWithoutTask(o.config.ToolRegistry)
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
	roReg.Register(&tools.ShellBackgroundTool{})
	roReg.Register(&tools.ShellCheckTool{})
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
		AgentSessionID:  sessionID, 
		ParentAgent:     a,
		Chain:           chain,
		ParentAgentName: name,
		AllowedSubagents: o.config.AgentManager.SubagentTypesFor(name),
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
