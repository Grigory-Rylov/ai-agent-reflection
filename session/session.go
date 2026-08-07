package session

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opencode/llama-client/pkg/store"
	"github.com/opencode/llama-client/pkg/tokenizers"
)

// ============================================================
// Типы и константы
// ============================================================

// Role определяет роль сообщения в диалоге
type Role string

const (
	SystemRole    Role = "system"
	UserRole      Role = "user"
	AssistantRole Role = "assistant"
	ToolRole      Role = "tool"
)

// MsgToolCall представляет вызов инструмента в сообщении ассистента
type MsgToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type,omitempty"`
	Function MsgToolCallFunc    `json:"function,omitempty"`
}

// MsgToolCallFunc представляет функцию в tool call
type MsgToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Message представляет одно сообщение в истории диалога
type Message struct {
	Role       Role          `json:"role"`
	Content    string        `json:"content"`
	ToolCalls  []MsgToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"` // ID инструмента для сообщений с role=tool
	Name       string        `json:"name,omitempty"`         // Имя инструмента для сообщений с role=tool
	Summary    bool          `json:"summary,omitempty"`      // true если это результат компактизации (для FilterCompacted)
	// Compacted — true если сообщение скрыто компактизацией (головная часть)
	// или output обрезан pruning-ом. В модель контекст такие сообщения не попадают.
	Compacted bool `json:"compacted,omitempty"`
	// TailStartID — индекс первого сообщения хвоста (tail), сохранённого при
	// компактизации. Хранится на summary-сообщении; индекс является стабильным
	// ID, т.к. сообщения в истории только добавляются.
	TailStartID int       `json:"tail_start_id,omitempty"`
	Timestamp   time.Time `json:"timestamp,omitempty"`
}

// ============================================================
// Конфигурация Session
// ============================================================

// Config contains session settings
type Config struct {
	// PeerID — идентификатор пользователя (VK peer_id)
	PeerID int64
	// SessionID — уникальный идентификатор сессии
	SessionID string
	// SessionFile — путь к файлу для сохранения сессии
	SessionFile string
	// MaxLoopHistory — сколько последних ответов AI отслеживать для обнаружения цикла
	MaxLoopHistory int
	// LoopSimilarityThreshold — порог схожести для обнаружения цикла (0.0-1.0)
	LoopSimilarityThreshold float64
	// AutoSave — автоматически сохранять сессию после каждого изменения
	AutoSave bool
	// SystemPrompt — системный промпт для AI
	SystemPrompt string
	// LoopAlertEnabled — включать ли alert при обнаружении цикла
	LoopAlertEnabled bool
	// LoopAlertMessage — пользовательский alert при обнаружении цикла
	LoopAlertMessage string
	// WorkingDir — текущая рабочая директория для инструментов
	WorkingDir string
	// Store — SQLite store для персистентности (если nil, используется SessionFile)
	Store store.Store
}

// DefaultConfig возвращает конфигурацию по умолчанию
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

// ============================================================
// Session — основная сущность для хранения истории сессии
// ============================================================

// Session manages dialog history and loop detection
type Session struct {
	config      Config
	sessionID   string
	messages    []Message
	pinned      []string // промпты /pin, переживающие компактизацию и reset
	loopHistory []string // последние N ответов AI для обнаружения цикла
	loopCount   int      // количество обнаруженных циклов
	isLooped    bool     // флаг обнаруженного цикла
	mu          sync.RWMutex
	createdAt   time.Time
	updatedAt   time.Time
	workingDir  string // текущая рабочая директория для инструментов
	resumePrompt string // последний user-промпт незавершённой обработки (для resume после рестарта)
}

