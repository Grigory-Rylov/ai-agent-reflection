package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ============================================================
// Context Overflow Error Handling
// ============================================================

// ContextOverflowError представляет ошибку превышения контекста
type ContextOverflowError struct {
	PromptTokens int
	MaxContext   int
	Message      string
	RawError     string
}

// Error реализует интерфейс error
func (e *ContextOverflowError) Error() string {
	return fmt.Sprintf("context overflow: %d tokens exceed max %d", e.PromptTokens, e.MaxContext)
}

// IsContextOverflowError проверяет является ли ошибка превышением контекста
func IsContextOverflowError(err error) bool {
	_, ok := err.(*ContextOverflowError)
	if ok {
		return true
	}
	// Также проверяем по тексту ошибки
	if err != nil {
		errStr := strings.ToLower(err.Error())
		return strings.Contains(errStr, "exceed") &&
			strings.Contains(errStr, "context")
	}
	return false
}

// ParseContextOverflowError пытается распарсить ошибку превышения контекста
// Возвращает nil если это не та ошибка
func ParseContextOverflowError(err error) *ContextOverflowError {
	if err == nil {
		return nil
	}

	errStr := err.Error()

	// Проверяем ключевые слова
	if !strings.Contains(errStr, "exceed") || !strings.Contains(errStr, "context") {
		return nil
	}

	overflow := &ContextOverflowError{
		RawError: errStr,
	}

	// Пытаемся распарсить JSON из ошибки
	// Формат llama-server: {"error":{"code":400,"message":"...","n_prompt_tokens":100010,"n_ctx":64000}}
	if idx := strings.Index(errStr, "{"); idx >= 0 {
		jsonPart := errStr[idx:]

		// Пробуем формат llama-server
		var apiError struct {
			Error struct {
				Code         interface{} `json:"code"` // может быть string или number
				Message      string      `json:"message"`
				Type         string      `json:"type"`
				PromptTokens int         `json:"n_prompt_tokens"`
				MaxContext   int         `json:"n_ctx"`
			} `json:"error"`
		}

		if parseErr := json.Unmarshal([]byte(jsonPart), &apiError); parseErr == nil {
			if apiError.Error.PromptTokens > 0 || apiError.Error.MaxContext > 0 {
				overflow.PromptTokens = apiError.Error.PromptTokens
				overflow.MaxContext = apiError.Error.MaxContext
				overflow.Message = apiError.Error.Message
				return overflow
			}
		}
	}

	// Fallback: парсим числа из строки
	overflow.Message = errStr
	return overflow
}

// ContextOverflowStats возвращает статистику ошибки превышения контекста
func ContextOverflowStats(err error) (promptTokens, maxContext int, isOverflow bool) {
	overflow := ParseContextOverflowError(err)
	if overflow == nil {
		return 0, 0, false
	}
	return overflow.PromptTokens, overflow.MaxContext, true
}
