package vk

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agentloop"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/store"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

type mockAgentLoop struct {
	lastMessage      string
	lastPeerID       int64
	lastSystemPrompt string
	sessions         map[int64]*session.Session
	mu               sync.Mutex
	returnErr        error

	blockCh chan struct{}

	cancelled bool

	slowDelay time.Duration
}

func newMockAgentLoop() *mockAgentLoop {
	return &mockAgentLoop{
		sessions: make(map[int64]*session.Session),
	}
}

func (m *mockAgentLoop) ProcessPrompt(ctx context.Context, prompt string, peerID int64) (string, error) {
	m.lastMessage = prompt
	m.lastPeerID = peerID

	sess := m.getOrCreateSession(peerID)
	sess.AddUserMessage(prompt)
	sess.AddAssistantMessage("processed: " + prompt)

	return "processed: " + prompt, nil
}

func (m *mockAgentLoop) ProcessMessage(ctx context.Context, prompt string, peerID int64) (string, error) {
	if m.returnErr != nil {
		return "", m.returnErr
	}

	if m.blockCh != nil {
		select {
		case <-ctx.Done():
			m.cancelled = true
			return "", ctx.Err()
		case <-m.blockCh:

		}
	}
	if m.slowDelay > 0 {
		timer := time.NewTimer(m.slowDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			m.cancelled = true
			return "", ctx.Err()
		case <-timer.C:

		}
	}
	return m.ProcessPrompt(ctx, prompt, peerID)
}

func (m *mockAgentLoop) ProcessPromptWithSystemPrompt(ctx context.Context, prompt string, peerID int64, systemPrompt string) (string, error) {
	m.lastSystemPrompt = systemPrompt
	return m.ProcessPrompt(ctx, prompt, peerID)
}

func (m *mockAgentLoop) Start(ctx context.Context) {}
func (m *mockAgentLoop) Stop()                     {}
func (m *mockAgentLoop) ResetSession(peerID int64) {
	m.mu.Lock()
	delete(m.sessions, peerID)
	m.mu.Unlock()
}
func (m *mockAgentLoop) ClearPeerSession(peerID int64) {
	m.ResetSession(peerID)
}
func (m *mockAgentLoop) GetSessionConfig(peerID int64) (session.Config, bool) {
	return session.DefaultConfig(), false
}
func (m *mockAgentLoop) SetThinkingCallback(cb func(peerID int64, content string) error) {}

func (m *mockAgentLoop) GetSession(peerID int64) *session.Session {
	return m.getOrCreateSession(peerID)
}

func (m *mockAgentLoop) EnsureSession(peerID int64) *session.Session {
	return m.getOrCreateSession(peerID)
}

func (m *mockAgentLoop) ResumeInterruptedTask(ctx context.Context, peerID int64) {}

func (m *mockAgentLoop) ClearAllSlots(ctx context.Context) {}

func (m *mockAgentLoop) GetContextStats(peerID int64) (int, int, error) {
	m.mu.Lock()
	sess := m.sessions[peerID]
	m.mu.Unlock()
	if sess == nil {
		return 0, 0, nil
	}

	history := sess.GetHistory()
	charCount := 0
	for _, msg := range history {
		charCount += len(msg.Content)
	}

	tokenCount := charCount / 4

	return charCount, tokenCount, nil
}

func (m *mockAgentLoop) TestLlamaServer(ctx context.Context) (string, time.Duration, float64, error) {
	return "mock-model", 10 * time.Millisecond, 100.0, nil
}

func (m *mockAgentLoop) GetModelHolder() *modelsconfig.Holder {
	return nil
}

func (m *mockAgentLoop) GetSlotManager() *agentloop.SlotManager { return nil }
func (m *mockAgentLoop) GetSlots() *agentloop.SlotClient        { return nil }
func (m *mockAgentLoop) GetStore() store.Store                  { return nil }

func (m *mockAgentLoop) getOrCreateSession(peerID int64) *session.Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[peerID]; ok {
		return sess
	}
	config := session.DefaultConfig()
	config.PeerID = peerID
	sess := session.NewSession(config)
	m.sessions[peerID] = sess
	return sess
}