// NewSession creates a new session
func NewSession(config Config) *Session {
	s := &Session{
		config:      config,
		sessionID:   generateSessionID(config.SessionID),
		messages:    make([]Message, 0),
		loopHistory: make([]string, 0, config.MaxLoopHistory),
		createdAt:   time.Now(),
		updatedAt:   time.Now(),
		workingDir:  config.WorkingDir,
	}

	// Добавляем системное сообщение с рабочей директорией
	if config.SystemPrompt != "" {
		s.messages = append(s.messages, Message{
			Role:    SystemRole,
			Content: s.buildSystemMessage(),
			Timestamp: time.Now(),
		})
	}

	// Загружаем существующую сессию
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

// ============================================================
// Обновление системного промпта
// ============================================================

// buildSystemMessage возвращает системный промпт с добавлением рабочей директории
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

// UpdateSystemPrompt обновляет системный промпт в истории сессии
func (s *Session) UpdateSystemPrompt(newPrompt string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.config.SystemPrompt = newPrompt
	updated := s.buildSystemMessage()

	// Обновляем первое сообщение (системное) если оно есть
	if len(s.messages) > 0 && s.messages[0].Role == SystemRole {
		s.messages[0].Content = updated
	} else {
		// Добавляем системное сообщение если его нет
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

// getSystemMessageIndex возвращает индекс системного сообщения в истории
func (s *Session) getSystemMessageIndex() int {
	for i, msg := range s.messages {
		if msg.Role == SystemRole {
			return i
		}
	}
	return -1
}

// ============================================================
// Работа с сообщениями
// ============================================================

// AddUserMessage добавляет сообщение пользователя в историю
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

// AddAssistantMessage добавляет сообщение ассистента в историю и отслеживает цикл
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

	// Проверка на зацикливание
	s.checkLoop(content)

	if s.config.AutoSave {
		s.saveNow()
	}
}

// AddAssistantMessageWithToolCalls добавляет сообщение ассистента с вызовами инструментов
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

// AddAssistantMessageWithSummary добавляет assistant сообщение с флагом Summary=true
// для маркировки результатов компактизации (нужно для FilterCompacted)
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

// MarkCompaction помечает головную часть истории как сжатую и добавляет маркер
// компактизации в конец, не стирая историю (модель opencode: старые сообщения
// остаются в БД, а при построении запроса GetContextMessages/FilterCompacted
// переупорядочивает их в [compaction-user, summary, ...tail...]).
// tailStartID — индекс первого сообщения сохранённого хвоста в s.messages.
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

// MarkMessageCompacted помечает сообщение по индексу как сжатое и заменяет его
// контент (используется pruning-ом). Не удаляет сообщение — индексы истории
// (в т.ч. tail_start_id маркеров) остаются стабильными.
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

// AddToolMessage добавляет результат выполнения инструмента в историю
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

// GetHistory возвращает все сообщения для отправки в API
func (s *Session) GetHistory() []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Message, len(s.messages))
	copy(result, s.messages)
	return result
}

// RestoreMessages заменяет историю сообщений целиком, сохраняя все метаданные
// (Summary/Compacted/TailStartID). Используется при восстановлении сессии
// сабагента после рестарта — чтобы маркеры компактизации переживали резюм.
func (s *Session) RestoreMessages(msgs []Message) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages = make([]Message, len(msgs))
	copy(s.messages, msgs)

	// Восстанавливаем системный промпт из первого system-сообщения.
	for _, msg := range s.messages {
		if msg.Role == SystemRole {
			s.config.SystemPrompt = msg.Content
			break
		}
	}

	s.updatedAt = time.Now()
}

// ============================================================
// Pinned промпты (/pin) — переживают компактизацию и reset
// ============================================================

// AddPinned добавляет pinned промпт, который сохраняется в начале контекста
// даже после компактизации. Пустые промпты игнорируются.
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

// GetPinned возвращает список pinned промптов
func (s *Session) GetPinned() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]string, len(s.pinned))
	copy(result, s.pinned)
	return result
}

// ClearPinned удаляет все pinned промпты
func (s *Session) ClearPinned() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pinned = nil
	s.updatedAt = time.Now()

	if s.config.AutoSave {
		s.saveNow()
	}
}

