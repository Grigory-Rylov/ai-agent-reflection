package agentloop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agentpolicy"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)


type slotActionRecord struct {
	slot   int
	action string
}


func multiAgentSlotServer(t *testing.T) (srv *httptest.Server, slotIDs func() map[string]int, actions func() []slotActionRecord) {
	t.Helper()
	var mu sync.Mutex
	agentSlots := map[string]int{}      
	actionLog := []slotActionRecord{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/slots":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"id":0},{"id":1},{"id":2},{"id":3}]`))
			return
		case strings.HasPrefix(r.URL.Path, "/slots/") && r.Method == http.MethodPost:
			var slotID int
			fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/slots/"), "%d", &slotID)
			action := r.URL.Query().Get("action")
			mu.Lock()
			actionLog = append(actionLog, slotActionRecord{slot: slotID, action: action})
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id_slot":%d,"n_saved":1}`, slotID)
			return
		case r.URL.Path == "/v1/chat/completions" && r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			var req struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
				SlotID int `json:"slot_id"`
			}
			json.Unmarshal(body, &req)

			agent := "unknown"
			sysPrompt := ""
			for _, m := range req.Messages {
				if m.Role == "system" {
					sysPrompt = m.Content
					break
				}
			}
			switch {
			case strings.Contains(sysPrompt, "LEAD_SYS"):
				agent = "lead"
			case strings.Contains(sysPrompt, "WORKER_SYS"):
				agent = "worker"
			case strings.Contains(sysPrompt, "QA_SYS"):
				agent = "qa"
			}

			mu.Lock()
			if _, ok := agentSlots[agent]; !ok && agent != "unknown" {
				agentSlots[agent] = req.SlotID
			}
			mu.Unlock()

			reply := pickMultiAgentReply(agent, req.Messages)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", reply)
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			fmt.Fprint(w, "[DONE]\n")
			return
		}
		http.NotFound(w, r)
	}))

	return server,
		func() map[string]int { mu.Lock(); defer mu.Unlock(); out := make(map[string]int, len(agentSlots)); for k, v := range agentSlots { out[k] = v }; return out },
		func() []slotActionRecord { mu.Lock(); defer mu.Unlock(); out := make([]slotActionRecord, len(actionLog)); copy(out, actionLog); return out }
}


func pickMultiAgentReply(agent string, msgs []struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}) string {
	hasToolResult := false
	for _, m := range msgs {
		if m.Role == "tool" {
			hasToolResult = true
		}
	}
	switch agent {
	case "lead":
		if hasToolResult {
			return "LEAD_DONE"
		}
		return `<function=task><parameter=subagent_type>worker</parameter><parameter=prompt>do work</parameter></function>`
	case "worker":
		if hasToolResult {
			return "WORKER_DONE"
		}
		return `<function=task><parameter=subagent_type>qa</parameter><parameter=prompt>review work</parameter></function>`
	case "qa":
		return "QA_DONE"
	}
	return "OK"
}


