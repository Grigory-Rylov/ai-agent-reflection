package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
)

type ContextOverflowError struct {
	PromptTokens int
	MaxContext   int
	Message      string
	RawError     string
}

func (e *ContextOverflowError) Error() string {
	return fmt.Sprintf("context overflow: %d tokens exceed max %d", e.PromptTokens, e.MaxContext)
}

func IsContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(*ContextOverflowError); ok {
		return true
	}
	return compress.IsContextOverflowMessage(err.Error())
}

func ParseContextOverflowError(err error) *ContextOverflowError {
	if err == nil {
		return nil
	}

	errStr := err.Error()
	if !compress.IsContextOverflowMessage(errStr) {
		return nil
	}

	overflow := &ContextOverflowError{RawError: errStr, Message: errStr}

	if promptTokens, maxContext, message, ok := parseAPITokenCounts(errStr); ok {
		overflow.PromptTokens = promptTokens
		overflow.MaxContext = maxContext
		overflow.Message = message
		return overflow
	}

	if promptTokens, _, maxContext, ok := compress.ParseOverflowTokenCounts(errStr); ok {
		overflow.PromptTokens = promptTokens
		overflow.MaxContext = maxContext
	}
	return overflow
}

func parseAPITokenCounts(errStr string) (promptTokens, maxContext int, message string, ok bool) {
	idx := strings.Index(errStr, "{")
	if idx < 0 {
		return 0, 0, "", false
	}

	var apiError struct {
		Error struct {
			Code         interface{} `json:"code"`
			Message      string      `json:"message"`
			Type         string      `json:"type"`
			PromptTokens int         `json:"n_prompt_tokens"`
			MaxContext   int         `json:"n_ctx"`
		} `json:"error"`
	}

	if err := json.Unmarshal([]byte(errStr[idx:]), &apiError); err != nil {
		return 0, 0, "", false
	}
	if apiError.Error.PromptTokens <= 0 && apiError.Error.MaxContext <= 0 {
		return 0, 0, "", false
	}
	return apiError.Error.PromptTokens, apiError.Error.MaxContext, apiError.Error.Message, true
}

func ContextOverflowStats(err error) (promptTokens, maxContext int, isOverflow bool) {
	overflow := ParseContextOverflowError(err)
	if overflow == nil {
		return 0, 0, false
	}
	return overflow.PromptTokens, overflow.MaxContext, true
}