// GetContextMessages возвращает сообщения для API: системное сообщение,
// затем все pinned промпты (как user), затем остальная история. После
// компактизации история переупорядочивается как в opencode filterCompacted():
// [system, pinned..., compaction-user, summary, ...tail...]. Головная часть
// (compacted) в контекст не попадает, но остаётся в истории (для последующих
// компактизаций). Pinned промпт не дублируется: если его контент уже есть в
// видимых сообщениях (например, сразу после /pin, когда промпт отправлен как
// обычное сообщение), он не вставляется повторно и появляется в контексте
// только после компактизации, когда исходное сообщение скрыто.
func (s *Session) GetContextMessages() []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	history := s.messages

	// Собираем видимые сообщения (переупорядоченные после компактизации).
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
			// Системное сообщение (в начале истории) всегда идёт первым.
			if len(history) > 0 && history[0].Role == SystemRole {
				visible = append([]Message{history[0]}, visible...)
			}
		}
	} else {
		visible = history
	}

	// Вставляем pinned промпты после системного сообщения.
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

// findLastCompactionMarkerLocked возвращает индекс и tail_start_id последнего
// summary-сообщения компактизации (или -1/0, если его нет).
func findLastCompactionMarkerLocked(msgs []Message) (markerIdx int, tailStartID int) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == AssistantRole && msgs[i].Summary {
			return i, msgs[i].TailStartID
		}
	}
	return -1, 0
}

// hasUserMessageContent возвращает true, если в msgs уже есть user-сообщение
// с таким же контентом.
func hasUserMessageContent(msgs []Message, content string) bool {
	for _, msg := range msgs {
		if msg.Role == UserRole && strings.TrimSpace(msg.Content) == strings.TrimSpace(content) {
			return true
		}
	}
	return false
}

// GetLastAssistantMessage возвращает последнее сообщение ассистента
func (s *Session) GetLastAssistantMessage() *Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getLastAssistantMessageLocked()
}

// getLastAssistantMessageLocked — версия без блокировки для вызова из locked-контекста
func (s *Session) getLastAssistantMessageLocked() *Message {
	for i := len(s.messages) - 1; i >= 0; i-- {
		if s.messages[i].Role == AssistantRole {
			msg := s.messages[i]
			return &msg
		}
	}
	return nil
}

// HistoryLength возвращает количество сообщений (без системного)
func (s *Session) HistoryLength() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.messages) - 1
}

// ============================================================
// Loop Detection — обнаружение зацикливания AI
// ============================================================

// checkLoop проверяет, не повторяет ли AI свой ответ
func (s *Session) checkLoop(content string) {
	content = normalizeString(content)

	// Проверяем на точное совпадение с предыдущими ответами ДО добавления текущего
	for _, prev := range s.loopHistory {
		if prev == content {
			s.isLooped = true
			s.loopCount++
			return
		}
	}

	// Проверяем на схожесть (если есть хотя бы 1 предыдущий ответ)
	if len(s.loopHistory) >= 1 {
		prev := s.loopHistory[len(s.loopHistory)-1]
		if similarity(prev, content) >= s.config.LoopSimilarityThreshold {
			s.isLooped = true
			s.loopCount++
			return
		}
	}

	// Добавляем в историю ответов AI (только после проверки!)
	s.loopHistory = append(s.loopHistory, content)

	// Обрезаем до MaxLoopHistory
	if len(s.loopHistory) > s.config.MaxLoopHistory {
		s.loopHistory = s.loopHistory[len(s.loopHistory)-s.config.MaxLoopHistory:]
	}
}

// IsLoopDetected возвращает true если обнаружен цикл
func (s *Session) IsLoopDetected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isLooped
}

// GetLoopCount возвращает количество обнаруженных циклов
func (s *Session) GetLoopCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loopCount
}