type mockOrchestrator struct {
	mu            sync.Mutex
	lastTask      string
	lastPeerID    int64
	fixedResponse string
	fixedErr      error
	callCount     int
	clearedPeers  map[int64]bool
	primaryAgents map[string]bool
	systemPrompts map[string]string
	agentNames    []string

	chainPeers     []int64
	chainResumeCtx context.Context
	chainStarted   chan struct{}
}

func newMockOrchestrator(response string) *mockOrchestrator {
	return &mockOrchestrator{fixedResponse: response, clearedPeers: make(map[int64]bool)}
}

func (m *mockOrchestrator) ExecuteTask(_ context.Context, task string, peerID int64) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastTask = task
	m.lastPeerID = peerID
	m.callCount++
	return m.fixedResponse, m.fixedErr
}

func (m *mockOrchestrator) RunAgent(_ context.Context, agentName, task string, peerID int64) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastTask = task
	m.lastPeerID = peerID
	m.callCount++
	return m.fixedResponse, m.fixedErr
}

func (m *mockOrchestrator) ListAgentNames() []string {
	if m.agentNames != nil {
		return m.agentNames
	}
	return []string{"worker", "qa", "explore", "general", "agent", "lead"}
}

func (m *mockOrchestrator) GetCurrentAgent() string {
	return "mock-agent"
}

func (m *mockOrchestrator) ClearActiveSessions(peerID int64) {
	if m.clearedPeers != nil {
		m.clearedPeers[peerID] = true
	}
}

func (m *mockOrchestrator) ClearRegisteredAgents(peerID int64) []string {
	return nil
}

func (m *mockOrchestrator) GetActiveAgentSessions(peerID int64) (string, error) {
	return "", nil
}

func (m *mockOrchestrator) IsPrimary(agentName string) bool {
	return m.primaryAgents != nil && m.primaryAgents[agentName]
}

func (m *mockOrchestrator) GetSystemPrompt(agentName string) (string, error) {
	if m.systemPrompts != nil {
		if p, ok := m.systemPrompts[agentName]; ok {
			return p, nil
		}
	}
	return "system prompt for " + agentName, nil
}

func (m *mockOrchestrator) ActiveChainPeers() []int64 { return m.chainPeers }

func (m *mockOrchestrator) ResumeActiveChainsForPeer(ctx context.Context, peerID int64) error {
	m.mu.Lock()
	m.chainResumeCtx = ctx
	m.mu.Unlock()
	close(m.chainStarted)
	<-ctx.Done()
	return nil
}

func TestCommandsDoNotReachModel(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	tests := []struct {
		name    string
		message string
	}{
		{"clear command", "/clear"},
		{"help command", "/help"},
		{"status command", "/status"},
		{"newsession command", "/newsession /tmp"},
		{"n alias command", "/n /tmp"},
		{"unknown command", "/unknownxyz"},
		{"restart command", "/restart"},
		{"update command", "/update"},
		{"b command", "/b main"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock.lastMessage = ""
			_ = handler.ProcessMessage(tt.message, 12345)
			if mock.lastMessage != "" {
				t.Errorf("Command %q was sent to AI model: lastMessage=%q", tt.message, mock.lastMessage)
			}
		})
	}
}

func TestNormalMessagesReachModel(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	mock.lastMessage = ""
	response := handler.ProcessMessage("Hello, how are you?", 12345)

	if mock.lastMessage == "" {
		t.Error("Normal message was NOT sent to AI model")
	}
	if response != "processed: Hello, how are you?" {
		t.Errorf("Expected 'processed: Hello, how are you?', got %q", response)
	}
}

func TestStopCommandIsSilentlyIgnored(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	response := handler.ProcessMessage("/stop", 12345)

	if mock.lastMessage != "" {
		t.Errorf("/stop was sent to AI model: lastMessage=%q", mock.lastMessage)
	}
	if response != "" {
		t.Errorf("/stop should be silently ignored by agent, got response: %q", response)
	}
}

