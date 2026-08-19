package session

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/store"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
)


type Role string

const (
	SystemRole    Role = "system"
	UserRole      Role = "user"
	AssistantRole Role = "assistant"
	ToolRole      Role = "tool"
)


type MsgToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type,omitempty"`
	Function MsgToolCallFunc    `json:"function,omitempty"`
}


type MsgToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}


type Message struct {
	Role       Role          `json:"role"`
	Content    string        `json:"content"`
	ToolCalls  []MsgToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"` 
	Name       string        `json:"name,omitempty"`         
	Summary    bool          `json:"summary,omitempty"`      
	
	
	Compacted bool `json:"compacted,omitempty"`
	
	
	
	TailStartID int       `json:"tail_start_id,omitempty"`
	Timestamp   time.Time `json:"timestamp,omitempty"`
}


type Config struct {
	
	PeerID int64
	
	SessionID string
	
	SessionFile string
	
	MaxLoopHistory int
	
	LoopSimilarityThreshold float64
	
	AutoSave bool
	
	SystemPrompt string
	
	LoopAlertEnabled bool
	
	LoopAlertMessage string
	
	WorkingDir string
	
	Store store.Store
}


func DefaultConfig() Config {
	return Config{
		PeerID:                  0,
		SessionFile:             "",
		MaxLoopHistory:          5,
		LoopSimilarityThreshold: 0.85,
		AutoSave:                false,
		SystemPrompt:            "You are a helpful assistant.",
		LoopAlertEnabled:        true,
		LoopAlertMessage:        "WARNING: You are repeating yourself. This appears to be a loop. Please provide a different response.",
	}
}


type Session struct {
	config      Config
	sessionID   string
	messages    []Message
	pinned      []string
	loopHistory []string
	loopCount   int
	isLooped    bool
	mu          sync.RWMutex
	createdAt   time.Time
	updatedAt   time.Time
	workingDir  string
	resumePrompt string
	peerInput   *PeerInput
}


func NewSession(config Config) *Session {
	s := &Session{
		config:      config,
		sessionID:   generateSessionID(config.SessionID),
		messages:    make([]Message, 0),
		loopHistory: make([]string, 0, config.MaxLoopHistory),
		createdAt:   time.Now(),
		updatedAt:   time.Now(),
		workingDir:  config.WorkingDir,
		peerInput:   &PeerInput{},
	}

	
	if config.SystemPrompt != "" {
		s.messages = append(s.messages, Message{
			Role:    SystemRole,
			Content: s.buildSystemMessage(),
			Timestamp: time.Now(),
		})
	}

	
	if config.Store != nil {
		s.loadFromStore(config.Store)
	} else if config.SessionFile != "" {
		s.Load()
	}

	if config.SystemPrompt != "" {
		s.UpdateSystemPrompt(config.SystemPrompt)
	}

	return s
}


func (s *Session) buildSystemMessage() string {
	content := s.config.SystemPrompt
	if content == "" {
		return ""
	}
	if s.workingDir != "" {
		content += "\n\nWorking directory: " + s.workingDir
	}
	return content
}


func (s *Session) UpdateSystemPrompt(newPrompt string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.config.SystemPrompt = newPrompt
	updated := s.buildSystemMessage()

	
	if len(s.messages) > 0 && s.messages[0].Role == SystemRole {
		s.messages[0].Content = updated
	} else {
		
		s.messages = append([]Message{{
			Role:      SystemRole,
			Content:   updated,
			Timestamp: time.Now(),
		}}, s.messages...)
	}

	if s.config.AutoSave {
		s.saveNow()
	}
}


func (s *Session) getSystemMessageIndex() int {
	for i, msg := range s.messages {
		if msg.Role == SystemRole {
			return i
		}
	}
	return -1
}


func (s *Session) AddUserMessage(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	msg := Message{
		Role:      UserRole,
		Content:   content,
		Timestamp: time.Now(),
	}
	s.messages = append(s.messages, msg)
	s.updatedAt = time.Now()

	if s.config.AutoSave {
		s.saveNow()
	}
}


