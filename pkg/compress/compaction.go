package compress

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/opencode/llama-client/pkg/tokenizers"
)

// ============================================================
// ContextState — структурированное состояние контекста
// ============================================================

// ContextState представляет структурированное состояние сессии
// Вместо полной истории храним только значимую информацию
type ContextState struct {
	// Goal — текущая цель пользователя
	Goal string `json:"goal"`

	// CurrentStep — текущий шаг выполнения
	CurrentStep string `json:"current_step"`

	// Plan — план на ближайшие шаги (3-7)
	Plan []string `json:"plan"`

	// Done — завершённые шаги
	Done []string `json:"done"`

	// OpenQuestions — нерешённые вопросы
	OpenQuestions []string `json:"open_questions"`

	// WorkingMemory — важные факты (до 10)
	WorkingMemory []string `json:"working_memory"`

	// Decisions — принятые решения
	Decisions []string `json:"decisions"`

	// Artifacts — ссылки на файлы/ресурсы
	Artifacts []ArtifactRef `json:"artifacts"`

	// RecentContext — последние результаты (сжатые)
	RecentContext []string `json:"recent_context"`

	// NextSteps — следующие шаги
	NextSteps []string `json:"next_steps"`

	// LastUpdated — время последнего обновления
	LastUpdated time.Time `json:"last_updated"`
}

// ArtifactRef — ссылка на внешний артефакт
type ArtifactRef struct {
	Type        string `json:"type"`         // "file", "url", "tool_result"
	Path        string `json:"path"`         // путь или URL
	Description string `json:"description"`  // краткое описание
	Tokens      int    `json:"tokens"`       // примерный размер в токенах
	Summary     string `json:"summary"`      // выжимка содержания
}