func TestRestarterCommandsDoNotReachLLM(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	tests := []struct {
		cmd          string
		expectedResp bool
	}{
		{"/restart", true},
		{"/update", true},
		{"/b main", true},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			mock.lastMessage = ""
			response := handler.ProcessMessage(tt.cmd, 12345)
			if mock.lastMessage != "" {
				t.Errorf("Restarter command %q was sent to AI model: lastMessage=%q", tt.cmd, mock.lastMessage)
			}
			if tt.expectedResp && response == "" {
				t.Errorf("Restarter command %q should return confirmation message", tt.cmd)
			}
		})
	}
}

func TestCommandResponseFormats(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	tests := []struct {
		name       string
		message    string
		expectResp bool
	}{
		{"clear", "/clear", true},
		{"help", "/help", true},
		{"status", "/status", true},
		{"unknown", "/blabla", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := handler.ProcessMessage(tt.message, 12345)
			if tt.expectResp && response == "" {
				t.Errorf("Expected non-empty response for %q", tt.message)
			}
			t.Logf("Response for %q: %s", tt.message, response)
		})
	}
}

func TestPinCommand(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	peerID := int64(12345)

	t.Run("pins a prompt", func(t *testing.T) {
		response := handler.ProcessMessage("/pin Always answer in Russian", peerID)
		if response == "" {
			t.Error("expected non-empty response for /pin")
		}
		sess := mock.GetSession(peerID)
		if sess == nil {
			t.Fatal("session should exist")
		}
		pinned := sess.GetPinned()
		if len(pinned) != 1 || pinned[0] != "Always answer in Russian" {
			t.Errorf("expected 1 pinned prompt, got %v", pinned)
		}
	})

	t.Run("pins multiple prompts", func(t *testing.T) {
		handler.ProcessMessage("/pin Use tabs for indentation", peerID)
		sess := mock.GetSession(peerID)
		pinned := sess.GetPinned()
		if len(pinned) != 2 {
			t.Errorf("expected 2 pinned prompts, got %d", len(pinned))
		}
	})

	t.Run("sends pinned prompt to model", func(t *testing.T) {
		mock.lastMessage = ""
		handler.ProcessMessage("/pin Some prompt", peerID)
		if mock.lastMessage != "Some prompt" {
			t.Errorf("expected pinned prompt to reach model, got %q", mock.lastMessage)
		}
	})

	t.Run("lists pinned prompts", func(t *testing.T) {
		response := handler.ProcessMessage("/pin", peerID)
		if !strings.Contains(response, "Always answer in Russian") {
			t.Errorf("expected list to contain pinned prompt, got: %s", response)
		}
		if !strings.Contains(response, "Use tabs for indentation") {
			t.Errorf("expected list to contain second pinned prompt, got: %s", response)
		}
	})

	t.Run("clears pinned prompts", func(t *testing.T) {
		response := handler.ProcessMessage("/pin clear", peerID)
		if response == "" {
			t.Error("expected non-empty response for /pin clear")
		}
		sess := mock.GetSession(peerID)
		if len(sess.GetPinned()) != 0 {
			t.Errorf("expected 0 pinned prompts after clear, got %v", sess.GetPinned())
		}
	})
}

func TestStatusShowsCorrectMessageCount(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	peerID := int64(12345)

	_ = handler.ProcessMessage("Привет, как дела?", peerID)
	_ = handler.ProcessMessage("Расскажи анекдот", peerID)

	status := handler.ProcessMessage("/status", peerID)

	t.Logf("Status output:\n%s", status)

	if strings.Contains(status, "Сообщений: 0") {
		t.Error("BUG: Status shows 0 messages but should show > 0 after processing messages")
	}
}

func TestStatusShowsCorrectTokenCount(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	peerID := int64(12345)

	_ = handler.ProcessMessage("Привет, это тестовое сообщение для проверки подсчёта токенов", peerID)

	status := handler.ProcessMessage("/status", peerID)

	t.Logf("Status output:\n%s", status)

	if strings.Contains(status, "Токенов в контексте: 0") {
		t.Error("BUG: Status shows 0 tokens but should show > 0 after processing messages")
	}
}