func (s *Session) AddAssistantMessage(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	msg := Message{
		Role:      AssistantRole,
		Content:   content,
		Timestamp: time.Now(),
	}
	s.messages = append(s.messages, msg)
	s.updatedAt = time.Now()

	
	s.checkLoop(content)

	if s.config.AutoSave {
		s.saveNow()
	}
}


func (s *Session) AddAssistantMessageWithToolCalls(content string, toolCalls []MsgToolCall) {
	s.mu.Lock()
	defer s.mu.Unlock()

	msg := Message{
		Role:      AssistantRole,
		Content:   content,
		ToolCalls: toolCalls,
		Timestamp: time.Now(),
	}
	s.messages = append(s.messages, msg)
	s.updatedAt = time.Now()

	if s.config.AutoSave {
		s.saveNow()
	}
}


func (s *Session) AddAssistantMessageWithSummary(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	msg := Message{
		Role:      AssistantRole,
		Content:   content,
		Summary:   true,
		Timestamp: time.Now(),
	}
	s.messages = append(s.messages, msg)
	s.updatedAt = time.Now()

	if s.config.AutoSave {
		s.saveNow()
	}
}


func (s *Session) MarkCompaction(tailStartID int, summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tailStartID > 0 {
		for i := 0; i < tailStartID && i < len(s.messages); i++ {
			if s.messages[i].Role != SystemRole {
				s.messages[i].Compacted = true
			}
		}
	}

	s.messages = append(s.messages, Message{
		Role:      UserRole,
		Content:   tokenizers.CompactionUserMessage,
		Timestamp: time.Now(),
	})
	s.messages = append(s.messages, Message{
		Role:        AssistantRole,
		Content:     summary,
		Summary:     true,
		TailStartID: tailStartID,
		Timestamp:   time.Now(),
	})
	s.updatedAt = time.Now()

	if s.config.AutoSave {
		s.saveNow()
	}
}


func (s *Session) MarkMessageCompacted(index int, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if index < 0 || index >= len(s.messages) {
		return
	}
	s.messages[index].Content = content
	s.messages[index].Compacted = true
	s.updatedAt = time.Now()

	if s.config.AutoSave {
		s.saveNow()
	}
}


func (s *Session) AddToolMessage(toolCallID, toolName, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	msg := Message{
		Role:       ToolRole,
		Content:    content,
		ToolCallID: toolCallID,
		Name:       toolName,
		Timestamp:  time.Now(),
	}
	s.messages = append(s.messages, msg)
	s.updatedAt = time.Now()

	if s.config.AutoSave {
		s.saveNow()
	}
}


func (s *Session) GetHistory() []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Message, len(s.messages))
	copy(result, s.messages)
	return result
}


func (s *Session) GetSystemPrompt() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.config.SystemPrompt
}


func (s *Session) RestoreMessages(msgs []Message) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages = make([]Message, len(msgs))
	copy(s.messages, msgs)

	
	for _, msg := range s.messages {
		if msg.Role == SystemRole {
			s.config.SystemPrompt = msg.Content
			break
		}
	}

	s.updatedAt = time.Now()
}


func (s *Session) AddPinned(content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.pinned = append(s.pinned, content)
	s.updatedAt = time.Now()

	if s.config.AutoSave {
		s.saveNow()
	}
}


func (s *Session) GetPinned() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]string, len(s.pinned))
	copy(result, s.pinned)
	return result
}


func (s *Session) ClearPinned() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pinned = nil
	s.updatedAt = time.Now()

	if s.config.AutoSave {
		s.saveNow()
	}
}