// GetLoopAlertMessage возвращает сообщение для уведомления модели о цикле
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

// ResetLoopDetection сбрасывает состояние обнаружения цикла
func (s *Session) ResetLoopDetection() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.loopHistory = make([]string, 0, s.config.MaxLoopHistory)
	s.loopCount = 0
	s.isLooped = false
}

// ============================================================
// Утилиты для Loop Detection
// ============================================================

// normalizeString нормализует строку для сравнения
func normalizeString(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	// Удаляем лишние пробелы
	parts := strings.Fields(s)
	s = strings.Join(parts, " ")
	return s
}

// similarity вычисляет схожесть двух строк (0.0-1.0)
// Использует алгоритм на основе общих слов
func similarity(a, b string) float64 {
	a = normalizeString(a)
	b = normalizeString(b)

	if a == b {
		return 1.0
	}

	// Считаем общие слова
	wordsA := strings.Fields(a)
	wordsB := strings.Fields(b)

	wordSet := make(map[string]int)
	for _, w := range wordsA {
		wordSet[w]++
	}
	for _, w := range wordsB {
		wordSet[w]++
	}

	// Считаем слова которые есть в обоих
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

	return float64(common) / float64(total) * 2 // умножаем на 2 для нормализации
}

// ============================================================
// Управление историей
// ============================================================

// ============================================================
// Reset и Clear
// ============================================================

// Reset полностью очищает историю, оставляя только системное сообщение
func (s *Session) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessionID = generateSessionID("")
	s.messages = s.messages[:0]

	// Восстанавливаем системное сообщение
	if s.config.SystemPrompt != "" {
		s.messages = append(s.messages, Message{
			Role:    SystemRole,
			Content: s.config.SystemPrompt,
			Timestamp: time.Now(),
		})
	}

	// Сбрасываем loop detection
	s.loopHistory = make([]string, 0, s.config.MaxLoopHistory)
	s.loopCount = 0
	s.isLooped = false
	s.updatedAt = time.Now()

	// Сбрасываем флаг незавершённой задачи
	s.resumePrompt = ""

	if s.config.AutoSave {
		s.saveNow()
	}
}

// ============================================================
// Persistence — сохранение и загрузка сессии
// ============================================================

