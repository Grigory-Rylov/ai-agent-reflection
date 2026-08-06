package compress

const (
	COMPACTION_BUFFER = 20_000
	OUTPUT_TOKEN_MAX  = 32_000
)

// ProviderTokens — токены от провайдера (как в opencode SessionV1.Assistant.tokens).
// Используется для точной проверки переполнения контекста.
type ProviderTokens struct {
	Total      int // общий счётчик от провайдера
	Input      int // input токены
	Output     int // output токены
	CacheRead  int // cache read токены
	CacheWrite int // cache write токены
}

// Count возвращает общий счётчик токенов: total || input+output+cache.read+cache.write
// (как в opencode isOverflow: input.tokens.total || input.tokens.input + input.tokens.output + ...)
func (t ProviderTokens) Count() int {
	if t.Total > 0 {
		return t.Total
	}
	return t.Input + t.Output + t.CacheRead + t.CacheWrite
}

// maxOutputTokens возвращает макс. токены для вывода.
// Как в opencode: min(context, outputTokenMax) где outputTokenMax по умолчанию 32000.
func maxOutputTokens(contextLimit int) int {
	if contextLimit <= 0 {
		return OUTPUT_TOKEN_MAX
	}
	if contextLimit < OUTPUT_TOKEN_MAX {
		return contextLimit
	}
	return OUTPUT_TOKEN_MAX
}

// Usable возвращает usable токены для компактизации.
// Поддерживает model.limit.input отдельно от context (как в opencode overflow.ts):
//   model.limit.input ? max(0, input - reserved) : max(0, context - maxOutputTokens)
func Usable(contextLimit int, reserved *int) int {
	return UsableWithLimits(contextLimit, 0, reserved)
}

// UsableWithLimits — как Usable, но с поддержкой inputLimit (model.limit.input).
// Как в opencode overflow.ts:
//   model.limit.input ? max(0, input - reserved) : max(0, context - maxOutputTokens)
// где reserved = cfg.compaction.reserved ?? min(COMPACTION_BUFFER, maxOutputTokens).
func UsableWithLimits(contextLimit, inputLimit int, reserved *int) int {
	if contextLimit <= 0 {
		return 0
	}

	outputMax := maxOutputTokens(contextLimit)

	// reserved для inputLimit ветки
	r := COMPACTION_BUFFER
	if reserved != nil && *reserved > 0 {
		r = *reserved
	} else if r > outputMax {
		r = outputMax
	}

	var u int
	if inputLimit > 0 {
		u = inputLimit - r
	} else {
		// Когда нет inputLimit: context - maxOutputTokens (не reserved!)
		u = contextLimit - outputMax
	}

	// Если reserved >= context, используем половину
	if r >= contextLimit {
		r = contextLimit / 2
		if inputLimit > 0 {
			u = inputLimit - r
		} else {
			u = contextLimit - r
		}
	}

	if u < 0 {
		return 0
	}
	return u
}

// IsOverflow — простая проверка переполнения с эвристической оценкой токенов.
func IsOverflow(currentTokens, contextLimit int, reserved *int) bool {
	if contextLimit <= 0 {
		return false
	}
	return currentTokens >= Usable(contextLimit, reserved)
}

// IsOverflowWithProviderTokens — проверка переполнения с provider-reported токенами.
// Как в opencode: tokens.total || input+output+cache.read+cache.write >= usable(...)
func IsOverflowWithProviderTokens(tokens ProviderTokens, contextLimit, inputLimit int, reserved *int) bool {
	if contextLimit <= 0 {
		return false
	}
	return tokens.Count() >= UsableWithLimits(contextLimit, inputLimit, reserved)
}