func (s *Session) GetContextMessages() []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	history := s.messages

	
	visible := make([]Message, 0, len(history))
	markerIdx, markerTailStart := findLastCompactionMarkerLocked(history)
	if markerIdx >= 0 {
		compactionUserIdx := -1
		for j := markerIdx - 1; j >= 0; j-- {
			if history[j].Role == UserRole {
				compactionUserIdx = j
				break
			}
		}
		tailStartID := markerTailStart
		if tailStartID <= 0 || tailStartID >= len(history) {
			tailStartID = compactionUserIdx + 1
		}
		if compactionUserIdx < 0 {
			visible = history
		} else {
			visible = append(visible, history[compactionUserIdx])
			visible = append(visible, history[markerIdx])
			for i := tailStartID; i < len(history); i++ {
				if i == compactionUserIdx || i == markerIdx {
					continue
				}
				visible = append(visible, history[i])
			}
			
			if len(history) > 0 && history[0].Role == SystemRole {
				visible = append([]Message{history[0]}, visible...)
			}
		}
	} else {
		visible = history
	}

	
	out := make([]Message, 0, len(visible)+len(s.pinned))
	inserted := false
	for _, msg := range visible {
		out = append(out, msg)
		if !inserted && msg.Role == SystemRole && len(s.pinned) > 0 {
			for _, p := range s.pinned {
				if hasUserMessageContent(visible, p) {
					continue
				}
				out = append(out, Message{
					Role:      UserRole,
					Content:   p,
					Timestamp: time.Now(),
				})
			}
			inserted = true
		}
	}
	if !inserted && len(s.pinned) > 0 {
		for _, p := range s.pinned {
			if hasUserMessageContent(visible, p) {
				continue
			}
			out = append(out, Message{
				Role:      UserRole,
				Content:   p,
				Timestamp: time.Now(),
			})
		}
	}
	return out
}


func findLastCompactionMarkerLocked(msgs []Message) (markerIdx int, tailStartID int) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == AssistantRole && msgs[i].Summary {
			return i, msgs[i].TailStartID
		}
	}
	return -1, 0
}


func hasUserMessageContent(msgs []Message, content string) bool {
	for _, msg := range msgs {
		if msg.Role == UserRole && strings.TrimSpace(msg.Content) == strings.TrimSpace(content) {
			return true
		}
	}
	return false
}


func (s *Session) GetLastAssistantMessage() *Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getLastAssistantMessageLocked()
}


func (s *Session) getLastAssistantMessageLocked() *Message {
	for i := len(s.messages) - 1; i >= 0; i-- {
		if s.messages[i].Role == AssistantRole {
			msg := s.messages[i]
			return &msg
		}
	}
	return nil
}


func (s *Session) HistoryLength() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.messages) - 1
}


func (s *Session) checkLoop(content string) {
	content = normalizeString(content)

	
	for _, prev := range s.loopHistory {
		if prev == content {
			s.isLooped = true
			s.loopCount++
			return
		}
	}

	
	if len(s.loopHistory) >= 1 {
		prev := s.loopHistory[len(s.loopHistory)-1]
		if similarity(prev, content) >= s.config.LoopSimilarityThreshold {
			s.isLooped = true
			s.loopCount++
			return
		}
	}

	
	s.loopHistory = append(s.loopHistory, content)

	
	if len(s.loopHistory) > s.config.MaxLoopHistory {
		s.loopHistory = s.loopHistory[len(s.loopHistory)-s.config.MaxLoopHistory:]
	}
}


func (s *Session) IsLoopDetected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isLooped
}


func (s *Session) GetLoopCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loopCount
}


func (s *Session) GetLoopAlertMessage() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.config.LoopAlertEnabled {
		return ""
	}

	if s.config.LoopAlertMessage != "" {
		return s.config.LoopAlertMessage
	}

	return fmt.Sprintf("WARNING: You are repeating yourself. Loop detected %d times. Please provide a different response.", s.loopCount)
}


func (s *Session) ResetLoopDetection() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.loopHistory = make([]string, 0, s.config.MaxLoopHistory)
	s.loopCount = 0
	s.isLooped = false
}


func normalizeString(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	
	parts := strings.Fields(s)
	s = strings.Join(parts, " ")
	return s
}


func similarity(a, b string) float64 {
	a = normalizeString(a)
	b = normalizeString(b)

	if a == b {
		return 1.0
	}

	
	wordsA := strings.Fields(a)
	wordsB := strings.Fields(b)

	wordSet := make(map[string]int)
	for _, w := range wordsA {
		wordSet[w]++
	}
	for _, w := range wordsB {
		wordSet[w]++
	}

	
	common := 0
	for _, count := range wordSet {
		if count >= 2 {
			common++
		}
	}

	total := len(wordsA) + len(wordsB)
	if total == 0 {
		return 0
	}

	return float64(common) / float64(total) * 2 
}


