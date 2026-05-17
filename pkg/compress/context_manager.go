package compress

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/opencode/llama-client/pkg/tokenizers"
)

// ============================================================
// ContextManager — менеджер управления контекстом
// ============================================================

// ContextManager управляет контекстом чата и автоматически сжимает его
type ContextManager struct {
	compressor   Compressor
	tokenizer    tokenizers.Tokenizer
	trigger      CompressionTrigger
	trackTokens  bool
	mu           sync.RWMutex
	peerContexts map[int64]*PeerContext
}

// PeerContext — контекст для одного пользователя
type PeerContext struct {
	tokens      int
	lastCheck   time.Time
	compressing bool
}

// ============================================================
// Agent — структура для работы с агентами
// ============================================================

// AgentConfig конфигурация агента для менеджера контекста
type AgentConfig struct {
	ServerURL  string
	Model      string
	MaxTokens  int
	Temperature float64
	Strategy   CompressionStrategy
}

// NewContextManager создаёт новый менеджер контекста
func NewContextManager(compressor Compressor, tokenizer tokenizers.Tokenizer, trigger CompressionTrigger) *ContextManager {
	return &ContextManager{
		compressor:   compressor,
		tokenizer:    tokenizer,
		trigger:      trigger,
		trackTokens:  true,
		peerContexts: make(map[int64]*PeerContext),
	}
}

// NewAgentContextManager создаёт менеджер контекста с LLMCompressor
func NewAgentContextManager(config AgentConfig) *ContextManager {
	compressor := NewLLMCompressor(config.ServerURL, config.Model, config.Temperature)
	tokenizer := tokenizers.NewLlamaServerTokenizer(config.ServerURL, config.Model, config.MaxTokens)
	trigger := DefaultCompressionTrigger()

	return NewContextManager(compressor, tokenizer, trigger)
}

// CheckAndCompress проверяет и при необходимости сжимает контекст
func (m *ContextManager) CheckAndCompress(ctx context.Context, peerID int64, messages []tokenizers.Message, maxTokens int) error {
	// Получаем или создаём контекст пирa
	peerCtx := m.getPeerContext(peerID)

	// Считаем токены если нужно
	if !m.trackTokens || peerCtx.tokens == 0 {
		tokens, err := m.countTokens(messages)
		if err != nil {
			// Если не удалось посчитать — пропускаем проверку
			return nil
		}
		peerCtx.tokens = tokens
	}

	// Проверяем триггер
	if !m.compressor.CheckTrigger(peerCtx.tokens, maxTokens) {
		return nil
	}

	// Блокируем от параллельного сжатия
	m.mu.Lock()
	if peerCtx.compressing {
		m.mu.Unlock()
		return nil
	}
	peerCtx.compressing = true
	m.mu.Unlock()

	// Выполняем сжатие
	result, err := m.compressContext(ctx, peerID, messages)
	if err != nil {
		m.mu.Lock()
		peerCtx.compressing = false
		m.mu.Unlock()
		return fmt.Errorf("compression failed: %w", err)
	}

	// Обновляем состояние
	m.mu.Lock()
	peerCtx.tokens = result.CompressedTokens
	peerCtx.compressing = false
	m.mu.Unlock()

	return nil
}

// countTokens подсчитывает токены в сообщениях
func (m *ContextManager) countTokens(messages []tokenizers.Message) (int, error) {
	total := 0
	for _, msg := range messages {
		count, err := m.tokenizer.CountTokens(msg.Content)
		if err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

// compressContext выполняет сжатие контекста
func (m *ContextManager) compressContext(ctx context.Context, peerID int64, messages []tokenizers.Message) (*CompressionResult, error) {
	req := &CompressionRequest{
		Messages:            messages,
		Strategy:            SummarizeStrategy,
		TargetTokens:        1500,
		MaxCompressionRatio: 0.5,
	}

	result, err := m.compressor.Compress(ctx, req)
	if err != nil {
		return nil, err
	}

	// Логируем сжатие
	fmt.Printf("[CONTEXT] %s\n", FormatCompressionReport(result))

	return result, nil
}

// getPeerContext возвращает или создаёт контекст пирa
func (m *ContextManager) getPeerContext(peerID int64) *PeerContext {
	m.mu.RLock()
	peerCtx, exists := m.peerContexts[peerID]
	m.mu.RUnlock()

	if !exists {
		m.mu.Lock()
		peerCtx, exists = m.peerContexts[peerID]
		if !exists {
			peerCtx = &PeerContext{
				lastCheck: time.Now(),
			}
			m.peerContexts[peerID] = peerCtx
		}
		m.mu.Unlock()
	}

	return peerCtx
}

// UpdateTokens обновляет количество токенов для пирa
func (m *ContextManager) UpdateTokens(peerID int64, tokens int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	peerCtx, exists := m.peerContexts[peerID]
	if !exists {
		peerCtx = &PeerContext{
			lastCheck: time.Now(),
		}
		m.peerContexts[peerID] = peerCtx
	}
	peerCtx.tokens = tokens
	peerCtx.lastCheck = time.Now()
}

// GetTokens возвращает текущее количество токенов для пирa
func (m *ContextManager) GetTokens(peerID int64) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	peerCtx, exists := m.peerContexts[peerID]
	if !exists {
		return 0
	}
	return peerCtx.tokens
}

// ClearPeerContext очищает контекст для пирa
func (m *ContextManager) ClearPeerContext(peerID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.peerContexts, peerID)
}
