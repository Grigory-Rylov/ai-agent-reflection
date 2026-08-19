package compress

const (
	COMPACTION_BUFFER = 20_000
	OUTPUT_TOKEN_MAX  = 32_000
)


type ProviderTokens struct {
	Total      int 
	Input      int 
	Output     int 
	CacheRead  int 
	CacheWrite int 
}


func (t ProviderTokens) Count() int {
	if t.Total > 0 {
		return t.Total
	}
	return t.Input + t.Output + t.CacheRead + t.CacheWrite
}


func maxOutputTokens(contextLimit int) int {
	if contextLimit <= 0 {
		return OUTPUT_TOKEN_MAX
	}
	if contextLimit < OUTPUT_TOKEN_MAX {
		return contextLimit
	}
	return OUTPUT_TOKEN_MAX
}


func Usable(contextLimit int, reserved *int) int {
	return UsableWithLimits(contextLimit, 0, reserved)
}


func UsableWithLimits(contextLimit, inputLimit int, reserved *int) int {
	if contextLimit <= 0 {
		return 0
	}

	outputMax := maxOutputTokens(contextLimit)

	
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
		
		u = contextLimit - outputMax
	}

	
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


func IsOverflow(currentTokens, contextLimit int, reserved *int) bool {
	return IsOverflowWithLimits(currentTokens, contextLimit, 0, reserved)
}


func IsOverflowWithLimits(currentTokens, contextLimit, inputLimit int, reserved *int) bool {
	if contextLimit <= 0 {
		return false
	}
	return currentTokens >= UsableWithLimits(contextLimit, inputLimit, reserved)
}


func IsOverflowWithProviderTokens(tokens ProviderTokens, contextLimit, inputLimit int, reserved *int) bool {
	if contextLimit <= 0 {
		return false
	}
	return tokens.Count() >= UsableWithLimits(contextLimit, inputLimit, reserved)
}
