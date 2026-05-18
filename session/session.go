package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
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
)

// Message представляет одно сообщение в истории диалога
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

// ============================================================
// Конфигурация Session
// ============================================================

// Config содержит настройки сессии
type Config struct {
	// PeerID — идентификатор пользователя (VK peer_id)
	PeerID int64
	// SessionFile — путь к файлу для сохранения сессии
	SessionFile string
	// MaxHistory — максимальное количество сообщений в истории
	MaxHistory int
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
}

// DefaultConfig возвращает конфигурацию по умолчанию
func DefaultConfig() Config {
	return Config{
		PeerID:                  0,
		SessionFile:             "",
		MaxHistory:              100,
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

// Session управляет историей диалога и обнаружением зацикливания
type Session struct {
	config     Config
	messages   []Message
	loopHistory []string // последние N ответов AI для обнаружения цикла
	loopCount  int      // количество обнаруженных циклов
	isLooped   bool     // флаг обнаруженного цикла
	mu         sync.RWMutex
	createdAt  time.Time
	updatedAt  time.Time
	workingDir string  // текущая рабочая директория для инструментов
}

// NewSession создаёт новую сессию
func NewSession(config Config) *Session {
	s := &Session{
		config:     config,
		messages:   make([]Message, 0),
		loopHistory: make([]string, 0, config.MaxLoopHistory),
		createdAt:  time.Now(),
		updatedAt:  time.Now(),
		workingDir: config.WorkingDir,
	}

	// Добавляем системное сообщение с рабочей директорией
	if config.SystemPrompt != "" {
		s.messages = append(s.messages, Message{
			Role:    SystemRole,
			Content: s.buildSystemMessage(),
			Timestamp: time.Now(),
		})
	}

	// Если указан файл сессии — загружаем существующую
	if config.SessionFile != "" {
		s.Load()
		// Обновляем системный промпт после загрузки (на случай если промпт изменился)
		if config.SystemPrompt != "" {
			s.UpdateSystemPrompt(config.SystemPrompt)
		}
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
	s.enforceHistoryLimit()
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
	s.enforceHistoryLimit()
	s.updatedAt = time.Now()

	// Проверка на зацикливание
	s.checkLoop(content)

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

// GetLastAssistantMessage возвращает последнее сообщение ассистента
func (s *Session) GetLastAssistantMessage() *Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

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

// enforceHistoryLimit удаляет старые сообщения при превышении лимита
func (s *Session) enforceHistoryLimit() {
	systemOffset := 0
	if s.config.SystemPrompt != "" {
		systemOffset = 1
	}

	for len(s.messages)-systemOffset > s.config.MaxHistory {
		for i := systemOffset; i < len(s.messages); i++ {
			if s.messages[i].Role != SystemRole {
				s.messages = append(s.messages[:i], s.messages[i+1:]...)
				break
			}
		}
	}
}

// ============================================================
// Reset и Clear
// ============================================================

// Reset полностью очищает историю, оставляя только системное сообщение
func (s *Session) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

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
}

// MessageData — сериализуемая версия Message
type MessageData struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp,omitempty"`
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
		messages[i] = MessageData{
			Role:      string(msg.Role),
			Content:   msg.Content,
			Timestamp: msg.Timestamp.Format(time.RFC3339),
		}
	}

	session := SessionData{
		PeerID:     s.config.PeerID,
		CreatedAt:  s.createdAt,
		UpdatedAt:  s.updatedAt,
		Messages:   messages,
		WorkingDir: s.workingDir,
		LoopCount:  s.loopCount,
		IsLooped:   s.isLooped,
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
			return nil
		}
		return fmt.Errorf("read session file: %w", err)
	}

	var session SessionData
	if err := json.Unmarshal(data, &session); err != nil {
		return fmt.Errorf("parse session file: %w", err)
	}

	// Восстанавливаем сообщения
	s.messages = make([]Message, len(session.Messages))
	for i, msg := range session.Messages {
		timestamp, _ := time.Parse(time.RFC3339, msg.Timestamp)
		s.messages[i] = Message{
			Role:      Role(msg.Role),
			Content:   msg.Content,
			Timestamp: timestamp,
		}
	}

	// Восстанавливаем рабочую директорию
	if session.WorkingDir != "" {
		s.workingDir = session.WorkingDir
	}

	// Восстанавливаем состояние loop detection
	s.loopCount = session.LoopCount
	s.isLooped = session.IsLooped
	s.createdAt = session.CreatedAt
	s.updatedAt = session.UpdatedAt

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
