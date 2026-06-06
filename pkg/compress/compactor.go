package compress

import (
	"context"
	"fmt"
	"strings"

	"github.com/opencode/llama-client/pkg/tokenizers"
)

// ============================================================
// Compactor — основной интерфейс сжатия
// ============================================================

// LLMCompressorInterface — интерфейс для LLM-сжатия.
type LLMCompressorInterface interface {
	Compress(ctx context.Context, req *CompressionRequest) (*CompressionResult, error)
}

// ArtifactStore — интерфейс для хранения артефактов.
type ArtifactStore interface {
	Save(name string, content string) (ArtifactRef, error)
	Load(path string) (string, error)
}

// Compactor выполняет сжатие контекста по новой стратегии.
type Compactor struct {
	config    CompactionConfig
	estimator TokenEstimator
	llm       LLMCompressorInterface
	store     ArtifactStore
}

// NewCompactor создаёт новый компрессор.
func NewCompactor(config CompactionConfig, llm LLMCompressorInterface, store ArtifactStore) *Compactor {
	return &Compactor{
		config:    config,
		estimator: &SimpleEstimator{},
		llm:       llm,
		store:     store,
	}
}

// CheckAndCompact проверяет и выполняет сжатие при необходимости.
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

// performCompaction выполняет сжатие заданного уровня.
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

// clearToolResults очищает большие tool results.
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

// summarizeToolResult создаёт краткую версию tool result.
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

// extractState извлекает структурированное состояние из сообщений.
func (c *Compactor) extractState(messages []tokenizers.Message) (*ContextState, []ArtifactRef) {
	state := &ContextState{}

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

// extractFiles извлекает ссылки на файлы из текста.
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

// extractDecisions извлекает решения из текста.
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

// extractFacts извлекает важные факты из текста.
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

// CompactWithLLM выполняет сжатие с LLM-суммаризацией.
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

// splitMessages разделяет сообщения на те что нужно сжать и оставить.
func (c *Compactor) splitMessages(messages []tokenizers.Message, keepCount int) (toSummarize, toKeep []tokenizers.Message) {
	if len(messages) > keepCount {
		return messages[:len(messages)-keepCount], messages[len(messages)-keepCount:]
	}
	return nil, messages
}

// calculateResultTokens вычисляет итоговое количество токенов.
func (c *Compactor) calculateResultTokens(result *CompactionResult) int {
	total := c.estimator.EstimateMessages(result.KeptMessages)
	if result.State != nil {
		total += result.State.EstimateTokens()
	}
	return total
}
