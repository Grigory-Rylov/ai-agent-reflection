package compress

const (
	COMPACTION_BUFFER = 20_000
	OUTPUT_TOKEN_MAX  = 32_000
)

func maxOutputTokens(modelContextLimit int) int {
	if modelContextLimit <= 0 {
		return OUTPUT_TOKEN_MAX
	}
	return modelContextLimit
}

func Usable(contextLimit int, reserved *int) int {
	if contextLimit <= 0 {
		return 0
	}
	r := COMPACTION_BUFFER
	if reserved != nil && *reserved > 0 {
		r = *reserved
	} else {
		mo := maxOutputTokens(contextLimit)
		if r > mo {
			r = mo
		}
	}
	if r >= contextLimit {
		r = contextLimit / 2
	}
	u := contextLimit - r
	if u < 0 {
		return 0
	}
	return u
}

func IsOverflow(currentTokens, contextLimit int, reserved *int) bool {
	if contextLimit <= 0 {
		return false
	}
	return currentTokens >= Usable(contextLimit, reserved)
}