// ToPrompt конвертирует состояние в промпт для модели
func (s *ContextState) ToPrompt() string {
	var sb strings.Builder

	if s.Goal != "" {
		sb.WriteString(fmt.Sprintf("## Current Goal\n%s\n\n", s.Goal))
	}

	if len(s.Plan) > 0 {
		sb.WriteString("## Plan\n")
		for i, step := range s.Plan {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
		}
		sb.WriteString("\n")
	}

	if len(s.Done) > 0 {
		sb.WriteString("## Completed\n")
		for _, item := range s.Done {
			sb.WriteString(fmt.Sprintf("- %s\n", item))
		}
		sb.WriteString("\n")
	}

	if len(s.Decisions) > 0 {
		sb.WriteString("## Decisions Made\n")
		for _, d := range s.Decisions {
			sb.WriteString(fmt.Sprintf("- %s\n", d))
		}
		sb.WriteString("\n")
	}

	if len(s.WorkingMemory) > 0 {
		sb.WriteString("## Important Facts\n")
		for _, fact := range s.WorkingMemory {
			sb.WriteString(fmt.Sprintf("- %s\n", fact))
		}
		sb.WriteString("\n")
	}

	if len(s.OpenQuestions) > 0 {
		sb.WriteString("## Open Questions\n")
		for _, q := range s.OpenQuestions {
			sb.WriteString(fmt.Sprintf("- %s\n", q))
		}
		sb.WriteString("\n")
	}

	if len(s.Artifacts) > 0 {
		sb.WriteString("## Available Artifacts\n")
		for _, a := range s.Artifacts {
			sb.WriteString(fmt.Sprintf("- %s (%s)\n", a.Path, a.Description))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// EstimateTokens оценивает количество токенов в состоянии
func (s *ContextState) EstimateTokens() int {
	return EstimateTokensSimple(s.ToPrompt())
}

// ============================================================
// CompactionLevel — уровни сжатия
// ============================================================

// CompactionLevel определяет степень сжатия
type CompactionLevel int

const (
	CompactionNone      CompactionLevel = iota // < 50%
	CompactionWarn                             // 50-70%
	CompactionNormal                           // 70-85%
	CompactionAggressive                       // > 85%
)

// CompactionThresholds — пороги для разных уровней
type CompactionThresholds struct {
	WarnPercent       float64 // 0.50
	NormalPercent     float64 // 0.70
	AggressivePercent float64 // 0.85
}

// DefaultThresholds возвращает пороги по умолчанию
func DefaultThresholds() CompactionThresholds {
	return CompactionThresholds{
		WarnPercent:       0.50,
		NormalPercent:     0.70,
		AggressivePercent: 0.85,
	}
}

// GetLevel определяет уровень сжатия по проценту заполнения
func (t CompactionThresholds) GetLevel(usagePercent float64) CompactionLevel {
	if usagePercent >= t.AggressivePercent {
		return CompactionAggressive
	}
	if usagePercent >= t.NormalPercent {
		return CompactionNormal
	}
	if usagePercent >= t.WarnPercent {
		return CompactionWarn
	}
	return CompactionNone
}

// ============================================================
// CompactionConfig — конфигурация сжатия
// ============================================================

// CompactionConfig настраивает поведение сжатия
type CompactionConfig struct {
	// Thresholds — пороги сжатия
	Thresholds CompactionThresholds

	// KeepLastMessages — сколько последних сообщений сохранять целиком
	KeepLastMessages int

	// MaxWorkingMemory — максимум элементов в рабочей памяти
	MaxWorkingMemory int

	// MaxArtifacts — максимум ссылок на артефакты
	MaxArtifacts int

	// ToolResultMaxTokens — макс. токенов для результата инструмента в чате
	ToolResultMaxTokens int

	// ExternalStorePath — путь для хранения больших результатов
	ExternalStorePath string
}

// DefaultCompactionConfig возвращает конфиг по умолчанию
func DefaultCompactionConfig() CompactionConfig {
	return CompactionConfig{
		Thresholds:          DefaultThresholds(),
		KeepLastMessages:    8,
		MaxWorkingMemory:    10,
		MaxArtifacts:        20,
		ToolResultMaxTokens: 500,
		ExternalStorePath:   "./artifacts",
	}
}

// ============================================================
// CompactionResult — результат сжатия
// ============================================================

// CompactionResult содержит результат операции сжатия
type CompactionResult struct {
	// State — новое структурированное состояние
	State *ContextState

	// KeptMessages — сообщения, оставленные без изменений
	KeptMessages []tokenizers.Message

	// SummarizedCount — количество сообщений, которые были сжаты
	SummarizedCount int

	// TokensBefore — токенов до сжатия
	TokensBefore int

	// TokensAfter — токенов после сжатия
	TokensAfter int

	// Level — применённый уровень сжатия
	Level CompactionLevel

	// ArtifactsSaved — артефакты, вынесенные во внешнее хранилище
	ArtifactsSaved []ArtifactRef
}

// CompressionRatio возвращает коэффициент сжатия
func (r *CompactionResult) CompressionRatio() float64 {
	if r.TokensBefore == 0 {
		return 1.0
	}
	return float64(r.TokensAfter) / float64(r.TokensBefore)
}

// ============================================================
// SimpleTokenEstimator — быстрая оценка токенов без API
// ============================================================

// EstimateTokensSimple оценивает количество токенов по тексту
// Использует эвристику: 1 токен ≈ 4 символа для английского, 2-3 для кода
func EstimateTokensSimple(text string) int {
	if len(text) == 0 {
		return 0
	}

	// Базовая оценка: 4 символа на токен
	charCount := len(text)

	// Корректировка для разных типов контента
	newlines := strings.Count(text, "\n")
	spaces := strings.Count(text, " ")

	// Код и структурированный текст имеют больше токенов
	codeFactor := 1.0
	if strings.Contains(text, "{") || strings.Contains(text, "func ") {
		codeFactor = 1.3
	}

	// Эвристика: (символы / 4) + поправка на структуру
	baseTokens := float64(charCount) / 4.0
	structureBonus := float64(newlines+spaces) / 20.0

	return int((baseTokens + structureBonus) * codeFactor)
}

// EstimateMessagesTokensSimple оценивает токены в сообщениях
func EstimateMessagesTokensSimple(messages []tokenizers.Message) int {
	total := 0
	for _, msg := range messages {
		// +4 токена на role и форматирование
		total += EstimateTokensSimple(msg.Content) + 4
	}
	return total
}

// ============================================================
// Compactor — основной интерфейс сжатия
// ============================================================

// Compactor выполняет сжатие контекста по новой стратегии
type Compactor struct {
	config    CompactionConfig
	estimator TokenEstimator
	llm       LLMCompressorInterface
	store     ArtifactStore
}

// TokenEstimator — интерфейс для оценки токенов
type TokenEstimator interface {
	Estimate(text string) int
	EstimateMessages(messages []tokenizers.Message) int
}

// LLMCompressorInterface — интерфейс для LLM-сжатия
type LLMCompressorInterface interface {
	Compress(ctx context.Context, req *CompressionRequest) (*CompressionResult, error)
}

// ArtifactStore — интерфейс для хранения артефактов
type ArtifactStore interface {
	Save(name string, content string) (ArtifactRef, error)
	Load(path string) (string, error)
}

// NewCompactor создаёт новый компрессор
func NewCompactor(config CompactionConfig, llm LLMCompressorInterface, store ArtifactStore) *Compactor {
	return &Compactor{
		config:    config,
		estimator: &SimpleEstimator{},
		llm:       llm,
		store:     store,
	}
}

// SimpleEstimator — простая реализация TokenEstimator
type SimpleEstimator struct{}

func (e *SimpleEstimator) Estimate(text string) int {
	return EstimateTokensSimple(text)
}

func (e *SimpleEstimator) EstimateMessages(messages []tokenizers.Message) int {
	return EstimateMessagesTokensSimple(messages)
}

// CheckAndCompact проверяет и выполняет сжатие при необходимости
func (c *Compactor) CheckAndCompact(ctx context.Context, messages []tokenizers.Message, maxTokens int) (*CompactionResult, error) {
	// Оцениваем текущий размер
	currentTokens := c.estimator.EstimateMessages(messages)
	usagePercent := float64(currentTokens) / float64(maxTokens)

	// Определяем уровень сжатия
	level := c.config.Thresholds.GetLevel(usagePercent)

	if level == CompactionNone {
		return nil, nil // Сжатие не требуется
	}

	// Выполняем сжатие соответствующего уровня
	return c.performCompaction(ctx, messages, level, maxTokens)
}

// performCompaction выполняет сжатие заданного уровня
func (c *Compactor) performCompaction(ctx context.Context, messages []tokenizers.Message, level CompactionLevel, maxTokens int) (*CompactionResult, error) {
	result := &CompactionResult{
		Level:        level,
		TokensBefore: c.estimator.EstimateMessages(messages),
	}

	// Определяем сколько сообщений оставить
	keepCount := c.config.KeepLastMessages
	if level == CompactionAggressive {
		keepCount = 4 // Минимум при агрессивном сжатии
	}

	// Разделяем сообщения
	var toSummarize, toKeep []tokenizers.Message
	if len(messages) > keepCount {
		toSummarize = messages[:len(messages)-keepCount]
		toKeep = messages[len(messages)-keepCount:]
	} else {
		toKeep = messages
	}

	// Обрабатываем tool results в оставшихся сообщениях
	clearedMessages := c.clearToolResults(toKeep)

	// Создаём состояние из старых сообщений
	state, artifacts := c.extractState(toSummarize)
	result.State = state
	result.ArtifactsSaved = artifacts
	result.KeptMessages = clearedMessages
	result.SummarizedCount = len(toSummarize)

	// Оцениваем результат
	result.TokensAfter = c.estimator.EstimateMessages(result.KeptMessages)
	result.TokensAfter += result.State.EstimateTokens()

	return result, nil
}

// clearToolResults очищает большие tool results
func (c *Compactor) clearToolResults(messages []tokenizers.Message) []tokenizers.Message {
	result := make([]tokenizers.Message, len(messages))

	for i, msg := range messages {
		cleared := msg

		// Если это tool result и он большой
		if msg.Role == "tool" && c.estimator.Estimate(msg.Content) > c.config.ToolResultMaxTokens {
			// Создаём краткую версию
			cleared.Content = c.summarizeToolResult(msg.Content)
		}

		result[i] = cleared
	}

	return result
}

// summarizeToolResult создаёт краткую версию tool result
func (c *Compactor) summarizeToolResult(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= 10 {
		return content
	}

	// Оставляем первые и последние строки
	var sb strings.Builder
	sb.WriteString("[COMPRESSED TOOL OUTPUT]\n")
	sb.WriteString(strings.Join(lines[:5], "\n"))
	sb.WriteString("\n... (")
	sb.WriteString(fmt.Sprintf("%d lines truncated", len(lines)-10))
	sb.WriteString(") ...\n")
	sb.WriteString(strings.Join(lines[len(lines)-5:], "\n"))

	return sb.String()
}

// extractState извлекает структурированное состояние из сообщений
func (c *Compactor) extractState(messages []tokenizers.Message) (*ContextState, []ArtifactRef) {
	state := &ContextState{
		LastUpdated: time.Now(),
	}

	var artifacts []ArtifactRef

	// Простая эвристика для извлечения информации
	for _, msg := range messages {
		content := msg.Content

		// Ищем файлы
		c.extractFiles(content, &artifacts)

		// Ищем решения (ключевые слова)
		c.extractDecisions(content, &state.Decisions)

		// Добавляем в рабочую память важные факты
		if len(state.WorkingMemory) < c.config.MaxWorkingMemory {
			facts := c.extractFacts(content)
			state.WorkingMemory = append(state.WorkingMemory, facts...)
		}
	}

	// Ограничиваем размеры
	if len(state.WorkingMemory) > c.config.MaxWorkingMemory {
		state.WorkingMemory = state.WorkingMemory[:c.config.MaxWorkingMemory]
	}
	if len(artifacts) > c.config.MaxArtifacts {
		artifacts = artifacts[:c.config.MaxArtifacts]
	}

	return state, artifacts
}

// extractFiles извлекает ссылки на файлы из текста
func (c *Compactor) extractFiles(content string, artifacts *[]ArtifactRef) {
	// Простые паттерны для файлов
	patterns := []string{".go", ".md", ".json", ".txt", ".yaml", ".yml"}

	for _, pattern := range patterns {
		idx := 0
		for {
			pos := strings.Index(content[idx:], pattern)
			if pos == -1 {
				break
			}

			// Извлекаем путь
			start := idx + pos
			end := start + len(pattern)

			// Ищем начало пути
			pathStart := start
			for pathStart > 0 && content[pathStart-1] != ' ' && content[pathStart-1] != '\n' {
				pathStart--
			}

			path := content[pathStart:end]
			if len(path) > 2 && len(path) < 200 {
				*artifacts = append(*artifacts, ArtifactRef{
					Type:        "file",
					Path:        path,
					Description: "referenced file",
				})
			}

			idx = end
		}
	}
}

// extractDecisions извлекает решения из текста
func (c *Compactor) extractDecisions(content string, decisions *[]string) {
	keywords := []string{"decided", "will use", "chosen", "selected", "agreed", "determined"}

	lowerContent := strings.ToLower(content)
	for _, kw := range keywords {
		if strings.Contains(lowerContent, kw) {
			// Извлекаем предложение с ключевым словом
			sentences := strings.Split(content, ". ")
			for _, s := range sentences {
				if strings.Contains(strings.ToLower(s), kw) && len(s) < 200 {
					*decisions = append(*decisions, strings.TrimSpace(s))
					break
				}
			}
		}
	}
}

// extractFacts извлекает важные факты из текста
func (c *Compactor) extractFacts(content string) []string {
	var facts []string

	// Ищем строки с важной информацией
	keywords := []string{"important:", "note:", "key:", "remember:", "fact:"}

	lowerContent := strings.ToLower(content)
	for _, kw := range keywords {
		if idx := strings.Index(lowerContent, kw); idx != -1 {
			// Извлекаем строку после ключевого слова
			start := idx + len(kw)
			end := start + 100
			if end > len(content) {
				end = len(content)
			}

			fact := strings.TrimSpace(content[start:end])
			if len(fact) > 10 {
				facts = append(facts, fact)
			}
		}
	}

	return facts
}

// ============================================================
// LLM-assisted Compaction
// ============================================================

// CompactWithLLM выполняет сжатие с LLM-суммаризацией
func (c *Compactor) CompactWithLLM(ctx context.Context, messages []tokenizers.Message, level CompactionLevel, maxTokens int) (*CompactionResult, error) {
	result := &CompactionResult{
		Level:        level,
		TokensBefore: c.estimator.EstimateMessages(messages),
	}

	// Определяем сколько сообщений оставить
	keepCount := c.config.KeepLastMessages
	if level == CompactionAggressive {
		keepCount = 4
	}

	// Разделяем сообщения
	toSummarize, toKeep := c.splitMessages(messages, keepCount)

	// Очищаем tool results
	result.KeptMessages = c.clearToolResults(toKeep)

	// Извлекаем состояние из старых сообщений
	result.State, result.ArtifactsSaved = c.extractState(toSummarize)
	result.SummarizedCount = len(toSummarize)

	// Оцениваем результат
	result.TokensAfter = c.calculateResultTokens(result)

	return result, nil
}

// splitMessages разделяет сообщения на те что нужно сжать и оставить
func (c *Compactor) splitMessages(messages []tokenizers.Message, keepCount int) (toSummarize, toKeep []tokenizers.Message) {
	if len(messages) > keepCount {
		return messages[:len(messages)-keepCount], messages[len(messages)-keepCount:]
	}
	return nil, messages
}

// calculateResultTokens вычисляет итоговое количество токенов
func (c *Compactor) calculateResultTokens(result *CompactionResult) int {
	total := c.estimator.EstimateMessages(result.KeptMessages)
	if result.State != nil {
		total += result.State.EstimateTokens()
	}
	return total
}