func TestRunAgent_MultiAgentDistinctSlots(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"lead.txt", "worker.txt", "qa.txt"} {
		role := strings.TrimSuffix(name, ".txt")
		os.WriteFile(filepath.Join(dir, name), []byte(strings.ToUpper(role)+"_SYS prompt"), 0644)
	}

	server, slotIDs, actions := multiAgentSlotServer(t)
	defer server.Close()

	holder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models:  map[string]modelsconfig.ModelEntry{"test": {Name: "m.gguf", Host: server.URL, SlotSave: true}},
	})

	am := agentpolicy.NewAgentManager()
	am.RegisterAgent(agentpolicy.AgentInfo{Name: "lead", Mode: agentpolicy.ModePrimary, Coordinator: true, Prompt: "LEAD_SYS"})
	am.RegisterAgent(agentpolicy.AgentInfo{Name: "worker", Mode: agentpolicy.ModeSubagent, Prompt: "WORKER_SYS"})
	am.RegisterAgent(agentpolicy.AgentInfo{Name: "qa", Mode: agentpolicy.ModeSubagent, Leaf: true, Prompt: "QA_SYS"})

	reg := tools.NewRegistry()
	reg.Register(&tools.FileReadTool{})

	orch := NewOrchestrator(OrchestratorConfig{
		ModelHolder:     holder,
		MaxTokens:       8192,
		Temperature:     0.3,
		ToolRegistry:    reg,
		AgentManager:    am,
		SystemPromptDir: dir,
		SlotManager:     NewSlotManager(newSlotClient()),
		Slots:           newSlotClient(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := orch.RunAgent(ctx, "lead", "build project", 999)
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if !strings.Contains(resp, "LEAD_DONE") {
		t.Fatalf("unexpected response: %q", resp)
	}

	slots := slotIDs()
	if len(slots) != 3 {
		t.Fatalf("expected 3 agents with slots, got %d: %v", len(slots), slots)
	}
	
	seen := map[int]string{}
	for agent, sid := range slots {
		if other, ok := seen[sid]; ok {
			t.Errorf("slot %d shared by %s and %s", sid, other, agent)
		}
		seen[sid] = agent
		if sid < 0 {
			t.Errorf("agent %s has no slot_id in request body", agent)
		}
	}

	
	if got := orch.config.SlotManager.GetAssignedSessions(); len(got) != 0 {
		t.Errorf("expected no assigned slots after completion, got %v", got)
	}

	
	
	actLog := actions()
	saveCount, eraseCount := 0, 0
	for _, a := range actLog {
		switch a.action {
		case "save":
			saveCount++
		case "erase":
			eraseCount++
		}
	}
	if saveCount < 3 {
		t.Errorf("expected ≥3 slot saves after completion, got %d (actions: %v)", saveCount, actLog)
	}
	if eraseCount < 3 {
		t.Errorf("expected ≥3 slot erases on release, got %d (actions: %v)", eraseCount, actLog)
	}
}


func TestMakeSubAgent_UnifiedSessionID(t *testing.T) {
	server, _, _ := multiAgentSlotServer(t)
	defer server.Close()

	holder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models:  map[string]modelsconfig.ModelEntry{"test": {Name: "m.gguf", Host: server.URL, SlotSave: true}},
	})
	am := agentpolicy.NewAgentManager()
	am.RegisterAgent(agentpolicy.AgentInfo{Name: "lead", Mode: agentpolicy.ModePrimary, Coordinator: true, Prompt: "sys"})

	orch := NewOrchestrator(OrchestratorConfig{
		ModelHolder:  holder,
		MaxTokens:    4096,
		ToolRegistry: tools.NewRegistry(),
		AgentManager: am,
		SlotManager:  NewSlotManager(newSlotClient()),
		Slots:        newSlotClient(),
	})

	a, sessionID, err := orch.makeSubAgent("lead", "sys", 1)
	if err != nil {
		t.Fatalf("makeSubAgent: %v", err)
	}

	
	if got := a.GetSession(1).GetSessionID(); got != sessionID {
		t.Errorf("session.SessionID %q != returned sessionID %q", got, sessionID)
	}

	
	slot := orch.config.SlotManager.GetSlotID(sessionID)
	if slot < 0 {
		t.Fatal("expected slot assigned to sessionID")
	}

	
	orch.releaseAgentSlot(sessionID)
	if orch.config.SlotManager.GetSlotID(sessionID) != -1 {
		t.Error("expected slot released after releaseAgentSlot")
	}
}


func TestAssignSessionSlot_LRUEvictionSavesAndClears(t *testing.T) {
	var mu sync.Mutex
	actions := []slotActionRecord{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/slots":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"id":0},{"id":1}]`)) 
			return
		case strings.HasPrefix(r.URL.Path, "/slots/") && r.Method == http.MethodPost:
			var slotID int
			fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/slots/"), "%d", &slotID)
			action := r.URL.Query().Get("action")
			mu.Lock()
			actions = append(actions, slotActionRecord{slot: slotID, action: action})
			mu.Unlock()
			if action == "restore" {
				
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":{"message":"file not found"}}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	holder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models:  map[string]modelsconfig.ModelEntry{"test": {Name: "m.gguf", Host: server.URL, SlotSave: true}},
	})
	mgr := NewSlotManager(newSlotClient())
	client := newSlotClient()

	
	s0 := AssignSessionSlot(mgr, client, holder, "session-A", nil)
	s1 := AssignSessionSlot(mgr, client, holder, "session-B", nil)
	if s0 == s1 {
		t.Fatalf("expected distinct slots, got %d/%d", s0, s1)
	}

	
	mgr.Touch("session-A")
	time.Sleep(1 * time.Millisecond)

	actions = nil
	s2 := AssignSessionSlot(mgr, client, holder, "session-C", nil)
	if s2 != s1 {
		t.Errorf("expected session-C to take session-B's slot %d, got %d", s1, s2)
	}

	
	
	mu.Lock()
	defer mu.Unlock()
	var saved, erased, restored bool
	for _, a := range actions {
		switch a.action {
		case "save":
			saved = true
		case "erase":
			erased = true
		case "restore":
			restored = true
		}
	}
	if !saved {
		t.Error("expected save of evicted session cache")
	}
	if !erased {
		t.Error("expected erase of server slot after eviction")
	}
	if restored {
		t.Error("restore must NOT be called for the new session after eviction (cold start)")
	}

	
	if mgr.GetSlotID("session-B") != -1 {
		t.Error("session-B should be evicted")
	}
}