// SessionData представляет сериализуемую структуру сессии
type SessionData struct {
	PeerID     int64         `json:"peer_id"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
	Messages   []MessageData `json:"messages"`
	WorkingDir string        `json:"working_dir,omitempty"`
	// Loop detection state
	LoopCount  int    `json:"loop_count"`
	IsLooped   bool   `json:"is_looped"`
	LastLooped string `json:"last_looped,omitempty"`
	// Pinned промпты (/pin), переживающие компактизацию
	Pinned []string `json:"pinned,omitempty"`
}

// MessageData — сериализуемая версия Message
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

// ToolCallData — сериализуемая версия ToolCall
type ToolCallData struct {
	ID       string             `json:"id"`
	Type     string             `json:"type,omitempty"`
	Function ToolCallFuncData   `json:"function,omitempty"`
}

// ToolCallFuncData — сериализуемая версия ToolCallFunction
type ToolCallFuncData struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Save сохраняет сессию в файл
func (s *Session) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.saveInternal()
}

// saveNow — внутренняя версия Save (без проверки AutoSave, без блокировки)
func (s *Session) saveNow() error {
	return s.saveInternal()
}

// saveInternal — внутренняя версия Save (без блокировки, вызывается из locked-контекста)
func (s *Session) saveInternal() error {

	if s.config.Store != nil {
		return s.saveToStore(s.config.Store)
	}

	if s.config.SessionFile == "" {
		return nil
	}

	// Создаём директории если нужно
	dir := filepath.Dir(s.config.SessionFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}

	// Собираем данные для сериализации
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
		// Сериализуем tool_calls если есть
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

	// Сохраняем последний ответ AI если цикл обнаружен
	if s.isLooped && s.GetLastAssistantMessage() != nil {
		session.LastLooped = s.GetLastAssistantMessage().Content
	}

	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	// Записываем через временный файл для атомарности
	tmpFile := s.config.SessionFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("write session tmp: %w", err)
	}

	// Переименовываем (атомарная операция)
	if err := os.Rename(tmpFile, s.config.SessionFile); err != nil {
		return fmt.Errorf("rename session file: %w", err)
	}

	return nil
}

// Load загружает сессию из файла
func (s *Session) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.config.SessionFile)
	if err != nil {
		// Если файл не существует — создаём новую сессию
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

	// Восстанавливаем сообщения
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
		// Восстанавливаем tool_calls если есть
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

	// Восстанавливаем рабочую директорию (только если существует)
	if session.WorkingDir != "" {
		if _, err := os.Stat(session.WorkingDir); err == nil {
			s.workingDir = session.WorkingDir
		} else {
			fmt.Printf("[SESSION] Working dir '%s' does not exist, using default\n", session.WorkingDir)
		}
	}

	// Восстанавливаем состояние loop detection
	s.loopCount = session.LoopCount
	s.isLooped = session.IsLooped
	s.createdAt = session.CreatedAt
	s.updatedAt = session.UpdatedAt

	// Восстанавливаем pinned промпты
	s.pinned = make([]string, len(session.Pinned))
	copy(s.pinned, session.Pinned)

	// Добавляем последний ответ AI в loopHistory для корректной работы
	if session.LastLooped != "" {
		s.loopHistory = append(s.loopHistory, normalizeString(session.LastLooped))
	}

	return nil
}

// ============================================================
// Working Directory — управление рабочей директорией сессии
// ============================================================

// GetWorkingDir возвращает текущую рабочую директорию сессии
func (s *Session) GetWorkingDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workingDir
}

// SetWorkingDir изменяет рабочую директорию сессии и обновляет системное сообщение
func (s *Session) SetWorkingDir(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workingDir = dir

	// Обновляем системное сообщение с новой директорией
	idx := s.getSystemMessageIndex()
	if idx >= 0 {
		s.messages[idx].Content = s.buildSystemMessage()
	}

	s.saveNow()
}

// ============================================================
// Утилиты
// ============================================================

// GetPeerID возвращает PeerID сессии
func (s *Session) GetPeerID() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.PeerID
}

// GetSessionID возвращает уникальный идентификатор сессии
func (s *Session) GetSessionID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionID
}

// SetResumePrompt сохраняет последний user-промпт незавершённой обработки.
// Персистится в БД; после рестарта непустое значение означает, что задачу
// надо продолжить. Пустая строка — обработка завершена.
func (s *Session) SetResumePrompt(prompt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resumePrompt = prompt
	s.updatedAt = time.Now()
	if s.config.AutoSave {
		s.saveNow()
	}
}

// GetResumePrompt возвращает последний незавершённый user-промпт (или "").
func (s *Session) GetResumePrompt() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.resumePrompt
}

// generateSessionID генерирует уникальный идентификатор сессии
// Если providedID не пустой, используется он, иначе генерируется новый
func generateSessionID(providedID string) string {
	if providedID != "" {
		return providedID
	}
	h := fnv.New128a()
	h.Write([]byte(strconv.FormatInt(time.Now().UnixNano(), 10)))
	sum := h.Sum(nil)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// String возвращает текстовое представление истории
func (s *Session) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result string
	for i, msg := range s.messages {
		result += fmt.Sprintf("%d. [%s]: %s\n", i+1, msg.Role, msg.Content)
	}
	return result
}

// ============================================================
// Утилиты для similarity (экспортируем для тестов)
// ============================================================

// NormalizeString для тестов
func NormalizeString(s string) string {
	return normalizeString(s)
}

// CalcSimilarity для тестов
func CalcSimilarity(a, b string) float64 {
	return similarity(a, b)
}