func (s *Session) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessionID = generateSessionID("")
	s.messages = s.messages[:0]

	
	if s.config.SystemPrompt != "" {
		s.messages = append(s.messages, Message{
			Role:    SystemRole,
			Content: s.config.SystemPrompt,
			Timestamp: time.Now(),
		})
	}

	
	s.loopHistory = make([]string, 0, s.config.MaxLoopHistory)
	s.loopCount = 0
	s.isLooped = false
	s.updatedAt = time.Now()

	s.resumePrompt = ""

	if s.peerInput != nil {
		s.peerInput.Clear()
	}

	if s.config.AutoSave {
		s.saveNow()
	}
}


type SessionData struct {
	PeerID     int64         `json:"peer_id"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
	Messages   []MessageData `json:"messages"`
	WorkingDir string        `json:"working_dir,omitempty"`
	
	LoopCount  int    `json:"loop_count"`
	IsLooped   bool   `json:"is_looped"`
	LastLooped string `json:"last_looped,omitempty"`
	
	Pinned []string `json:"pinned,omitempty"`
}


type MessageData struct {
	Role        string                   `json:"role"`
	Content     string                   `json:"content"`
	ToolCalls   []ToolCallData           `json:"tool_calls,omitempty"`
	ToolCallID  string                   `json:"tool_call_id,omitempty"`
	Name        string                   `json:"name,omitempty"`
	Summary     bool                     `json:"summary,omitempty"`
	Compacted   bool                     `json:"compacted,omitempty"`
	TailStartID int                      `json:"tail_start_id,omitempty"`
	Timestamp   string                   `json:"timestamp,omitempty"`
}


type ToolCallData struct {
	ID       string             `json:"id"`
	Type     string             `json:"type,omitempty"`
	Function ToolCallFuncData   `json:"function,omitempty"`
}


type ToolCallFuncData struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}


func (s *Session) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.saveInternal()
}


func (s *Session) saveNow() error {
	return s.saveInternal()
}


func (s *Session) saveInternal() error {

	if s.config.Store != nil {
		return s.saveToStore(s.config.Store)
	}

	if s.config.SessionFile == "" {
		return nil
	}

	
	dir := filepath.Dir(s.config.SessionFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}

	
	messages := make([]MessageData, len(s.messages))
	for i, msg := range s.messages {
		msgData := MessageData{
			Role:        string(msg.Role),
			Content:     msg.Content,
			ToolCallID:  msg.ToolCallID,
			Name:        msg.Name,
			Summary:     msg.Summary,
			Compacted:   msg.Compacted,
			TailStartID: msg.TailStartID,
			Timestamp:   msg.Timestamp.Format(time.RFC3339),
		}
		
		if len(msg.ToolCalls) > 0 {
			msgData.ToolCalls = make([]ToolCallData, len(msg.ToolCalls))
			for j, tc := range msg.ToolCalls {
				msgData.ToolCalls[j] = ToolCallData{
					ID:   tc.ID,
					Type: tc.Type,
					Function: ToolCallFuncData{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
		}
		messages[i] = msgData
	}

	session := SessionData{
		PeerID:     s.config.PeerID,
		CreatedAt:  s.createdAt,
		UpdatedAt:  s.updatedAt,
		Messages:   messages,
		WorkingDir: s.workingDir,
		LoopCount:  s.loopCount,
		IsLooped:   s.isLooped,
		Pinned:     s.pinned,
	}

	
	if s.isLooped && s.GetLastAssistantMessage() != nil {
		session.LastLooped = s.GetLastAssistantMessage().Content
	}

	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	
	tmpFile := s.config.SessionFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("write session tmp: %w", err)
	}

	
	if err := os.Rename(tmpFile, s.config.SessionFile); err != nil {
		return fmt.Errorf("rename session file: %w", err)
	}

	return nil
}


func (s *Session) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.config.SessionFile)
	if err != nil {
		
		if os.IsNotExist(err) {
			fmt.Printf("[SESSION] File not found: %s, creating new session\n", s.config.SessionFile)
			return nil
		}
		return fmt.Errorf("read session file: %w", err)
	}

	var session SessionData
	if err := json.Unmarshal(data, &session); err != nil {
		return fmt.Errorf("parse session file: %w", err)
	}

	fmt.Printf("[SESSION] Loaded %d messages from %s (peer_id from file: %d)\n",
		len(session.Messages), s.config.SessionFile, session.PeerID)

	
	s.messages = make([]Message, len(session.Messages))
	for i, msg := range session.Messages {
		timestamp, _ := time.Parse(time.RFC3339, msg.Timestamp)
		message := Message{
			Role:        Role(msg.Role),
			Content:     msg.Content,
			ToolCallID:  msg.ToolCallID,
			Name:        msg.Name,
			Summary:     msg.Summary,
			Compacted:   msg.Compacted,
			TailStartID: msg.TailStartID,
			Timestamp:   timestamp,
		}
		
		if len(msg.ToolCalls) > 0 {
			message.ToolCalls = make([]MsgToolCall, len(msg.ToolCalls))
			for j, tc := range msg.ToolCalls {
				message.ToolCalls[j] = MsgToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: MsgToolCallFunc{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
		}
		s.messages[i] = message
	}

	
	if session.WorkingDir != "" {
		if _, err := os.Stat(session.WorkingDir); err == nil {
			s.workingDir = session.WorkingDir
		} else {
			fmt.Printf("[SESSION] Working dir '%s' does not exist, using default\n", session.WorkingDir)
		}
	}

	
	s.loopCount = session.LoopCount
	s.isLooped = session.IsLooped
	s.createdAt = session.CreatedAt
	s.updatedAt = session.UpdatedAt

	
	s.pinned = make([]string, len(session.Pinned))
	copy(s.pinned, session.Pinned)

	
	if session.LastLooped != "" {
		s.loopHistory = append(s.loopHistory, normalizeString(session.LastLooped))
	}

	return nil
}


func (s *Session) GetWorkingDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workingDir
}


func (s *Session) SetWorkingDir(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workingDir = dir

	
	idx := s.getSystemMessageIndex()
	if idx >= 0 {
		s.messages[idx].Content = s.buildSystemMessage()
	}

	s.saveNow()
}


func (s *Session) GetPeerID() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.PeerID
}


func (s *Session) GetSessionID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionID
}


// GetPeerInput returns the shared per-peer inbox used to hand user messages
// that arrive mid-turn to the running agent loop.
func (s *Session) GetPeerInput() *PeerInput {
	return s.peerInput
}


// SetPeerInput replaces the inbox. The agent-loop layer points a fresh agent
// session at the durable session's inbox so the running tool loop sees messages
// admitted while it executes.
func (s *Session) SetPeerInput(in *PeerInput) {
	s.peerInput = in
}


func (s *Session) SetResumePrompt(prompt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resumePrompt = prompt
	s.updatedAt = time.Now()
	if s.config.AutoSave {
		s.saveNow()
	}
}


func (s *Session) GetResumePrompt() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.resumePrompt
}


var (
	randGen          = rand.New(rand.NewSource(time.Now().UnixNano()))
	randMu           sync.Mutex
	sessionIDCounter uint64
)

func generateSessionID(providedID string) string {
	if providedID != "" {
		return providedID
	}
	h := fnv.New128a()
	randMu.Lock()
	random := randGen.Uint64()
	randMu.Unlock()
	counter := atomic.AddUint64(&sessionIDCounter, 1)
	h.Write([]byte(strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + strconv.FormatUint(random, 10) + "-" + strconv.FormatUint(counter, 10)))
	sum := h.Sum(nil)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}


func (s *Session) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result string
	for i, msg := range s.messages {
		result += fmt.Sprintf("%d. [%s]: %s\n", i+1, msg.Role, msg.Content)
	}
	return result
}


func NormalizeString(s string) string {
	return normalizeString(s)
}


func CalcSimilarity(a, b string) float64 {
	return similarity(a, b)
}
