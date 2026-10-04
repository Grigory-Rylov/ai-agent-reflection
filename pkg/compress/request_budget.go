package compress

import (
	"regexp"
	"strconv"
)

const (
	MinOutputTokens          = 1_024
	InputFloorTokens         = 4_096
	OutputSafetyMarginTokens = 2_048
)

var contextOverflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)exceed[^0-9]{0,40}context`),
	regexp.MustCompile(`(?i)context[^0-9]{0,40}exceed`),
	regexp.MustCompile(`(?i)context[_ ]length[_ ]exceeded`),
	regexp.MustCompile(`(?i)too many tokens`),
	regexp.MustCompile(`(?i)token limit exceeded`),
	regexp.MustCompile(`(?i)exceeds the limit of \d+ tokens`),
	regexp.MustCompile(`(?i)maximum context length is \d+ tokens`),
	regexp.MustCompile(`(?i)prompt.{0,40}max tokens.{0,40}exceeds.{0,40}context`),
	regexp.MustCompile(`(?i)request_too_large.{0,120}tokens`),
	regexp.MustCompile(`(?i)model_context_window_exceeded`),
}

var overflowTokenCountsRe = regexp.MustCompile(`(?is)prompt \((\d+) tokens?\).*?max tokens \((\d+)\).*?context \((\d+)\)`)

func OutputCapForWindow(contextWindow int) int {
	if contextWindow <= 0 {
		return OUTPUT_TOKEN_MAX
	}

	outputCap := OUTPUT_TOKEN_MAX
	if room := contextWindow - InputFloorTokens; room < outputCap {
		outputCap = room
	}
	if outputCap < MinOutputTokens {
		return MinOutputTokens
	}
	return outputCap
}

func ClampOutputTokens(promptTokens, contextWindow int) int {
	outputCap := OutputCapForWindow(contextWindow)
	if contextWindow <= 0 {
		return outputCap
	}

	room := contextWindow - promptTokens - OutputSafetyMarginTokens
	if room < MinOutputTokens {
		return MinOutputTokens
	}
	if room < outputCap {
		return room
	}
	return outputCap
}

func FitsContext(promptTokens, outputCap, contextWindow int) bool {
	if contextWindow <= 0 {
		return true
	}
	return promptTokens+outputCap <= contextWindow
}

func IsContextOverflowMessage(text string) bool {
	for _, pattern := range contextOverflowPatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

func ParseOverflowTokenCounts(text string) (int, int, int, bool) {
	match := overflowTokenCountsRe.FindStringSubmatch(text)
	if match == nil {
		return 0, 0, 0, false
	}

	promptTokens, promptErr := strconv.Atoi(match[1])
	maxTokens, maxErr := strconv.Atoi(match[2])
	contextWindow, contextErr := strconv.Atoi(match[3])
	if promptErr != nil || maxErr != nil || contextErr != nil {
		return 0, 0, 0, false
	}
	return promptTokens, maxTokens, contextWindow, true
}
