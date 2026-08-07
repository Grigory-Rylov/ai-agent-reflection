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

	"github.com/opencode/llama-client/pkg/agentpolicy"
	"github.com/opencode/llama-client/pkg/modelsconfig"
	"github.com/opencode/llama-client/pkg/tools"
)

// slotActionRecord — запись о вызове slot-эндпоинта (save/restore/clear/delete)
// для конкретного слота.
type slotActionRecord struct {
	slot   int
	action string
}

// multiAgentSlotServer поднимает llama-server с 4 слотами и скриптовым LLM,
// реализующим цепочку lead → worker → qa. Фиксирует slot_id из тел запросов
// /v1/chat/completions и все обращения к /slots/{id}?action=...
func multiAgentSlotServer(t *testing.T) (srv *httptest.Server, slotIDs func() map[string]int, actions func() []slotActionRecord) {
	t.Helper()
	var mu sync.Mutex
	agentSlots := map[string]int{}      // agentName → slot_id из первого запроса
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

// pickMultiAgentReply строит ответ LLM в зависимости от агента и того, пришёл ли
// уже результат сабагента (tool-сообщение). Цепочка: lead → worker → qa.
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

// TestRunAgent_MultiAgentDistinctSlots проверяет сценарий lead → worker → qa:
//   - каждый агент пинится к своему слоту (slot_id в теле запроса);
//   - слоты у lead/worker/qa — различные;
//   - все LLM-запросы одного агента идут в один и тот же слот (переиспользование);
//   - после завершения RunAgent все слоты освобождены (SlotManager пуст, файлы удалены).
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
	// Все три слота различны.
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

	// После завершения все слоты освобождены — SlotManager пуст.
	if got := orch.config.SlotManager.GetAssignedSessions(); len(got) != 0 {
		t.Errorf("expected no assigned slots after completion, got %v", got)
	}

	// После завершения: каждый агент сохранил свой KV-cache (save) и освободил
	// слот (erase). 3 агента → ≥3 save и ≥3 erase.
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

// TestMakeSubAgent_UnifiedSessionID проверяет, что sessionID, использованный
// для слота и БД, совпадает с a.GetSession().GetSessionID() — единый идентификатор.
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

	// sessionID (slot/DB key) == session.SessionID
	if got := a.GetSession(1).GetSessionID(); got != sessionID {
		t.Errorf("session.SessionID %q != returned sessionID %q", got, sessionID)
	}

	// Слот выделен и привязан к этому sessionID.
	slot := orch.config.SlotManager.GetSlotID(sessionID)
	if slot < 0 {
		t.Fatal("expected slot assigned to sessionID")
	}

	// Освобождение убирает привязку.
	orch.releaseAgentSlot(sessionID)
	if orch.config.SlotManager.GetSlotID(sessionID) != -1 {
		t.Error("expected slot released after releaseAgentSlot")
	}
}

// TestAssignSessionSlot_LRUEvictionSavesAndClears проверяет: при вытеснении
// кэш вытесненной сессии сохраняется в файл, серверный слот очищается, и новая
// сессия НЕ восстанавливает чужой кэш (стартует cold — restore не вызывается
// для вытесняющего слота).
func TestAssignSessionSlot_LRUEvictionSavesAndClears(t *testing.T) {
	var mu sync.Mutex
	actions := []slotActionRecord{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/slots":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"id":0},{"id":1}]`)) // 2 слота
			return
		case strings.HasPrefix(r.URL.Path, "/slots/") && r.Method == http.MethodPost:
			var slotID int
			fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/slots/"), "%d", &slotID)
			action := r.URL.Query().Get("action")
			mu.Lock()
			actions = append(actions, slotActionRecord{slot: slotID, action: action})
			mu.Unlock()
			if action == "restore" {
				// Имитируем отсутствие файла (первый запуск).
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

	// Заполняем 2 слота.
	s0 := AssignSessionSlot(mgr, client, holder, "session-A", nil)
	s1 := AssignSessionSlot(mgr, client, holder, "session-B", nil)
	if s0 == s1 {
		t.Fatalf("expected distinct slots, got %d/%d", s0, s1)
	}

	// Помечаем session-A как более свежую (LRU — session-B).
	mgr.Touch("session-A")
	time.Sleep(1 * time.Millisecond)

	actions = nil
	s2 := AssignSessionSlot(mgr, client, holder, "session-C", nil)
	if s2 != s1 {
		t.Errorf("expected session-C to take session-B's slot %d, got %d", s1, s2)
	}

	// На вытеснение: save (вытесненной session-B) + erase. restore НЕ должен
	// вызываться для session-C (cold start после eviction).
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

	// session-B вытеснена, session-A и session-C остаются.
	if mgr.GetSlotID("session-B") != -1 {
		t.Error("session-B should be evicted")
	}
}