func TestUnknownCommandsDoNotCallAI(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	mock.lastMessage = ""
	response := handler.ProcessMessage("/unknowncommand", 12345)

	if mock.lastMessage != "" {
		t.Error("Unknown command should NOT send message to AI model")
	}

	if !strings.Contains(response, "Неизвестная команда") {
		t.Errorf("Unknown command should return error message, got: %q", response)
	}
}

func TestStatusShowsCorrectCharCount(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	peerID := int64(12345)

	_ = handler.ProcessMessage("Тестовое сообщение", peerID)

	status := handler.ProcessMessage("/status", peerID)

	t.Logf("Status output:\n%s", status)

	if strings.Contains(status, "Символов в контексте: 0") {
		t.Error("BUG: Status shows 0 chars but should show > 0 after processing messages")
	}
}

func TestPrimaryAgentSharesMainContext(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	mockOrch := newMockOrchestrator("ephemeral")
	mockOrch.primaryAgents = map[string]bool{"lead": true}
	mockOrch.systemPrompts = map[string]string{"lead": "You are a Lead Agent."}
	handler := NewBotHandlerWithPeerID(nil, mock, log, 0, 0, mockOrch, nil)
	handler.SetTargetQueue(agentloop.NewTargetQueue(
		func(ctx context.Context, name, prompt string, peerID int64) (string, error) {
			return "queued-resp", nil
		},
		func(name string, peerID int64, response string, err error) {},
	))

	response := handler.ProcessMessage("#lead создай проект", 12345)
	if !strings.Contains(response, "processed: ") {
		t.Fatalf("expected main-agent response, got: %s", response)
	}
	if mock.lastSystemPrompt != "You are a Lead Agent." {
		t.Errorf("expected lead system prompt to be passed, got: %q", mock.lastSystemPrompt)
	}

	sess := mock.GetSession(12345)
	if sess == nil {
		t.Fatal("expected shared session")
	}
	var hasLeadTask bool
	for _, msg := range sess.GetHistory() {
		if msg.Role == session.UserRole && strings.Contains(msg.Content, "создай проект") {
			hasLeadTask = true
		}
	}
	if !hasLeadTask {
		t.Error("expected #lead task in shared session history")
	}

	mock.lastMessage = ""
	handler.ProcessMessage("расскажи про проект", 12345)
	if !strings.Contains(mock.lastMessage, "расскажи про проект") {
		t.Errorf("expected plain follow-up to reach main agent, got lastMessage=%q", mock.lastMessage)
	}
}

func TestNonPrimaryAgentRoutedThroughQueue(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	mockOrch := newMockOrchestrator("ephemeral result")
	mockOrch.agentNames = []string{"worker", "lead"}
	mockOrch.primaryAgents = map[string]bool{"lead": true}
	handler := NewBotHandlerWithPeerID(nil, mock, log, 0, 0, mockOrch, nil)

	ran := make(chan string, 1)
	deliveredResp := make(chan string, 1)
	handler.SetTargetQueue(agentloop.NewTargetQueue(
		func(ctx context.Context, name, prompt string, peerID int64) (string, error) {
			select {
			case ran <- name + "|" + prompt:
			default:
			}
			return "queued-result", nil
		},
		func(name string, peerID int64, resp string, err error) {
			select {
			case deliveredResp <- resp:
			default:
			}
		},
	))

	reply := handler.ProcessMessage("#worker сделай задачу", 12345)
	if !strings.HasPrefix(reply, "▶️") {
		t.Fatalf("expected queued ack, got %q", reply)
	}

	select {
	case routed := <-ran:
		if routed != "worker|сделай задачу" {
			t.Errorf("unexpected route %q", routed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queue runner never invoked")
	}

	select {
	case resp := <-deliveredResp:
		if resp != "queued-result" {
			t.Errorf("delivery resp = %q", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queue delivery never fired")
	}

	if mock.lastMessage != "" {
		t.Errorf("expected non-primary agent NOT to reach main agent, got lastMessage=%q", mock.lastMessage)
	}
}

func TestParseAgentHashMention(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
		wantTask string
	}{
		{
			name:     "empty string",
			input:    "",
			wantName: "",
			wantTask: "",
		},
		{
			name:     "no hash prefix",
			input:    "just some text",
			wantName: "",
			wantTask: "just some text",
		},
		{
			name:     "#worker with task",
			input:    "#worker создай функцию",
			wantName: "worker",
			wantTask: "создай функцию",
		},
		{
			name:     "#coordinator only name no space",
			input:    "#coordinator",
			wantName: "coordinator",
			wantTask: "",
		},
		{
			name:     "#qa with long task",
			input:    "#qa протестируй модуль авторизации",
			wantName: "qa",
			wantTask: "протестируй модуль авторизации",
		},
		{
			name:     "#explore case insensitive",
			input:    "#EXPLORE найди баги",
			wantName: "explore",
			wantTask: "найди баги",
		},
		{
			name:     "#general with extra spaces",
			input:    "#general   привет",
			wantName: "general",
			wantTask: "привет",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotTask := ParseAgentHashMention(tt.input, []string{"worker", "qa", "explore", "general", "agent", "coordinator"})
			if gotName != tt.wantName {
				t.Errorf("ParseAgentHashMention() name = %q, want %q", gotName, tt.wantName)
			}
			if gotTask != tt.wantTask {
				t.Errorf("ParseAgentHashMention() task = %q, want %q", gotTask, tt.wantTask)
			}
		})
	}
}

func TestClearCancelsPendingQuestionAndGrants(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()

	orch := &mockOrchestrator{clearedPeers: make(map[int64]bool)}
	handler := NewBotHandlerWithPeerID(nil, mock, log, 0, 0, orch, nil)

	peerID := int64(987)

	ch := tools.RegisterPendingQuestion(peerID)
	tools.ApplyPathGrant(peerID, "/some/path/")

	if !tools.HasPendingQuestion(peerID) {
		t.Fatal("expected pending question to be registered")
	}
	if !tools.IsPathGranted(peerID, "/some/path/file.txt") {
		t.Fatal("expected path grant to be applied")
	}

	waitDone := make(chan struct{})
	go func() {
		<-ch
		close(waitDone)
	}()

	handler.ProcessMessage("/clear", peerID)

	if tools.HasPendingQuestion(peerID) {
		t.Error("expected pending question cleared after /clear")
	}
	if tools.IsPathGranted(peerID, "/some/path/file.txt") {
		t.Error("expected path grants cleared after /clear")
	}
	if !orch.clearedPeers[peerID] {
		t.Error("expected ClearActiveSessions called for peer after /clear")
	}

	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Error("expected pending question wait to be unblocked after /clear")
	}
}

func TestProcessMessageResolvesPendingQuestionWithoutMutex(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	peerID := int64(555)

	mu := handler.getPeerMutex(peerID)
	mu.Lock()
	defer mu.Unlock()

	ch := tools.RegisterPendingQuestion(peerID)
	defer tools.UnregisterPendingQuestion(peerID)

	resultCh := make(chan string, 1)
	go func() {
		resultCh <- handler.ProcessMessage("✅ Allow", peerID)
	}()

	select {
	case result := <-resultCh:
		if result != "" {
			t.Errorf("expected empty response for resolved question, got %q", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ProcessMessage deadlocked on peer mutex while answering pending question")
	}

	select {
	case answer := <-ch:
		if answer["answer"] != "✅ Allow" {
			t.Errorf("expected answer '✅ Allow', got %v", answer)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected question answer to be delivered")
	}
}

func TestClearCancelsRunningAgent(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()

	orch := &mockOrchestrator{clearedPeers: make(map[int64]bool)}
	handler := NewBotHandlerWithPeerID(nil, mock, log, 0, 0, orch, nil)

	peerID := int64(456)

	sess := mock.GetSession(peerID)
	if sess == nil {
		t.Fatal("expected session after /clear")
	}

	handler.ProcessMessage("/clear", peerID)

	if !orch.clearedPeers[peerID] {
		t.Error("expected ClearActiveSessions called for peer 456")
	}

	sessAfter := mock.GetSession(peerID)
	if sessAfter == nil {
		t.Fatal("session should exist after /clear (recreated)")
	}
	if sessAfter.HistoryLength() != 0 {
		t.Errorf("expected history cleared, got %d messages", sessAfter.HistoryLength())
	}
}

func TestClearKeepsWorkingDir(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	handler.ProcessMessage("/n /tmp", 12345)
	sess := mock.GetSession(12345)
	if sess == nil {
		t.Fatal("Expected session to exist")
	}
	if sess.GetWorkingDir() == "" {
		t.Fatal("Expected working dir to be set")
	}
	expectedWD := sess.GetWorkingDir()

	handler.ProcessMessage("/clear", 12345)
	sess = mock.GetSession(12345)
	if sess == nil {
		t.Fatal("Expected session to exist after /clear")
	}
	if got := sess.GetWorkingDir(); got != expectedWD {
		t.Errorf("Expected working dir %q to be preserved, got %q", expectedWD, got)
	}
	if got := sess.GetPinned(); len(got) != 0 {
		t.Errorf("Expected pinned prompts cleared after /clear, got %v", got)
	}
}

func TestHandleNewSession(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	tests := []struct {
		name            string
		message         string
		checkWorkingDir func(t *testing.T, dir string)
	}{
		{
			name:    "/n with /tmp should set working directory",
			message: "/n /tmp",
			checkWorkingDir: func(t *testing.T, dir string) {
				if dir != "/tmp" && dir != filepath.Join(os.Getenv("TMPDIR"), "tmp") {
					t.Errorf("Expected /tmp or temp dir, got %q", dir)
				}
			},
		},
		{
			name:    "/newsession with /tmp should set working directory",
			message: "/newsession /tmp",
			checkWorkingDir: func(t *testing.T, dir string) {
				if dir != "/tmp" && dir != filepath.Join(os.Getenv("TMPDIR"), "tmp") {
					t.Errorf("Expected /tmp or temp dir, got %q", dir)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler.ProcessMessage(tt.message, 12345)
			sess := mock.GetSession(12345)
			if sess == nil {
				t.Fatal("Expected session to exist")
			}
			wd := sess.GetWorkingDir()
			tt.checkWorkingDir(t, wd)
		})
	}
}

func TestHandleNewSessionWithTilde(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Cannot get home directory")
	}

	message := "/n ~"
	handler.ProcessMessage(message, 12345)
	sess := mock.GetSession(12345)
	if sess == nil {
		t.Fatal("Expected session to exist")
	}
	wd := sess.GetWorkingDir()
	if wd != home {
		t.Errorf("Expected home dir %q, got %q", home, wd)
	}
}

func TestHandleNewSessionWithNonexistentPath(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	response := handler.ProcessMessage("/n /nonexistent/path/12345", 12345)
	if !strings.Contains(response, "не существует") && !strings.Contains(response, "Ошибка") {
		t.Errorf("Expected error about nonexistent path, got %q", response)
	}
}

func TestContextCanceledErrorSuppressed(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	mock.returnErr = context.Canceled
	handler := NewBotHandler(nil, mock, log)

	response := handler.ProcessMessage("Hello, cancel this", 12345)
	if response != "" {
		t.Errorf("Expected empty response for context.Canceled, got %q", response)
	}
}

func TestWrappedContextCanceledErrorSuppressed(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	mock.returnErr = fmt.Errorf("process tool results: %w", context.Canceled)
	handler := NewBotHandler(nil, mock, log)

	response := handler.ProcessMessage("Hello, cancel this", 12345)
	if response != "" {
		t.Errorf("Expected empty response for wrapped context.Canceled, got %q", response)
	}
}

func TestOtherAgentErrorStillShown(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	mock.returnErr = fmt.Errorf("server down")
	handler := NewBotHandler(nil, mock, log)

	response := handler.ProcessMessage("Hello", 12345)
	if !strings.Contains(response, "Ошибка") {
		t.Errorf("Expected error message, got %q", response)
	}
}
